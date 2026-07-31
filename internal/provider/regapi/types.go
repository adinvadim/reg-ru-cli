package regapi

import (
	"fmt"
	"net/http"
)

const DefaultBaseURL = "https://api.reg.ru/api/regru2"

type Balance struct {
	Source   string `json:"source"`
	Currency string `json:"currency"`
	Prepay   string `json:"prepay"`
	Blocked  string `json:"blocked,omitempty"`
	Credit   string `json:"credit,omitempty"`
}

type InvoiceItem struct {
	ItemType  string `json:"itemType"`
	Domain    string `json:"domain,omitempty"`
	Service   string `json:"service,omitempty"`
	ServiceID string `json:"serviceId,omitempty"`
	Action    string `json:"action,omitempty"`
}

type Invoice struct {
	Source       string        `json:"source"`
	ID           string        `json:"id"`
	CreatedAt    string        `json:"createdAt"`
	Currency     string        `json:"currency"`
	Payment      string        `json:"payment"`
	TotalPayment string        `json:"totalPayment,omitempty"`
	PayType      string        `json:"payType"`
	PayStatus    string        `json:"payStatus"`
	Items        []InvoiceItem `json:"items"`
}

type ListRequest struct {
	Limit  int
	Offset int
}

type PeriodRequest struct {
	StartDate       string
	EndDate         string
	PayType         string
	Limit           int
	Offset          int
	IncludeInactive bool
}

type InvoicePage struct {
	Source     string    `json:"source"`
	Invoices   []Invoice `json:"invoices"`
	Offset     int       `json:"offset"`
	Received   int       `json:"received"`
	NextOffset int       `json:"nextOffset"`
}

type InvoiceStatus struct {
	ID        string `json:"id"`
	PayStatus string `json:"payStatus"`
}

type PaymentChange struct {
	ID           string `json:"id"`
	Result       string `json:"result,omitempty"`
	Currency     string `json:"currency,omitempty"`
	Payment      string `json:"payment,omitempty"`
	TotalPayment string `json:"totalPayment,omitempty"`
	OldPayType   string `json:"oldPayType,omitempty"`
	PayType      string `json:"payType,omitempty"`
	PayStatus    string `json:"payStatus,omitempty"`
	ErrorCode    string `json:"errorCode,omitempty"`
}

type DeleteOutcome struct {
	ID         string `json:"id"`
	Result     string `json:"result,omitempty"`
	Status     string `json:"status,omitempty"`
	PayStatus  string `json:"payStatus,omitempty"`
	ErrorCode  string `json:"errorCode,omitempty"`
	Successful bool   `json:"successful"`
}

type APIError struct {
	StatusCode int
	Code       string
	Retryable  bool
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code == "" {
		return fmt.Sprintf("REG.API returned HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("REG.API returned %s (HTTP %d)", e.Code, e.StatusCode)
}

func (e *APIError) AuthenticationFailure() bool {
	if e == nil {
		return false
	}
	switch e.Code {
	case "NO_USERNAME", "NO_AUTH", "PASSWORD_AUTH_FAILED", "USER_AUTHENTICATION_FAILED",
		"MORE_THAN_ONE_ACCOUNT_WITH_THE_SAME_EMAIL",
		"ACCESS_DENIED", "ACCESS_DENIED_FROM_IP", "ACCOUNT_BLOCKED":
		return true
	default:
		return e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden
	}
}

type ContractError struct{ Message string }

func (e *ContractError) Error() string {
	if e == nil || e.Message == "" {
		return "REG.API response does not match the documented contract"
	}
	return e.Message
}

type AmbiguousMutationError struct{ Cause error }

func (e *AmbiguousMutationError) Error() string { return "REG.API mutation outcome is unknown" }
func (e *AmbiguousMutationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
