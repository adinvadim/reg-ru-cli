package support

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/adinvadim/reg-ru-cli/internal/profile"
	"github.com/adinvadim/reg-ru-cli/internal/provider/portal/session"
)

type fakeSupportBroker struct {
	page *fakeSupportPage
}

func (b *fakeSupportBroker) WithSession(
	ctx context.Context,
	_ session.Profile,
	use func(session.PageExecutor) error,
) error {
	return use(b.page)
}

type fakeSupportPage struct {
	responses []json.RawMessage
	errors    []error
	programs  []session.ProgramID
	navigated string
}

func newTestPortal(page *fakeSupportPage) *Portal {
	portal := NewPortal(&fakeSupportBroker{page: page})
	portal.pollInterval = time.Millisecond
	portal.pollTimeout = 10 * time.Millisecond
	return portal
}

func (p *fakeSupportPage) Navigate(_ context.Context, target string) error {
	p.navigated = target
	return nil
}

func (p *fakeSupportPage) RunJSON(
	_ context.Context,
	program session.ProgramID,
	_ json.RawMessage,
	result *json.RawMessage,
) error {
	p.programs = append(p.programs, program)
	if len(p.errors) > 0 {
		err := p.errors[0]
		p.errors = p.errors[1:]
		if err != nil {
			return err
		}
	}
	if len(p.responses) == 0 {
		return errors.New("unexpected program call")
	}
	*result = p.responses[0]
	p.responses = p.responses[1:]
	return nil
}

func TestPortalMutationTreatsProgramInterruptionAsAmbiguous(t *testing.T) {
	page := &fakeSupportPage{
		responses: []json.RawMessage{json.RawMessage(`{"state":"available","tickets":[],"total":0}`)},
		errors:    []error{nil, errors.New("page transition")},
	}
	portal := newTestPortal(page)
	err := portal.Mutate(context.Background(), supportPortalAccount(), MutationRequest{
		Action: "create", Message: "test",
	})
	if !isPortalKind(err, PortalAmbiguous) {
		t.Fatalf("Mutate() error = %v, want ambiguous", err)
	}
	if mutationProgramCalls(page.programs) != 1 {
		t.Fatalf("programs = %v, mutation was unexpectedly retried", page.programs)
	}
}

func supportPortalAccount() profile.Account {
	return profile.Account{
		ID:       "p_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		Provider: "reg.ru",
		Portal:   profile.Portal{SessionRef: "s_fixture"},
	}
}

func TestPortalMutationStopsAtReadPreflightDrift(t *testing.T) {
	page := &fakeSupportPage{responses: []json.RawMessage{
		json.RawMessage(`{"state":"build-drift"}`),
	}}
	portal := newTestPortal(page)
	err := portal.Mutate(context.Background(), supportPortalAccount(), MutationRequest{
		Action: "create", Message: "test",
	})
	if !isPortalKind(err, PortalContract) {
		t.Fatalf("Mutate() error = %v, want contract drift", err)
	}
	if len(page.programs) != 1 || page.programs[0] != programSupportRead {
		t.Fatalf("programs = %v, mutation must not run after drift", page.programs)
	}
	if page.navigated != "https://www.reg.ru/support/tickets/" {
		t.Fatalf("navigated = %q", page.navigated)
	}
}

func TestPortalMutationDispatchesOnceAndPreservesAmbiguousOutcome(t *testing.T) {
	page := &fakeSupportPage{responses: []json.RawMessage{
		json.RawMessage(`{"state":"available","tickets":[],"total":0}`),
		json.RawMessage(`{"state":"ambiguous"}`),
	}}
	portal := newTestPortal(page)
	err := portal.Mutate(context.Background(), supportPortalAccount(), MutationRequest{
		Action: "create", Message: "test",
	})
	if !isPortalKind(err, PortalAmbiguous) {
		t.Fatalf("Mutate() error = %v, want ambiguous", err)
	}
	if mutationProgramCalls(page.programs) != 1 {
		t.Fatalf("programs = %v, mutation was unexpectedly retried", page.programs)
	}
}

