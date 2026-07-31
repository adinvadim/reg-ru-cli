package regapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const maxResponseSize = 8 << 20

var (
	identifierPattern = regexp.MustCompile(`^[1-9][0-9]{0,63}$`)
	decimalPattern    = regexp.MustCompile(`^-?(?:0|[1-9][0-9]{0,63})(?:\.[0-9]{1,32})?$`)
)

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type ClientOptions struct {
	BaseURL        string
	HTTPClient     HTTPDoer
	RequestTimeout time.Duration
}

type Client struct {
	baseURL        *url.URL
	httpClient     HTTPDoer
	username       []byte
	password       []byte
	requestTimeout time.Duration
}

func New(username, password []byte, options ClientOptions) (*Client, error) {
	baseURL := strings.TrimRight(options.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || parsed.Scheme != "https" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" {
		return nil, errors.New("invalid REG.API base URL")
	}
	if len(username) == 0 || len(password) == 0 {
		return nil, errors.New("REG.API username and password are required")
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	requestTimeout := options.RequestTimeout
	if requestTimeout <= 0 {
		requestTimeout = 30 * time.Second
	}
	return &Client{
		baseURL:        parsed,
		httpClient:     httpClient,
		username:       append([]byte(nil), username...),
		password:       append([]byte(nil), password...),
		requestTimeout: requestTimeout,
	}, nil
}

func (c *Client) Close() {
	wipe(c.username)
	wipe(c.password)
	c.username = nil
	c.password = nil
}

func (c *Client) GetBalance(ctx context.Context, currency string) (Balance, error) {
	if !validCurrency(currency) {
		return Balance{}, errors.New("invalid REG.API currency")
	}
	raw, err := c.call(ctx, "user/get_balance", struct {
		Currency string `json:"currency"`
	}{Currency: currency}, false)
	if err != nil {
		return Balance{}, err
	}
	var wire struct {
		Currency string `json:"currency"`
		Prepay   string `json:"prepay"`
		Blocked  string `json:"blocked"`
		Credit   string `json:"credit"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil || !validCurrency(wire.Currency) || !validDecimal(wire.Prepay) ||
		(wire.Blocked != "" && !validDecimal(wire.Blocked)) || (wire.Credit != "" && !validDecimal(wire.Credit)) {
		return Balance{}, &ContractError{Message: "decode REG.API balance response"}
	}
	return Balance{
		Source: "regapi2", Currency: wire.Currency, Prepay: wire.Prepay,
		Blocked: wire.Blocked, Credit: wire.Credit,
	}, nil
}

func (c *Client) ListUnpaid(ctx context.Context, request ListRequest) (InvoicePage, error) {
	return c.list(ctx, "bill/get_not_payed", request, nil)
}

func (c *Client) ListForPeriod(ctx context.Context, request PeriodRequest) (InvoicePage, error) {
	if !dateOnly(request.StartDate) || !dateOnly(request.EndDate) {
		return InvoicePage{}, errors.New("REG.API period dates are required")
	}
	input := map[string]any{
		"start_date": request.StartDate,
		"end_date":   request.EndDate,
		"limit":      normalizedLimit(request.Limit),
		"offset":     request.Offset,
	}
	if request.PayType != "" {
		input["pay_type"] = request.PayType
	}
	if request.IncludeInactive {
		input["all"] = true
	}
	return c.list(ctx, "bill/get_for_period", ListRequest{
		Limit: normalizedLimit(request.Limit), Offset: request.Offset,
	}, input)
}

func (c *Client) list(
	ctx context.Context,
	operation string,
	request ListRequest,
	input map[string]any,
) (InvoicePage, error) {
	request.Limit = normalizedLimit(request.Limit)
	if request.Limit < 1 || request.Limit > 1024 || request.Offset < 0 {
		return InvoicePage{}, errors.New("invalid REG.API page")
	}
	if input == nil {
		input = map[string]any{"limit": request.Limit, "offset": request.Offset}
	}
	raw, err := c.call(ctx, operation, input, false)
	if err != nil {
		return InvoicePage{}, err
	}
	var answer struct {
		Bills *[]wireInvoice `json:"bills"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil || answer.Bills == nil {
		return InvoicePage{}, &ContractError{Message: "decode REG.API invoice list response"}
	}
	invoices := make([]Invoice, 0, len(*answer.Bills))
	for _, wire := range *answer.Bills {
		invoice, err := normalizeInvoice(wire)
		if err != nil {
			return InvoicePage{}, err
		}
		invoices = append(invoices, invoice)
	}
	return InvoicePage{
		Source: "regapi2", Invoices: invoices, Offset: request.Offset,
		Received: len(invoices), NextOffset: request.Offset + len(invoices),
	}, nil
}

func (c *Client) Status(ctx context.Context, ids []string) ([]InvoiceStatus, error) {
	input, err := billInput(ids)
	if err != nil {
		return nil, err
	}
	raw, err := c.call(ctx, "bill/nop", input, false)
	if err != nil {
		return nil, err
	}
	var answer struct {
		Bills *[]struct {
			ID        string `json:"bill_id"`
			PayStatus string `json:"pay_status"`
		} `json:"bills"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil || answer.Bills == nil {
		return nil, &ContractError{Message: "decode REG.API invoice status response"}
	}
	statuses := make([]InvoiceStatus, 0, len(*answer.Bills))
	for _, wire := range *answer.Bills {
		if !validIdentifier(wire.ID) || wire.PayStatus == "" {
			return nil, &ContractError{Message: "REG.API invoice status is incomplete"}
		}
		statuses = append(statuses, InvoiceStatus{ID: wire.ID, PayStatus: wire.PayStatus})
	}
	return statuses, nil
}

func (c *Client) ChangePaymentType(
	ctx context.Context,
	ids []string,
	payType string,
	currency string,
) ([]PaymentChange, error) {
	if !oneOf(payType, "prepay", "yamoney", "bank") || !oneOf(currency, "RUR", "USD") || payType == "yamoney" && currency != "RUR" {
		return nil, errors.New("invalid REG.API payment type or currency")
	}
	input, err := billInput(ids)
	if err != nil {
		return nil, err
	}
	input["pay_type"] = payType
	input["currency"] = currency
	raw, err := c.call(ctx, "bill/change_pay_type", input, true)
	if err != nil {
		return nil, err
	}
	var answer struct {
		Bills *[]struct {
			ID           string `json:"bill_id"`
			Result       string `json:"result"`
			Currency     string `json:"currency"`
			Payment      string `json:"payment"`
			TotalPayment string `json:"total_payment"`
			OldPayType   string `json:"old_pay_type"`
			PayType      string `json:"pay_type"`
			PayStatus    string `json:"pay_status"`
			ErrorCode    string `json:"error_code"`
		} `json:"bills"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil || answer.Bills == nil {
		return nil, &AmbiguousMutationError{Cause: &ContractError{Message: "decode REG.API payment-type response"}}
	}
	changes := make([]PaymentChange, 0, len(*answer.Bills))
	for _, item := range *answer.Bills {
		if !validIdentifier(item.ID) || item.ErrorCode == "" &&
			(!validCurrency(item.Currency) || !validDecimal(item.Payment) ||
				!validDecimal(item.TotalPayment) || item.PayType == "" || item.PayStatus == "") {
			return nil, &AmbiguousMutationError{Cause: &ContractError{Message: "REG.API payment-type outcome is incomplete"}}
		}
		changes = append(changes, PaymentChange{
			ID: item.ID, Result: item.Result, Currency: item.Currency,
			Payment: item.Payment, TotalPayment: item.TotalPayment,
			OldPayType: item.OldPayType, PayType: item.PayType,
			PayStatus: item.PayStatus, ErrorCode: item.ErrorCode,
		})
	}
	if !sameOutcomeIDs(ids, changes, func(change PaymentChange) string { return change.ID }) {
		return nil, &AmbiguousMutationError{Cause: &ContractError{Message: "REG.API payment-type outcomes do not match the request"}}
	}
	return changes, nil
}

func (c *Client) Delete(ctx context.Context, ids []string) ([]DeleteOutcome, error) {
	input, err := billInput(ids)
	if err != nil {
		return nil, err
	}
	raw, err := c.call(ctx, "bill/delete", input, true)
	if err != nil {
		return nil, err
	}
	var answer struct {
		Bills *[]struct {
			ID        string `json:"bill_id"`
			Result    string `json:"result"`
			Status    string `json:"status"`
			PayStatus string `json:"pay_status"`
			ErrorCode string `json:"error_code"`
		} `json:"bills"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil || answer.Bills == nil {
		return nil, &AmbiguousMutationError{Cause: &ContractError{Message: "decode REG.API delete response"}}
	}
	outcomes := make([]DeleteOutcome, 0, len(*answer.Bills))
	for index := range *answer.Bills {
		item := &(*answer.Bills)[index]
		if !validIdentifier(item.ID) || item.Result == "success" && item.Status != "deleted" ||
			item.Result != "success" && item.ErrorCode == "" {
			return nil, &AmbiguousMutationError{Cause: &ContractError{Message: "REG.API delete outcome is incomplete"}}
		}
		outcomes = append(outcomes, DeleteOutcome{
			ID: item.ID, Result: item.Result, Status: item.Status,
			PayStatus: item.PayStatus, ErrorCode: item.ErrorCode,
			Successful: item.Result == "success" && item.Status == "deleted",
		})
	}
	if !sameOutcomeIDs(ids, outcomes, func(outcome DeleteOutcome) string { return outcome.ID }) {
		return nil, &AmbiguousMutationError{Cause: &ContractError{Message: "REG.API delete outcomes do not match the request"}}
	}
	return outcomes, nil
}

type wireInvoice struct {
	ID           string `json:"bill_id"`
	CreatedAt    string `json:"bill_date"`
	Currency     string `json:"currency"`
	Payment      string `json:"payment"`
	TotalPayment string `json:"total_payment"`
	PayType      string `json:"pay_type"`
	PayStatus    string `json:"pay_status"`
	Items        *[]struct {
		ItemType  string `json:"itemtype"`
		Domain    string `json:"dname"`
		Service   string `json:"servtype"`
		ServiceID string `json:"service_id"`
		Action    string `json:"action"`
	} `json:"items"`
}

func normalizeInvoice(wire wireInvoice) (Invoice, error) {
	if !validIdentifier(wire.ID) || wire.CreatedAt == "" || !validCurrency(wire.Currency) ||
		!validDecimal(wire.Payment) || wire.PayType == "" || wire.PayStatus == "" || wire.Items == nil ||
		(wire.TotalPayment != "" && !validDecimal(wire.TotalPayment)) {
		return Invoice{}, &ContractError{Message: "REG.API invoice is incomplete"}
	}
	items := make([]InvoiceItem, 0, len(*wire.Items))
	for _, item := range *wire.Items {
		if item.ItemType == "" || item.ServiceID != "" && !validIdentifier(item.ServiceID) {
			return Invoice{}, &ContractError{Message: "REG.API invoice item is incomplete"}
		}
		items = append(items, InvoiceItem{
			ItemType: item.ItemType, Domain: item.Domain, Service: item.Service,
			ServiceID: item.ServiceID, Action: item.Action,
		})
	}
	return Invoice{
		Source: "regapi2", ID: wire.ID, CreatedAt: wire.CreatedAt,
		Currency: wire.Currency, Payment: wire.Payment, TotalPayment: wire.TotalPayment,
		PayType: wire.PayType, PayStatus: wire.PayStatus, Items: items,
	}, nil
}

type envelope struct {
	Result      string          `json:"result"`
	Answer      json.RawMessage `json:"answer"`
	ErrorCode   string          `json:"error_code"`
	ErrorText   string          `json:"error_text"`
	ErrorParams json.RawMessage `json:"error_params"`
}

func (c *Client) call(ctx context.Context, operation string, input any, mutation bool) (json.RawMessage, error) {
	inputData, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("encode REG.API input: %w", err)
	}
	form := url.Values{
		"username":      {string(c.username)},
		"password":      {string(c.password)},
		"input_format":  {"json"},
		"input_data":    {string(inputData)},
		"output_format": {"json"},
	}
	requestContext, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()
	requestURL := *c.baseURL
	requestURL.Path = strings.TrimRight(c.baseURL.Path, "/") + "/" + operation
	request, err := http.NewRequestWithContext(
		requestContext,
		http.MethodPost,
		requestURL.String(),
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return nil, fmt.Errorf("build REG.API request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.httpClient.Do(request)
	if err != nil {
		if mutation {
			return nil, &AmbiguousMutationError{Cause: err}
		}
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize+1))
	if err != nil || len(body) > maxResponseSize {
		if err == nil {
			err = errors.New("REG.API response exceeds the safety limit")
		}
		if mutation {
			return nil, &AmbiguousMutationError{Cause: err}
		}
		return nil, err
	}
	var decoded envelope
	decodeErr := json.Unmarshal(body, &decoded)
	if decodeErr == nil && decoded.Result == "error" && decoded.ErrorCode != "" {
		return nil, &APIError{
			StatusCode: response.StatusCode,
			Code:       decoded.ErrorCode,
			Retryable:  decoded.ErrorCode == "SERVICE_UNAVAILABLE" || decoded.ErrorCode == "INTERNAL_ERROR" || decoded.ErrorCode == "BILLING_LOCK",
		}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		apiErr := &APIError{StatusCode: response.StatusCode, Retryable: response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500}
		if mutation && response.StatusCode >= 500 {
			return nil, &AmbiguousMutationError{Cause: apiErr}
		}
		return nil, apiErr
	}
	if decodeErr != nil || decoded.Result != "success" || len(bytes.TrimSpace(decoded.Answer)) == 0 || bytes.Equal(bytes.TrimSpace(decoded.Answer), []byte("null")) {
		contractErr := &ContractError{Message: "decode REG.API success envelope"}
		if mutation {
			return nil, &AmbiguousMutationError{Cause: contractErr}
		}
		return nil, contractErr
	}
	return decoded.Answer, nil
}

func billInput(ids []string) (map[string]any, error) {
	if len(ids) == 0 || len(ids) > 100 {
		return nil, errors.New("one to 100 REG.API invoice IDs are required")
	}
	for _, id := range ids {
		if !validIdentifier(id) {
			return nil, errors.New("invalid REG.API invoice ID")
		}
	}
	if len(ids) == 1 {
		return map[string]any{"bill_id": ids[0]}, nil
	}
	bills := make([]map[string]string, 0, len(ids))
	for _, id := range ids {
		bills = append(bills, map[string]string{"bill_id": id})
	}
	return map[string]any{"bills": bills}, nil
}

func normalizedLimit(value int) int {
	if value == 0 {
		return 100
	}
	return value
}

func validIdentifier(value string) bool { return identifierPattern.MatchString(value) }
func validDecimal(value string) bool    { return decimalPattern.MatchString(value) }
func validCurrency(value string) bool   { return oneOf(value, "RUR", "USD", "EUR", "UAH") }

func dateOnly(value string) bool {
	parsed, err := time.Parse("2006-01-02", value)
	return err == nil && parsed.Format("2006-01-02") == value
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func sameOutcomeIDs[T any](expected []string, actual []T, id func(T) string) bool {
	if len(expected) != len(actual) {
		return false
	}
	remaining := make(map[string]int, len(expected))
	for _, value := range expected {
		remaining[value]++
	}
	for _, value := range actual {
		identifier := id(value)
		if remaining[identifier] == 0 {
			return false
		}
		remaining[identifier]--
	}
	return true
}

func wipe(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
