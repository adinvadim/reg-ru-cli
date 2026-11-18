package regapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

// ReregBid is one row of domain/get_user_rereg_bids: a backorder lot the
// account has bid on, including lots where the bid was outbid.
type ReregBid struct {
	Name            string `json:"name"`
	Zone            string `json:"zone"`
	Active          bool   `json:"active"`
	UserBid         string `json:"userBid"`
	MaxBid          string `json:"maxBid"`
	NextPrice       string `json:"nextPrice,omitempty"`
	BlitzPrice      string `json:"blitzPrice,omitempty"`
	DeleteDate      string `json:"deleteDate,omitempty"`
	Registrar       string `json:"registrar,omitempty"`
	FirstCreateDate string `json:"firstCreateDate,omitempty"`
}

// ReregBids is the domain/get_user_rereg_bids answer: own bids plus the
// per-zone bid ladder in roubles.
type ReregBids struct {
	Bids    []ReregBid          `json:"bids"`
	Ladders map[string][]string `json:"ladders"`
}

// ReregBidRequest is the only domain/set_rereg_bids shape the CLI sends: one
// name, one ladder price, nameservers, and either the account's saved contact
// profile or a contacts hash from the credential process.
type ReregBidRequest struct {
	Name        string
	Price       string
	Nameservers []string
	ProfileType string
	ProfileName string
	Contacts    map[string]string
}

// ReregBidResult is a recognised domain/set_rereg_bids success.
type ReregBidResult struct {
	Name      string `json:"name"`
	ServiceID string `json:"serviceId,omitempty"`
	BillID    string `json:"billId"`
	Payment   string `json:"payment"`
	PayType   string `json:"payType,omitempty"`
}

