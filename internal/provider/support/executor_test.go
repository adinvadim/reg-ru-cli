package support

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/adinvadim/reg-ru-cli/internal/cli"
	"github.com/adinvadim/reg-ru-cli/internal/profile"
	"github.com/adinvadim/reg-ru-cli/internal/provider/portal/session"
)

type countingInput struct{ calls int }

func (i *countingInput) Resolve(context.Context) ([]byte, error) {
	i.calls++
	return []byte("private ticket body"), nil
}

func TestExecutorFailsEveryUncapturedCapabilityClosedWithStableReason(t *testing.T) {
	testCases := []struct {
		action     string
		capability string
		reason     Reason
	}{
		{"support.ticket.list", "support.ticket.list", ReasonInventoryContractUncaptured},
		{"support.ticket.get", "support.ticket.show", ReasonDetailContractUncaptured},
		{"support.ticket.create", "support.ticket.create", ReasonCreateContractUnverified},
		{"support.ticket.reply", "support.ticket.reply", ReasonReplyContractUncaptured},
		{"support.ticket.attach", "support.ticket.attachment", ReasonAttachmentContractUncaptured},
		{"support.ticket.close", "support.ticket.close", ReasonCloseContractUncaptured},
		{"support.ticket.reopen", "support.ticket.reopen", ReasonReopenContractUncaptured},
	}
	executor := NewExecutor(nil)
	for _, testCase := range testCases {
		t.Run(testCase.action, func(t *testing.T) {
			input := &countingInput{}
			_, err := executor.Execute(context.Background(), cli.Operation{
				Action: testCase.action, Capability: testCase.capability, Input: input,
			})
			var cliErr *cli.CLIError
			if !errors.As(err, &cliErr) {
				t.Fatalf("error = %v, want *cli.CLIError", err)
			}
			if cliErr.Code != cli.CodeCapability || cliErr.ExitCode != cli.ExitCapability {
				t.Fatalf("error = %#v", cliErr)
			}
			if cliErr.Details["capability"] != testCase.capability || cliErr.Details["reason"] != string(testCase.reason) {
				t.Errorf("details = %#v", cliErr.Details)
			}
			if input.calls != 0 {
				t.Fatalf("unsupported capability resolved private input %d time(s)", input.calls)
			}
		})
	}
}

func TestExecutorDelegatesOtherProviders(t *testing.T) {
	called := false
	fallback := executorFunc(func(_ context.Context, operation cli.Operation) (cli.Result, error) {
		called = true
		return cli.Result{Human: operation.Action}, nil
	})
	result, err := NewExecutor(fallback).Execute(context.Background(), cli.Operation{Action: "billing.balance"})
	if err != nil || !called || result.Human != "billing.balance" {
		t.Fatalf("result = %#v, err = %v, called = %t", result, err, called)
	}
}

type executorFunc func(context.Context, cli.Operation) (cli.Result, error)

func (f executorFunc) Execute(ctx context.Context, operation cli.Operation) (cli.Result, error) {
	return f(ctx, operation)
}

type fakePortal struct {
	page      TicketPage
	ticket    Ticket
	mutations []MutationRequest
	err       error
}

func (p *fakePortal) List(context.Context, profile.Account, ListRequest) (TicketPage, error) {
	return p.page, p.err
}

func (p *fakePortal) Get(context.Context, profile.Account, string) (Ticket, error) {
	return p.ticket, p.err
}

func (p *fakePortal) Mutate(_ context.Context, _ profile.Account, request MutationRequest) error {
	p.mutations = append(p.mutations, request)
	return p.err
}

func configuredExecutor(portal PortalControlPlane) *Executor {
	return NewExecutorWithOptions(ExecutorOptions{
		Profiles: profile.NewMemoryRepository(profile.Config{Accounts: map[string]profile.Account{
			"work": {ID: "p_aaaaaaaaaaaaaaaaaaaaaaaaaa", Provider: "reg.ru", Portal: profile.Portal{SessionRef: "s_fixture"}},
		}}),
		Portal: portal,
	}, nil)
}

