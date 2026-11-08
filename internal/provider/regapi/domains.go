package regapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/net/idna"
)

var userServIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// DomainCheck is one per-name answer of the partner-only domain/check.
type DomainCheck struct {
	Name       string `json:"name"`
	Available  bool   `json:"available"`
	Result     string `json:"result,omitempty"`
	ErrorCode  string `json:"errorCode,omitempty"`
	Premium    bool   `json:"premium,omitempty"`
	Price      string `json:"price,omitempty"`
	RenewPrice string `json:"renewPrice,omitempty"`
	Currency   string `json:"currency,omitempty"`
}

// DomainPrice is the per-year price of one zone from domain/get_prices.
type DomainPrice struct {
	Zone             string `json:"zone"`
	Currency         string `json:"currency"`
	RegPrice         string `json:"regPrice"`
	RenewPrice       string `json:"renewPrice,omitempty"`
	RetailRegPrice   string `json:"retailRegPrice,omitempty"`
	RetailRenewPrice string `json:"retailRenewPrice,omitempty"`
	MinPeriod        string `json:"minPeriod,omitempty"`
}

// DomainCreateRequest is the only domain/create shape the CLI sends: one name,
// one year, a saved contact profile, nameservers, a caller-chosen service ID,
// and payment strictly from balance.
type DomainCreateRequest struct {
	Name        string
	ProfileType string
	ProfileName string
	Nameservers []string
	UserServID  string
}

// DomainCreateResult is a recognised domain/create success.
type DomainCreateResult struct {
	Name      string `json:"name"`
	ServiceID string `json:"serviceId"`
	BillID    string `json:"billId,omitempty"`
	Payment   string `json:"payment,omitempty"`
	PayType   string `json:"payType,omitempty"`
}

// DomainItemError is a per-item refusal inside a success envelope.
type DomainItemError struct {
	Name   string
	Code   string
	Result string
}

func (e *DomainItemError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("REG.API refused %s: %s", e.Name, e.Code)
	}
	return fmt.Sprintf("REG.API refused %s", e.Name)
}

// ServiceInfo is the read-after-write view of one service.
type ServiceInfo struct {
	ServiceID      string `json:"serviceId"`
	Name           string `json:"name,omitempty"`
	ServType       string `json:"servtype,omitempty"`
	State          string `json:"state,omitempty"`
	CreationDate   string `json:"creationDate,omitempty"`
	ExpirationDate string `json:"expirationDate,omitempty"`
}

// NameserverUpdate is a recognised domain/update_nss per-item outcome.
type NameserverUpdate struct {
	Name      string `json:"name"`
	ServiceID string `json:"serviceId,omitempty"`
	Result    string `json:"result"`
}

// ValidUserServID reports whether value is a 32-character lower-case hex ID.
func ValidUserServID(value string) bool { return userServIDPattern.MatchString(value) }