// GetUserReregBids reads the account's backorder bids and the bid ladder.
func (c *Client) GetUserReregBids(ctx context.Context) (ReregBids, error) {
	raw, err := c.call(ctx, "domain/get_user_rereg_bids", map[string]any{}, false)
	if err != nil {
		return ReregBids{}, err
	}
	var answer struct {
		Domains *[]struct {
			Name            string          `json:"dname"`
			Zone            string          `json:"tld"`
			Active          json.RawMessage `json:"is_active"`
			UserBid         json.RawMessage `json:"user_bid"`
			MaxBid          json.RawMessage `json:"max_bid"`
			NextPrice       json.RawMessage `json:"next_price"`
			BlitzPrice      json.RawMessage `json:"blitz_price"`
			DeleteDate      string          `json:"delete_date"`
			Registrar       string          `json:"registrar"`
			FirstCreateDate string          `json:"first_create_date"`
		} `json:"domains"`
		Ladders map[string][]json.RawMessage `json:"rereg_bids"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil || answer.Domains == nil {
		return ReregBids{}, &ContractError{Message: "decode REG.API domain/get_user_rereg_bids response"}
	}
	result := ReregBids{Bids: make([]ReregBid, 0, len(*answer.Domains)), Ladders: map[string][]string{}}
	for zone, steps := range answer.Ladders {
		ladder := make([]string, 0, len(steps))
		for _, step := range steps {
			value, ok := flexDecimal(step)
			if !ok || value == "" {
				return ReregBids{}, &ContractError{Message: "REG.API rereg bid ladder is not decimal"}
			}
			ladder = append(ladder, value)
		}
		result.Ladders[strings.ToLower(zone)] = ladder
	}
	for _, item := range *answer.Domains {
		userBid, userOK := flexDecimal(item.UserBid)
		maxBid, maxOK := flexDecimal(item.MaxBid)
		next, nextOK := flexDecimal(item.NextPrice)
		blitz, blitzOK := flexDecimal(item.BlitzPrice)
		active, activeOK := flexBool(item.Active)
		if item.Name == "" || !userOK || userBid == "" || !maxOK || !nextOK || !blitzOK || !activeOK ||
			item.DeleteDate != "" && !dateOnly(item.DeleteDate) {
			return ReregBids{}, &ContractError{Message: "REG.API rereg bid is incomplete"}
		}
		result.Bids = append(result.Bids, ReregBid{
			Name: strings.ToLower(item.Name), Zone: strings.ToLower(item.Zone), Active: active,
			UserBid: userBid, MaxBid: maxBid, NextPrice: next, BlitzPrice: blitz,
			DeleteDate: item.DeleteDate, Registrar: item.Registrar, FirstCreateDate: item.FirstCreateDate,
		})
	}
	sort.SliceStable(result.Bids, func(i, j int) bool { return result.Bids[i].Name < result.Bids[j].Name })
	return result, nil
}

// SetReregBid sends domain/set_rereg_bids exactly once for one name. REG.API
// charges the balance synchronously and documents no idempotency key, so
// transport failures, 5xx, and unrecognised success bodies return
// AmbiguousMutationError; a per-item refusal returns DomainItemError.
func (c *Client) SetReregBid(ctx context.Context, request ReregBidRequest) (ReregBidResult, error) {
	price, ok := wirePrice(request.Price)
	contactsOK := len(request.Contacts) > 0 != (request.ProfileType != "" || request.ProfileName != "")
	profileOK := len(request.Contacts) > 0 || request.ProfileType != "" && request.ProfileName != ""
	if request.Name == "" || !ok || len(request.Nameservers) < 2 || !contactsOK || !profileOK {
		return ReregBidResult{}, errors.New("incomplete REG.API domain/set_rereg_bids request")
	}
	input := map[string]any{
		"nss":     nameserverInput(request.Nameservers),
		"domains": []map[string]any{{"dname": request.Name, "price": price}},
	}
	if len(request.Contacts) > 0 {
		input["contacts"] = request.Contacts
	} else {
		input["profile_type"] = request.ProfileType
		input["profile_name"] = request.ProfileName
	}
	raw, err := c.call(ctx, "domain/set_rereg_bids", input, true)
	if err != nil {
		return ReregBidResult{}, err
	}
	ambiguous := func(message string) error {
		return &AmbiguousMutationError{Cause: &ContractError{Message: message}}
	}
	var answer struct {
		BillID  json.RawMessage `json:"bill_id"`
		Payment json.RawMessage `json:"payment"`
		PayType string          `json:"pay_type"`
		Domains []struct {
			Name      string          `json:"dname"`
			Result    string          `json:"result"`
			ServiceID json.RawMessage `json:"service_id"`
			ErrorCode string          `json:"error_code"`
		} `json:"domains"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return ReregBidResult{}, ambiguous("decode REG.API domain/set_rereg_bids response")
	}
	if len(answer.Domains) != 1 {
		return ReregBidResult{}, ambiguous("REG.API domain/set_rereg_bids did not answer for exactly one name")
	}
	item := answer.Domains[0]
	if !sameDomain(item.Name, request.Name) {
		return ReregBidResult{}, ambiguous("REG.API domain/set_rereg_bids answered for another name")
	}
	if item.Result != "success" {
		if item.Result == "" && item.ErrorCode == "" {
			return ReregBidResult{}, ambiguous("REG.API domain/set_rereg_bids item has no outcome")
		}
		code := item.ErrorCode
		if code == "" {
			code = item.Result
		}
		return ReregBidResult{}, &DomainItemError{Name: request.Name, Code: code, Result: item.Result}
	}
	billID, _ := flexString(answer.BillID)
	payment, paymentOK := flexDecimal(answer.Payment)
	serviceID, _ := flexString(item.ServiceID)
	if !validIdentifier(billID) || !paymentOK || payment == "" || serviceID != "" && !validIdentifier(serviceID) {
		return ReregBidResult{}, ambiguous("REG.API domain/set_rereg_bids outcome is incomplete")
	}
	return ReregBidResult{
		Name: strings.ToLower(request.Name), ServiceID: serviceID, BillID: billID,
		Payment: payment, PayType: answer.PayType,
	}, nil
}

// wirePrice renders a rouble amount as a JSON number, the documented type of
// the set_rereg_bids price.
func wirePrice(value string) (json.Number, bool) {
	if !validDecimal(value) || strings.HasPrefix(value, "-") {
		return "", false
	}
	trimmed := value
	if strings.Contains(trimmed, ".") {
		trimmed = strings.TrimRight(strings.TrimRight(trimmed, "0"), ".")
	}
	if trimmed == "" || trimmed == "0" {
		return "", false
	}
	return json.Number(trimmed), true
}

func flexBool(raw json.RawMessage) (bool, bool) {
	trimmed := bytes.TrimSpace(raw)
	switch string(trimmed) {
	case "", "null", "0", `"0"`, "false", `""`:
		return false, true
	case "1", `"1"`, "true":
		return true, true
	default:
		return false, false
	}
}
