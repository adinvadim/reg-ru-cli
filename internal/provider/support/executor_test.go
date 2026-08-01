package support

import (
	"context"
	"errors"
	"testing"

	"github.com/adinvadim/reg-ru-cli/internal/cli"
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
