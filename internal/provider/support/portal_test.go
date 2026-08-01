package support

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

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
	portal := NewPortal(&fakeSupportBroker{page: page})
	err := portal.Mutate(context.Background(), supportPortalAccount(), MutationRequest{
		Action: "create", Message: "test",
	})
	if !isPortalKind(err, PortalAmbiguous) {
		t.Fatalf("Mutate() error = %v, want ambiguous", err)
	}
	if len(page.programs) != 2 {
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
	portal := NewPortal(&fakeSupportBroker{page: page})
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
	portal := NewPortal(&fakeSupportBroker{page: page})
	err := portal.Mutate(context.Background(), supportPortalAccount(), MutationRequest{
		Action: "create", Message: "test",
	})
	if !isPortalKind(err, PortalAmbiguous) {
		t.Fatalf("Mutate() error = %v, want ambiguous", err)
	}
	if len(page.programs) != 2 || page.programs[0] != programSupportRead || page.programs[1] != programSupportMutation {
		t.Fatalf("programs = %v, want one preflight and one mutation", page.programs)
	}
}
