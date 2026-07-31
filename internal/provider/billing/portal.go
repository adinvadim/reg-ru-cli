package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/adinvadim/reg-ru-cli/internal/profile"
	"github.com/adinvadim/reg-ru-cli/internal/provider/portal/session"
)

const (
	programBillingHistory  session.ProgramID = "portal.billing.history"
	programBillingCheckout session.ProgramID = "portal.billing.checkout"
)

var (
	invoiceIDPattern = regexp.MustCompile(`^[1-9][0-9]*$`)
	decimalPattern   = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]+)?$`)
)

type BillingSessionBroker interface {
	WithSession(context.Context, session.Profile, func(session.PageExecutor) error) error
	Handoff(context.Context, session.Profile, func(session.PageExecutor) error) error
}

type PortalControlPlane interface {
	History(context.Context, profile.Account) (PortalHistory, error)
	OpenCheckout(context.Context, profile.Account, string) (CheckoutHandoff, error)
}

type PortalInvoice struct {
	ID           string `json:"id"`
	Amount       string `json:"amount"`
	State        string `json:"state"`
	PayStatus    string `json:"payStatus"`
	Frozen       bool   `json:"freezed"`
	IsPrepayment bool   `json:"isPrepayment"`
}

type PortalHistory struct {
	Invoices []PortalInvoice `json:"invoices"`
}

type CheckoutHandoff struct {
	Handoff     string `json:"handoff"`
	Destination string `json:"destination"`
	Shareable   bool   `json:"shareable"`
	ExpiresAt   any    `json:"expires_at"`
}

type PortalErrorKind string

const (
	PortalUnavailable  PortalErrorKind = "unavailable"
	PortalUnauthorized PortalErrorKind = "unauthorized"
	PortalNetwork      PortalErrorKind = "network"
	PortalContract     PortalErrorKind = "contract"
	PortalDomain       PortalErrorKind = "domain"
)

type PortalError struct {
	Kind PortalErrorKind
	Code string
	Err  error
}

func (e *PortalError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code != "" {
		return e.Code
	}
	return string(e.Kind)
}

func (e *PortalError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type PortalBillingControlPlane struct{ broker BillingSessionBroker }

func NewPortalBillingControlPlane(broker BillingSessionBroker) *PortalBillingControlPlane {
	return &PortalBillingControlPlane{broker: broker}
}

type billingPortalEnvelope struct {
	State  string          `json:"state"`
	Status int             `json:"status"`
	Items  json.RawMessage `json:"items"`
}

func (c *PortalBillingControlPlane) History(
	ctx context.Context,
	account profile.Account,
) (PortalHistory, error) {
	var envelope billingPortalEnvelope
	if err := c.run(ctx, account, programBillingHistory, nil, false, &envelope); err != nil {
		return PortalHistory{}, err
	}
	if err := billingPortalStateError(envelope); err != nil {
		return PortalHistory{}, err
	}
	if envelope.State != "available" {
		return PortalHistory{}, &PortalError{Kind: PortalContract}
	}
	var invoices []PortalInvoice
	if err := json.Unmarshal(envelope.Items, &invoices); err != nil || invoices == nil {
		return PortalHistory{}, &PortalError{Kind: PortalContract, Err: err}
	}
	seen := make(map[string]struct{}, len(invoices))
	for _, invoice := range invoices {
		if !invoiceIDPattern.MatchString(invoice.ID) || !decimalPattern.MatchString(invoice.Amount) ||
			invoice.State == "" || invoice.PayStatus == "" {
			return PortalHistory{}, &PortalError{Kind: PortalContract}
		}
		if _, exists := seen[invoice.ID]; exists {
			return PortalHistory{}, &PortalError{Kind: PortalContract}
		}
		seen[invoice.ID] = struct{}{}
	}
	return PortalHistory{Invoices: invoices}, nil
}

func (c *PortalBillingControlPlane) OpenCheckout(
	ctx context.Context,
	account profile.Account,
	invoiceID string,
) (CheckoutHandoff, error) {
	if !invoiceIDPattern.MatchString(invoiceID) {
		return CheckoutHandoff{}, &PortalError{Kind: PortalContract}
	}
	var envelope billingPortalEnvelope
	if err := c.run(ctx, account, programBillingCheckout, map[string]string{
		"invoiceId": invoiceID,
	}, true, &envelope); err != nil {
		return CheckoutHandoff{}, err
	}
	if err := billingPortalStateError(envelope); err != nil {
		return CheckoutHandoff{}, err
	}
	if envelope.State != "browser-opened" {
		return CheckoutHandoff{}, &PortalError{Kind: PortalContract}
	}
	return CheckoutHandoff{
		Handoff: "browser_opened", Destination: "reg-ru-checkout",
		Shareable: false, ExpiresAt: nil,
	}, nil
}

func (c *PortalBillingControlPlane) run(
	ctx context.Context,
	account profile.Account,
	program session.ProgramID,
	arguments any,
	visible bool,
	destination any,
) error {
	if c == nil || c.broker == nil || account.Portal.SessionRef == "" {
		return &PortalError{Kind: PortalUnavailable, Code: "portal_session_required"}
	}
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return &PortalError{Kind: PortalContract, Err: err}
	}
	var result json.RawMessage
	use := func(page session.PageExecutor) error {
		return page.RunJSON(ctx, program, encoded, &result)
	}
	portalProfile := session.Profile{ID: account.ID, SessionRef: account.Portal.SessionRef}
	if visible {
		err = c.broker.Handoff(ctx, portalProfile, use)
	} else {
		err = c.broker.WithSession(ctx, portalProfile, use)
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(result, destination); err != nil {
		return &PortalError{Kind: PortalContract, Err: err}
	}
	return nil
}

func billingPortalStateError(envelope billingPortalEnvelope) error {
	switch envelope.State {
	case "available", "browser-opened":
		return nil
	case "unauthorized":
		return &PortalError{Kind: PortalUnauthorized}
	case "network":
		return &PortalError{Kind: PortalNetwork, Code: fmt.Sprintf("http_%d", envelope.Status)}
	case "not-found", "already-paid", "not-payable", "checkout-unavailable":
		return &PortalError{Kind: PortalDomain, Code: envelope.State}
	case "drift", "":
		return &PortalError{Kind: PortalContract}
	default:
		return &PortalError{Kind: PortalContract, Code: envelope.State}
	}
}

func isBillingPortalKind(err error, kind PortalErrorKind) bool {
	var portalErr *PortalError
	return errors.As(err, &portalErr) && portalErr.Kind == kind
}
