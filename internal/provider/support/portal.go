package support

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/adinvadim/reg-ru-cli/internal/profile"
	"github.com/adinvadim/reg-ru-cli/internal/provider/portal/session"
)

const (
	programSupportRead     session.ProgramID = "portal.support.read"
	programSupportMutation session.ProgramID = "portal.support.mutation"
)

type SessionBroker interface {
	WithSession(context.Context, session.Profile, func(session.PageExecutor) error) error
}

type PortalControlPlane interface {
	List(context.Context, profile.Account, ListRequest) (TicketPage, error)
	Get(context.Context, profile.Account, string) (Ticket, error)
	Mutate(context.Context, profile.Account, MutationRequest) error
}

type ListRequest struct {
	Limit  int    `json:"limit"`
	Page   int    `json:"page"`
	Status string `json:"status"`
}

type TicketSummary struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Preview string `json:"preview"`
}

type Message struct {
	Body    string `json:"body"`
	Created string `json:"created"`
	Sender  string `json:"sender"`
	Kind    string `json:"kind"`
}

type Ticket struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Status   string    `json:"status"`
	Messages []Message `json:"messages"`
}

type TicketPage struct {
	Tickets []TicketSummary `json:"tickets"`
	Total   int             `json:"total"`
}

type MutationRequest struct {
	Action  string `json:"action"`
	ID      string `json:"id,omitempty"`
	Message string `json:"message,omitempty"`
}

type PortalErrorKind string

const (
	PortalUnauthorized PortalErrorKind = "unauthorized"
	PortalTransport    PortalErrorKind = "transport"
	PortalContract     PortalErrorKind = "contract"
	PortalNotFound     PortalErrorKind = "not-found"
	PortalAmbiguous    PortalErrorKind = "ambiguous"
)

type PortalError struct {
	Kind PortalErrorKind
	Code string
}

func (e *PortalError) Error() string {
	if e.Code != "" {
		return e.Code
	}
	return string(e.Kind)
}

type Portal struct{ broker SessionBroker }

func NewPortal(broker SessionBroker) *Portal { return &Portal{broker: broker} }

func (p *Portal) List(ctx context.Context, account profile.Account, request ListRequest) (TicketPage, error) {
	requestBody := struct {
		Action string `json:"action"`
		ListRequest
	}{Action: "list", ListRequest: request}
	var envelope struct {
		State   string          `json:"state"`
		Status  int             `json:"status"`
		Tickets []TicketSummary `json:"tickets"`
		Total   int             `json:"total"`
	}
	if err := p.onSupportPage(ctx, account, func(page session.PageExecutor) error {
		return runProgram(ctx, page, programSupportRead, requestBody, &envelope)
	}); err != nil {
		return TicketPage{}, err
	}
	if err := portalStateError(envelope.State); err != nil {
		return TicketPage{}, err
	}
	if envelope.Tickets == nil || envelope.Total < len(envelope.Tickets) {
		return TicketPage{}, &PortalError{Kind: PortalContract}
	}
	return TicketPage{Tickets: envelope.Tickets, Total: envelope.Total}, nil
}

func (p *Portal) Get(ctx context.Context, account profile.Account, id string) (Ticket, error) {
	var envelope struct {
		State  string `json:"state"`
		Ticket Ticket `json:"ticket"`
	}
	if err := p.onSupportPage(ctx, account, func(page session.PageExecutor) error {
		var navigation struct {
			State string `json:"state"`
		}
		err := runProgram(ctx, page, programSupportRead, map[string]any{"action": "navigate", "id": id}, &navigation)
		if err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil {
			if stateErr := portalStateError(navigation.State); stateErr != nil && navigation.State != "navigating" {
				return stateErr
			}
		}
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
		return runProgram(ctx, page, programSupportRead, map[string]any{"action": "detail", "id": id}, &envelope)
	}); err != nil {
		return Ticket{}, err
	}
	if err := portalStateError(envelope.State); err != nil {
		return Ticket{}, err
	}
	if envelope.Ticket.ID != id || envelope.Ticket.Title == "" || envelope.Ticket.Messages == nil {
		return Ticket{}, &PortalError{Kind: PortalContract}
	}
	return envelope.Ticket, nil
}