// CheckDomains runs one batched partner-only domain/check.
func (c *Client) CheckDomains(ctx context.Context, names []string) ([]DomainCheck, error) {
	if len(names) == 0 || len(names) > 100 {
		return nil, errors.New("one to 100 domain names are required")
	}
	items := make([]map[string]string, 0, len(names))
	for _, name := range names {
		items = append(items, map[string]string{"dname": name})
	}
	raw, err := c.call(ctx, "domain/check", map[string]any{
		"domains": items, "premium_as_taken": 0, "currency": "RUR",
	}, false)
	if err != nil {
		return nil, err
	}
	var answer struct {
		Domains *[]struct {
			Name       string          `json:"dname"`
			Result     string          `json:"result"`
			ErrorCode  string          `json:"error_code"`
			Premium    json.RawMessage `json:"is_premium"`
			Price      json.RawMessage `json:"price"`
			RenewPrice json.RawMessage `json:"renew_price"`
			Currency   string          `json:"currency"`
		} `json:"domains"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil || answer.Domains == nil {
		return nil, &ContractError{Message: "decode REG.API domain/check response"}
	}
	checks := make([]DomainCheck, 0, len(*answer.Domains))
	for _, item := range *answer.Domains {
		price, priceOK := flexDecimal(item.Price)
		renew, renewOK := flexDecimal(item.RenewPrice)
		if item.Name == "" || !priceOK || !renewOK || item.Result != "Available" && item.ErrorCode == "" {
			return nil, &ContractError{Message: "REG.API domain/check item is incomplete"}
		}
		premium, _ := flexString(item.Premium)
		checks = append(checks, DomainCheck{
			Name: strings.ToLower(item.Name), Available: item.Result == "Available" && item.ErrorCode == "",
			Result: item.Result, ErrorCode: item.ErrorCode, Premium: premium == "1",
			Price: price, RenewPrice: renew, Currency: item.Currency,
		})
	}
	return checks, nil
}

// GetDomainPrices returns per-zone registration and renewal prices.
func (c *Client) GetDomainPrices(ctx context.Context, currency string) (map[string]DomainPrice, error) {
	if !validCurrency(currency) {
		return nil, errors.New("invalid REG.API currency")
	}
	raw, err := c.call(ctx, "domain/get_prices", map[string]any{
		"show_renew_data": 1, "currency": currency,
	}, false)
	if err != nil {
		return nil, err
	}
	var answer struct {
		Currency string                                `json:"currency"`
		Prices   map[string]map[string]json.RawMessage `json:"prices"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil || answer.Prices == nil {
		return nil, &ContractError{Message: "decode REG.API domain/get_prices response"}
	}
	responseCurrency := answer.Currency
	if responseCurrency == "" {
		responseCurrency = currency
	}
	prices := make(map[string]DomainPrice, len(answer.Prices))
	for zone, fields := range answer.Prices {
		regPrice, regOK := flexDecimal(fields["reg_price"])
		renewPrice, renewOK := flexDecimal(fields["renew_price"])
		retailReg, retailRegOK := flexDecimal(fields["retail_reg_price"])
		retailRenew, retailRenewOK := flexDecimal(fields["retail_renew_price"])
		minPeriod, _ := flexString(fields["reg_min_period"])
		if !regOK || !renewOK || !retailRegOK || !retailRenewOK {
			return nil, &ContractError{Message: "REG.API domain price is not a decimal"}
		}
		prices[strings.ToLower(zone)] = DomainPrice{
			Zone: strings.ToLower(zone), Currency: responseCurrency, RegPrice: regPrice, RenewPrice: renewPrice,
			RetailRegPrice: retailReg, RetailRenewPrice: retailRenew, MinPeriod: minPeriod,
		}
	}
	return prices, nil
}

// CreateDomain sends domain/create exactly once. Transport failures, 5xx, and
// unrecognised success bodies return AmbiguousMutationError; per-item refusals
// return DomainItemError.
func (c *Client) CreateDomain(ctx context.Context, request DomainCreateRequest) (DomainCreateResult, error) {
	if request.Name == "" || request.ProfileType == "" || request.ProfileName == "" ||
		!ValidUserServID(request.UserServID) || len(request.Nameservers) < 2 {
		return DomainCreateResult{}, errors.New("incomplete REG.API domain/create request")
	}
	raw, err := c.call(ctx, "domain/create", map[string]any{
		"domain_name":  request.Name,
		"period":       1,
		"profile_type": request.ProfileType,
		"profile_name": request.ProfileName,
		"nss":          nameserverInput(request.Nameservers),
		"user_servid":  request.UserServID,
		"pay_type":     "prepay",
	}, true)
	if err != nil {
		return DomainCreateResult{}, err
	}
	var answer struct {
		BillID    json.RawMessage `json:"bill_id"`
		Payment   json.RawMessage `json:"payment"`
		PayType   string          `json:"pay_type"`
		Name      string          `json:"dname"`
		ServiceID json.RawMessage `json:"service_id"`
		Domains   []struct {
			Name      string          `json:"dname"`
			Result    string          `json:"result"`
			ServiceID json.RawMessage `json:"service_id"`
			ErrorCode string          `json:"error_code"`
		} `json:"domains"`
	}
	ambiguous := func(message string) error {
		return &AmbiguousMutationError{Cause: &ContractError{Message: message}}
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return DomainCreateResult{}, ambiguous("decode REG.API domain/create response")
	}
	billID, _ := flexString(answer.BillID)
	payment, paymentOK := flexDecimal(answer.Payment)
	result := DomainCreateResult{Name: request.Name, BillID: billID, Payment: payment, PayType: answer.PayType}
	switch {
	case len(answer.Domains) == 1:
		item := answer.Domains[0]
		if item.Name != "" && !sameDomain(item.Name, request.Name) {
			return DomainCreateResult{}, ambiguous("REG.API domain/create answered for another name")
		}
		if item.Result != "success" {
			if item.ErrorCode == "" && item.Result == "" {
				return DomainCreateResult{}, ambiguous("REG.API domain/create item has no outcome")
			}
			return DomainCreateResult{}, &DomainItemError{Name: request.Name, Code: item.ErrorCode, Result: item.Result}
		}
		result.ServiceID, _ = flexString(item.ServiceID)
	case len(answer.Domains) == 0:
		if answer.Name != "" && !sameDomain(answer.Name, request.Name) {
			return DomainCreateResult{}, ambiguous("REG.API domain/create answered for another name")
		}
		result.ServiceID, _ = flexString(answer.ServiceID)
	default:
		return DomainCreateResult{}, ambiguous("REG.API domain/create answered for several names")
	}
	if !validIdentifier(result.ServiceID) || billID != "" && !validIdentifier(billID) || !paymentOK {
		return DomainCreateResult{}, ambiguous("REG.API domain/create outcome is incomplete")
	}
	return result, nil
}

// ServiceByUserServID looks a service up by the caller-defined user_servid.
// found is false only when REG.API answers with a recognised not-found code.
func (c *Client) ServiceByUserServID(ctx context.Context, userServID string) (ServiceInfo, bool, error) {
	if !ValidUserServID(userServID) {
		return ServiceInfo{}, false, errors.New("invalid user_servid")
	}
	raw, err := c.call(ctx, "service/get_info", map[string]any{"user_servid": userServID}, false)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && serviceNotFound(apiErr.Code) {
			return ServiceInfo{}, false, nil
		}
		return ServiceInfo{}, false, err
	}
	var answer struct {
		Services *[]wireService `json:"services"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil || answer.Services == nil {
		return ServiceInfo{}, false, &ContractError{Message: "decode REG.API service/get_info response"}
	}
	if len(*answer.Services) != 1 {
		return ServiceInfo{}, false, &ContractError{Message: "REG.API service/get_info did not identify one service"}
	}
	item := (*answer.Services)[0]
	if serviceNotFound(item.ErrorCode) {
		return ServiceInfo{}, false, nil
	}
	info, err := item.normalize()
	if err != nil {
		return ServiceInfo{}, false, err
	}
	return info, true, nil
}

// ListDomains returns the current domain services of the account.
func (c *Client) ListDomains(ctx context.Context) ([]ServiceInfo, error) {
	raw, err := c.call(ctx, "service/get_list", map[string]any{"servtype": "domain"}, false)
	if err != nil {
		return nil, err
	}
	var answer struct {
		Services *[]wireService `json:"services"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil || answer.Services == nil {
		return nil, &ContractError{Message: "decode REG.API service/get_list response"}
	}
	services := make([]ServiceInfo, 0, len(*answer.Services))
	for _, item := range *answer.Services {
		info, err := item.normalize()
		if err != nil {
			return nil, err
		}
		services = append(services, info)
	}
	return services, nil
}

// UpdateNameservers sends domain/update_nss once for one domain.
func (c *Client) UpdateNameservers(ctx context.Context, name string, nameservers []string) (NameserverUpdate, error) {
	if name == "" || len(nameservers) < 2 {
		return NameserverUpdate{}, errors.New("incomplete REG.API domain/update_nss request")
	}
	raw, err := c.call(ctx, "domain/update_nss", map[string]any{
		"domain_name": name, "nss": nameserverInput(nameservers),
	}, true)
	if err != nil {
		return NameserverUpdate{}, err
	}
	var answer struct {
		Domains *[]struct {
			Name      string          `json:"dname"`
			Result    string          `json:"result"`
			ServiceID json.RawMessage `json:"service_id"`
			ErrorCode string          `json:"error_code"`
		} `json:"domains"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil || answer.Domains == nil || len(*answer.Domains) != 1 {
		return NameserverUpdate{}, &AmbiguousMutationError{Cause: &ContractError{Message: "decode REG.API domain/update_nss response"}}
	}
	item := (*answer.Domains)[0]
	if !sameDomain(item.Name, name) {
		return NameserverUpdate{}, &AmbiguousMutationError{Cause: &ContractError{Message: "REG.API domain/update_nss answered for another name"}}
	}
	if item.Result != "success" {
		if item.ErrorCode == "" {
			return NameserverUpdate{}, &AmbiguousMutationError{Cause: &ContractError{Message: "REG.API domain/update_nss outcome is incomplete"}}
		}
		return NameserverUpdate{}, &DomainItemError{Name: name, Code: item.ErrorCode, Result: item.Result}
	}
	serviceID, _ := flexString(item.ServiceID)
	return NameserverUpdate{Name: strings.ToLower(item.Name), ServiceID: serviceID, Result: item.Result}, nil
}

type wireService struct {
	ServiceID      json.RawMessage `json:"service_id"`
	Name           string          `json:"dname"`
	ServType       string          `json:"servtype"`
	State          string          `json:"state"`
	CreationDate   string          `json:"creation_date"`
	ExpirationDate string          `json:"expiration_date"`
	ErrorCode      string          `json:"error_code"`
}

func (w wireService) normalize() (ServiceInfo, error) {
	serviceID, _ := flexString(w.ServiceID)
	if !validIdentifier(serviceID) || w.State == "" || w.ErrorCode != "" {
		return ServiceInfo{}, &ContractError{Message: "REG.API service is incomplete"}
	}
	return ServiceInfo{
		ServiceID: serviceID, Name: strings.ToLower(w.Name), ServType: w.ServType, State: w.State,
		CreationDate: w.CreationDate, ExpirationDate: w.ExpirationDate,
	}, nil
}

func sameDomain(first, second string) bool {
	firstASCII, firstErr := idna.Lookup.ToASCII(first)
	secondASCII, secondErr := idna.Lookup.ToASCII(second)
	return firstErr == nil && secondErr == nil && firstASCII != "" && strings.EqualFold(firstASCII, secondASCII)
}

func serviceNotFound(code string) bool {
	return oneOf(code, "SERVICE_NOT_FOUND", "SERVICE_ID_NOT_FOUND", "NO_SUCH_SERVICE", "SERVICE_NOT_EXISTS")
}

func nameserverInput(nameservers []string) map[string]string {
	input := make(map[string]string, len(nameservers))
	for index, host := range nameservers {
		input[fmt.Sprintf("ns%d", index)] = host
	}
	return input
}

func flexString(raw json.RawMessage) (string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", true
	}
	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		return text, true
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err == nil {
		return number.String(), true
	}
	return "", false
}

func flexDecimal(raw json.RawMessage) (string, bool) {
	value, ok := flexString(raw)
	if !ok {
		return "", false
	}
	if value == "" {
		return "", true
	}
	return value, validDecimal(value)
}