func TestPortalListWaitsForRenderedInventory(t *testing.T) {
	page := &fakeSupportPage{responses: []json.RawMessage{
		json.RawMessage(`{"state":"operation-drift"}`),
		json.RawMessage(`{"state":"available","tickets":[],"total":0}`),
		json.RawMessage(`{"state":"available","tickets":[{"id":"123","status":"open","preview":"Test"}],"total":1}`),
	}}
	portal := newTestPortal(page)
	tickets, err := portal.List(context.Background(), supportPortalAccount(), ListRequest{
		Limit: 10, Page: 1, Status: "all",
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(tickets.Tickets) != 1 || tickets.Tickets[0].ID != "123" {
		t.Fatalf("List() tickets = %#v, want rendered inventory", tickets.Tickets)
	}
}

func TestPortalGetWaitsForRenderedMessageHistory(t *testing.T) {
	page := &fakeSupportPage{responses: []json.RawMessage{
		json.RawMessage(`{"state":"operation-drift"}`),
		json.RawMessage(`{"state":"available","tickets":[],"total":0}`),
		json.RawMessage(`{"state":"navigating"}`),
		json.RawMessage(`{"state":"available","ticket":{"id":"123","title":"Test","status":"open","messages":[]}}`),
		json.RawMessage(`{"state":"available","ticket":{"id":"123","title":"Test","status":"open","messages":[{"body":"created"},{"body":"reply"}]}}`),
	}}
	portal := newTestPortal(page)
	ticket, err := portal.Get(context.Background(), supportPortalAccount(), "123")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(ticket.Messages) != 2 {
		t.Fatalf("Get() messages = %#v, want rendered history", ticket.Messages)
	}
}

func TestPortalReconcilesInterruptedDetailMutationWithoutRetry(t *testing.T) {
	for _, action := range []string{"reply", "close"} {
		t.Run(action, func(t *testing.T) {
			page := &fakeSupportPage{
				responses: []json.RawMessage{
					json.RawMessage(`{"state":"available","tickets":[],"total":0}`),
					json.RawMessage(`{"state":"navigating"}`),
					json.RawMessage(`{"state":"available","ticket":{"id":"123","title":"Test","status":"open","messages":[{"body":"created"}]}}`),
					json.RawMessage(`{"state":"available","tickets":[],"total":0}`),
					json.RawMessage(`{"state":"navigating"}`),
					json.RawMessage(`{"state":"committed"}`),
				},
				errors: []error{nil, nil, nil, errors.New("page transition")},
			}
			portal := newTestPortal(page)
			err := portal.Mutate(context.Background(), supportPortalAccount(), MutationRequest{
				Action: action, ID: "123", Message: "reply",
			})
			if err != nil {
				t.Fatalf("Mutate() error = %v, want reconciled success", err)
			}
			if mutationProgramCalls(page.programs) != 1 {
				t.Fatalf("programs = %v, want exactly one mutation", page.programs)
			}
		})
	}
}

func TestPortalReconcilesInterruptedCreateWithoutRetry(t *testing.T) {
	page := &fakeSupportPage{
		responses: []json.RawMessage{
			json.RawMessage(`{"state":"available","tickets":[],"total":0}`),
			json.RawMessage(`{"state":"committed"}`),
		},
		errors: []error{nil, errors.New("page transition")},
	}
	portal := newTestPortal(page)
	err := portal.Mutate(context.Background(), supportPortalAccount(), MutationRequest{
		Action: "create", Message: "created",
	})
	if err != nil {
		t.Fatalf("Mutate() error = %v, want reconciled success", err)
	}
	if mutationProgramCalls(page.programs) != 1 {
		t.Fatalf("programs = %v, want exactly one mutation", page.programs)
	}
}

func mutationProgramCalls(programs []session.ProgramID) int {
	calls := 0
	for _, program := range programs {
		if program == programSupportMutation {
			calls++
		}
	}
	return calls
}