func (p *Portal) Mutate(ctx context.Context, account profile.Account, request MutationRequest) error {
	var envelope struct {
		State string `json:"state"`
	}
	if err := p.onSupportPage(ctx, account, func(page session.PageExecutor) error {
		var preflight struct {
			State string `json:"state"`
		}
		if err := runProgram(ctx, page, programSupportRead, map[string]any{
			"action": "list",
			"limit":  1,
			"page":   1,
			"status": "all",
		}, &preflight); err != nil {
			return err
		}
		if err := portalStateError(preflight.State); err != nil {
			return err
		}

		if request.Action != "create" {
			var navigation struct {
				State string `json:"state"`
			}
			err := runProgram(ctx, page, programSupportRead, map[string]any{"action": "navigate", "id": request.ID}, &navigation)
			if err != nil && ctx.Err() != nil {
				return ctx.Err()
			}
			if err == nil {
				if stateErr := portalStateError(navigation.State); stateErr != nil && navigation.State != "navigating" {
					return stateErr
				}
			}
			timer := time.NewTimer(time.Second)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := runProgram(ctx, page, programSupportMutation, request, &envelope); err != nil {
			return &PortalError{Kind: PortalAmbiguous, Code: "ambiguous"}
		}
		return nil
	}); err != nil {
		return err
	}
	return portalStateError(envelope.State)
}

func (p *Portal) onSupportPage(ctx context.Context, account profile.Account, use func(session.PageExecutor) error) error {
	if p == nil || p.broker == nil || account.Portal.SessionRef == "" {
		return &PortalError{Kind: PortalUnauthorized, Code: "portal_session_required"}
	}
	return p.broker.WithSession(ctx, session.Profile{ID: account.ID, SessionRef: account.Portal.SessionRef}, func(page session.PageExecutor) error {
		navigator, ok := page.(session.PageNavigator)
		if !ok {
			return &PortalError{Kind: PortalContract, Code: "navigation-unavailable"}
		}
		if err := navigator.Navigate(ctx, "https://www.reg.ru/support/tickets/"); err != nil {
			return err
		}
		return use(page)
	})
}

func runProgram(ctx context.Context, page session.PageExecutor, program session.ProgramID, input, output any) error {
	encoded, err := json.Marshal(input)
	if err != nil {
		return &PortalError{Kind: PortalContract, Code: "argument-encoding"}
	}
	var result json.RawMessage
	if err := page.RunJSON(ctx, program, encoded, &result); err != nil {
		return err
	}
	if err := json.Unmarshal(result, output); err != nil {
		return &PortalError{Kind: PortalContract, Code: "result-decoding"}
	}
	return nil
}

func portalStateError(state string) error {
	switch state {
	case "available", "committed":
		return nil
	case "unauthorized", "principal-drift":
		return &PortalError{Kind: PortalUnauthorized, Code: state}
	case "transport":
		return &PortalError{Kind: PortalTransport, Code: state}
	case "not-found":
		return &PortalError{Kind: PortalNotFound, Code: state}
	case "ambiguous":
		return &PortalError{Kind: PortalAmbiguous, Code: state}
	case "build-drift", "route-drift", "operation-drift", "response-drift", "":
		return &PortalError{Kind: PortalContract, Code: state}
	default:
		return &PortalError{Kind: PortalContract, Code: state}
	}
}

func isPortalKind(err error, kind PortalErrorKind) bool {
	var portalErr *PortalError
	return errors.As(err, &portalErr) && portalErr.Kind == kind
}

func validateTicketID(id string) error {
	if id == "" {
		return fmt.Errorf("ticket ID is empty")
	}
	for _, char := range id {
		if char < '0' || char > '9' {
			return fmt.Errorf("ticket ID is invalid")
		}
	}
	return nil
}
