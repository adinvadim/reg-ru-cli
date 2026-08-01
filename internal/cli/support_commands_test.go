package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSupportMessageInputsAreDeferredAndKeptOutOfArguments(t *testing.T) {
	directory := t.TempDir()
	messagePath := filepath.Join(directory, "message.txt")
	if err := os.WriteFile(messagePath, []byte("message from file"), 0o600); err != nil {
		t.Fatal(err)
	}
	testCases := []struct {
		name  string
		args  []string
		stdin string
		want  string
	}{
		{name: "body", args: []string{"--body", "message from flag"}, want: "message from flag"},
		{name: "file", args: []string{"--file", messagePath}, want: "message from file"},
		{name: "stdin", stdin: "message from stdin", want: "message from stdin"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var received Operation
			executor := executorFunc(func(ctx context.Context, operation Operation) (Result, error) {
				received = operation
				value, err := operation.Input.Resolve(ctx)
				if err != nil {
					return Result{}, err
				}
				if string(value) != testCase.want {
					t.Errorf("input = %q, want %q", value, testCase.want)
				}
				return Result{Human: "accepted"}, nil
			})
			args := []string{"--account", "personal", "--force", "support", "ticket", "create"}
			args = append(args, testCase.args...)
			run := runCLI(t, nil, args, testCase.stdin, false, nil, executor)
			if run.exitCode != ExitOK {
				t.Fatalf("exit code = %d; stderr=%q", run.exitCode, run.stderr)
			}
			if received.Action != "support.ticket.create" || received.Capability != "support.ticket.create" {
				t.Fatalf("operation = %#v", received)
			}
			if len(received.Arguments) != 0 || len(received.Parameters) != 0 {
				t.Error("private body leaked into operation arguments or parameters")
			}
			rendered := fmt.Sprintf("%#v", received)
			for _, forbidden := range []string{testCase.want, messagePath} {
				if strings.Contains(rendered, forbidden) {
					t.Errorf("formatted operation exposed deferred input metadata")
				}
			}
		})
	}
}

func TestSupportMessageInputValidationAndDryRun(t *testing.T) {
	calls := 0
	executor := executorFunc(func(_ context.Context, _ Operation) (Result, error) {
		calls++
		return Result{Human: "unexpected"}, nil
	})
	invalid := [][]string{
		{"--account", "personal", "--force", "support", "ticket", "create", "--body", "one", "--file", "two"},
		{"--account", "personal", "--force", "support", "ticket", "create", "--body", ""},
		{"--account", "personal", "support", "ticket", "list", "--limit", "0"},
		{"--account", "personal", "support", "ticket", "list", "--page", "0"},
		{"--account", "personal", "support", "ticket", "list", "--status", "pending"},
	}
	for _, args := range invalid {
		run := runCLI(t, nil, args, "", false, nil, executor)
		if run.exitCode != ExitUsage {
			t.Errorf("args %v exit code = %d; stderr=%q", args, run.exitCode, run.stderr)
		}
	}
	run := runCLI(t, nil, []string{
		"--account", "personal", "--force", "support", "ticket", "create",
	}, "", true, nil, executor)
	if run.exitCode != ExitUsage {
		t.Errorf("TTY without body exit code = %d; stderr=%q", run.exitCode, run.stderr)
	}

	run = runCLI(t, nil, []string{
		"--account", "personal", "--dry-run", "support", "ticket", "create",
		"--file", filepath.Join(t.TempDir(), "missing.txt"),
	}, "", false, nil, executor)
	if run.exitCode != ExitOK {
		t.Errorf("dry-run exit code = %d; stderr=%q", run.exitCode, run.stderr)
	}
	if calls != 0 {
		t.Fatalf("invalid or dry-run support command reached executor %d time(s)", calls)
	}
}

func TestSupportListAndReplyBuildTypedOperations(t *testing.T) {
	var operations []Operation
	executor := executorFunc(func(_ context.Context, operation Operation) (Result, error) {
		operations = append(operations, operation)
		return Result{Human: "ok"}, nil
	})
	list := runCLI(t, nil, []string{
		"--account", "personal", "support", "ticket", "list",
		"--limit", "25", "--page", "2", "--status", "closed",
	}, "", false, nil, executor)
	if list.exitCode != ExitOK {
		t.Fatalf("list exit code = %d; stderr=%q", list.exitCode, list.stderr)
	}
	reply := runCLI(t, nil, []string{
		"--account", "personal", "--force", "support", "ticket", "reply", "ticket-number",
		"--body", "reply body", "--attachment", "diagnostic.txt",
	}, "", false, nil, executor)
	if reply.exitCode != ExitOK {
		t.Fatalf("reply exit code = %d; stderr=%q", reply.exitCode, reply.stderr)
	}
	if len(operations) != 2 {
		t.Fatalf("operations = %#v", operations)
	}
	if got := operations[0].Parameters; got["limit"][0] != "25" || got["page"][0] != "2" || got["status"][0] != "closed" {
		t.Errorf("list parameters = %#v", got)
	}
	if operations[1].Arguments[0] != "ticket-number" || operations[1].Parameters["attachments"][0] != "diagnostic.txt" {
		t.Errorf("reply operation = %#v", operations[1])
	}
}