func TestConfiguredExecutorListsAndReadsCapturedTickets(t *testing.T) {
	portal := &fakePortal{
		page:   TicketPage{Total: 1, Tickets: []TicketSummary{{ID: "123", Status: "open", Preview: "Test"}}},
		ticket: Ticket{ID: "123", Title: "Test", Status: "open", Messages: []Message{{Body: "Test", Created: "today", Sender: "customer", Kind: "customer"}}},
	}
	executor := configuredExecutor(portal)
	list, err := executor.Execute(context.Background(), cli.Operation{
		Action: "support.ticket.list", Capability: "support.ticket.list", Account: "work",
		ProfileID: "p_aaaaaaaaaaaaaaaaaaaaaaaaaa", Parameters: map[string][]string{
			"limit": {"50"}, "page": {"1"}, "status": {"all"},
		},
	})
	if err != nil || list.Data == nil || !strings.Contains(list.Human, "123\topen\tTest") {
		t.Fatalf("list = %#v, err = %v", list, err)
	}
	show, err := executor.Execute(context.Background(), cli.Operation{
		Action: "support.ticket.get", Capability: "support.ticket.show", Account: "work",
		ProfileID: "p_aaaaaaaaaaaaaaaaaaaaaaaaaa", Arguments: []string{"123"},
	})
	if err != nil || !strings.Contains(show.Human, "Ticket 123: Test (open)") {
		t.Fatalf("show = %#v, err = %v", show, err)
	}
}

func TestConfiguredExecutorDefersInputUntilCapturedMutation(t *testing.T) {
	portal := &fakePortal{}
	input := &countingInput{}
	result, err := configuredExecutor(portal).Execute(context.Background(), cli.Operation{
		Action: "support.ticket.reply", Capability: "support.ticket.reply", Account: "work",
		ProfileID: "p_aaaaaaaaaaaaaaaaaaaaaaaaaa", Arguments: []string{"123"}, Input: input,
	})
	if err != nil || result.Data == nil || input.calls != 1 {
		t.Fatalf("result = %#v, err = %v, input calls = %d", result, err, input.calls)
	}
	if len(portal.mutations) != 1 || portal.mutations[0].Action != "reply" || portal.mutations[0].ID != "123" {
		t.Fatalf("mutations = %#v", portal.mutations)
	}

	blockedInput := &countingInput{}
	_, err = configuredExecutor(portal).Execute(context.Background(), cli.Operation{
		Action: "support.ticket.reply", Capability: "support.ticket.reply", Account: "work",
		ProfileID: "p_aaaaaaaaaaaaaaaaaaaaaaaaaa", Arguments: []string{"123"}, Input: blockedInput,
		Parameters: map[string][]string{"attachments": {"diagnostic.txt"}},
	})
	if err == nil || blockedInput.calls != 0 {
		t.Fatalf("attachment err = %v, input calls = %d", err, blockedInput.calls)
	}
}

func TestConfiguredExecutorClassifiesPortalFailures(t *testing.T) {
	testCases := []struct {
		name string
		err  error
		code string
	}{
		{"expired session", &session.Error{Code: session.CodeSessionLost}, cli.CodeAuthExpired},
		{"principal drift", &PortalError{Kind: PortalUnauthorized, Code: "principal-drift"}, cli.CodeAuthExpired},
		{"contract drift", &PortalError{Kind: PortalContract, Code: "build-drift"}, cli.CodeContractDrift},
		{"ambiguous mutation", &PortalError{Kind: PortalAmbiguous}, cli.CodeOutcomeUnknown},
		{"transport", &PortalError{Kind: PortalTransport}, cli.CodeNetwork},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			portal := &fakePortal{err: testCase.err}
			_, err := configuredExecutor(portal).Execute(context.Background(), cli.Operation{
				Action: "support.ticket.close", Capability: "support.ticket.close", Account: "work",
				ProfileID: "p_aaaaaaaaaaaaaaaaaaaaaaaaaa", Arguments: []string{"123"},
			})
			var cliErr *cli.CLIError
			if !errors.As(err, &cliErr) || cliErr.Code != testCase.code {
				t.Fatalf("error = %#v, want code %q", err, testCase.code)
			}
		})
	}
}
