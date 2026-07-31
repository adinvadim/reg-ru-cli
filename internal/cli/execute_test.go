package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type executorFunc func(context.Context, Operation) (Result, error)

func (f executorFunc) Execute(ctx context.Context, operation Operation) (Result, error) {
	return f(ctx, operation)
}

type testRun struct {
	exitCode int
	stdout   string
	stderr   string
}

func runCLI(
	t *testing.T,
	ctx context.Context,
	args []string,
	input string,
	inputTTY bool,
	env map[string]string,
	executor Executor,
) testRun {
	t.Helper()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if ctx == nil {
		ctx = context.Background()
	}
	if env == nil {
		env = map[string]string{}
	}
	if executor == nil {
		executor = UnavailableExecutor{}
	}

	exitCode := Execute(ctx, args, Options{
		In:     strings.NewReader(input),
		Out:    &stdout,
		ErrOut: &stderr,
		Getenv: func(name string) string {
			return env[name]
		},
		InputIsTTY:  func() bool { return inputTTY },
		OutputIsTTY: func() bool { return false },
		ErrorIsTTY:  func() bool { return false },
		Executor:    executor,
	})

	return testRun{
		exitCode: exitCode,
		stdout:   stdout.String(),
		stderr:   stderr.String(),
	}
}

func TestRootHelpAndVersion(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		run := runCLI(t, nil, []string{"--help"}, "", false, nil, nil)
		if run.exitCode != ExitOK {
			t.Fatalf("exit code = %d, want %d; stderr=%q", run.exitCode, ExitOK, run.stderr)
		}
		for _, expected := range []string{"Usage:", "regru", "auth", "vps", "s3", "billing", "support"} {
			if !strings.Contains(run.stdout, expected) {
				t.Errorf("stdout does not contain %q:\n%s", expected, run.stdout)
			}
		}
		if run.stderr != "" {
			t.Errorf("stderr = %q, want empty", run.stderr)
		}
		if strings.Contains(run.stdout, "-v, --version") {
			t.Error("-v must remain available for future verbosity, not version")
		}
	})

	t.Run("version", func(t *testing.T) {
		run := runCLI(t, nil, []string{"--version"}, "", false, nil, nil)
		if run.exitCode != ExitOK {
			t.Fatalf("exit code = %d, want %d; stderr=%q", run.exitCode, ExitOK, run.stderr)
		}
		if run.stdout != "regru dev\n" {
			t.Errorf("stdout = %q, want %q", run.stdout, "regru dev\n")
		}
		if run.stderr != "" {
			t.Errorf("stderr = %q, want empty", run.stderr)
		}
	})
}

func TestJSONSuccessEnvelope(t *testing.T) {
	executor := executorFunc(func(_ context.Context, operation Operation) (Result, error) {
		return Result{
			Human: "Authenticated as " + operation.Account,
			Plain: []string{"account=" + operation.Account, "state=authenticated"},
			Data: map[string]any{
				"account": operation.Account,
				"state":   "authenticated",
			},
		}, nil
	})
	run := runCLI(
		t,
		nil,
		[]string{"--json", "--account", "personal", "auth", "status"},
		"",
		false,
		nil,
		executor,
	)
	if run.exitCode != ExitOK {
		t.Fatalf("exit code = %d, want %d; stderr=%q", run.exitCode, ExitOK, run.stderr)
	}
	if run.stderr != "" {
		t.Fatalf("stderr = %q, want empty", run.stderr)
	}

	var envelope struct {
		SchemaVersion string `json:"schemaVersion"`
		OK            bool   `json:"ok"`
		Command       string `json:"command"`
		Data          struct {
			Account string `json:"account"`
			State   string `json:"state"`
		} `json:"data"`
		Warnings []Warning `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(run.stdout), &envelope); err != nil {
		t.Fatalf("decode stdout: %v\n%s", err, run.stdout)
	}
	if envelope.SchemaVersion != schemaVersion || !envelope.OK {
		t.Errorf("unexpected envelope: %+v", envelope)
	}
	if envelope.Command != "auth status" {
		t.Errorf("command = %q, want auth status", envelope.Command)
	}
	if envelope.Warnings == nil {
		t.Error("warnings must encode as an empty array, not null")
	}
	if envelope.Data.Account != "personal" || envelope.Data.State != "authenticated" {
		t.Errorf("unexpected data: %+v", envelope.Data)
	}
}

func TestPlainOutputIsStableAndLineOriented(t *testing.T) {
	executor := executorFunc(func(_ context.Context, operation Operation) (Result, error) {
		return Result{
			Human: "Authenticated",
			Plain: []string{operation.Account + "\tauthenticated"},
			Data:  map[string]string{"state": "authenticated"},
		}, nil
	})
	run := runCLI(
		t,
		nil,
		[]string{"--plain", "--account", "work", "auth", "status"},
		"",
		false,
		nil,
		executor,
	)
	if run.exitCode != ExitOK {
		t.Fatalf("exit code = %d, want %d; stderr=%q", run.exitCode, ExitOK, run.stderr)
	}
	if run.stdout != "work\tauthenticated\n" {
		t.Errorf("stdout = %q", run.stdout)
	}
}

func TestUnavailableCapabilityUsesStructuredErrorAndExitSeven(t *testing.T) {
	run := runCLI(
		t,
		nil,
		[]string{"--json", "--account", "personal", "vps", "list"},
		"",
		false,
		nil,
		nil,
	)
	if run.exitCode != ExitCapability {
		t.Fatalf("exit code = %d, want %d; stderr=%q", run.exitCode, ExitCapability, run.stderr)
	}
	if run.stdout != "" {
		t.Errorf("stdout = %q, want empty", run.stdout)
	}

	var envelope struct {
		SchemaVersion string `json:"schemaVersion"`
		OK            bool   `json:"ok"`
		Command       string `json:"command"`
		Error         struct {
			Code      string         `json:"code"`
			ExitCode  int            `json:"exitCode"`
			Retryable bool           `json:"retryable"`
			Details   map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(run.stderr), &envelope); err != nil {
		t.Fatalf("decode stderr: %v\n%s", err, run.stderr)
	}
	if envelope.SchemaVersion != schemaVersion || envelope.OK {
		t.Errorf("unexpected envelope: %+v", envelope)
	}
	if envelope.Error.Code != CodeCapability {
		t.Errorf("error code = %q, want %q", envelope.Error.Code, CodeCapability)
	}
	if envelope.Command != "vps list" {
		t.Errorf("command = %q, want vps list", envelope.Command)
	}
	if envelope.Error.ExitCode != ExitCapability {
		t.Errorf("exit code field = %d, want %d", envelope.Error.ExitCode, ExitCapability)
	}
	if envelope.Error.Details["capability"] != "cloudvps.instances" {
		t.Errorf("capability = %#v", envelope.Error.Details["capability"])
	}
}

func TestOutputModesAreMutuallyExclusive(t *testing.T) {
	run := runCLI(
		t,
		nil,
		[]string{"--json", "--plain", "--account", "personal", "auth", "status"},
		"",
		false,
		nil,
		nil,
	)
	if run.exitCode != ExitUsage {
		t.Fatalf("exit code = %d, want %d; stderr=%q", run.exitCode, ExitUsage, run.stderr)
	}
	if !strings.Contains(run.stderr, CodeInvalidUsage) {
		t.Errorf("stderr does not contain %q: %s", CodeInvalidUsage, run.stderr)
	}
}

func TestVersionHasNoShortVFlag(t *testing.T) {
	run := runCLI(t, nil, []string{"-v"}, "", false, nil, nil)
	if run.exitCode != ExitUsage {
		t.Fatalf("exit code = %d, want %d; stderr=%q", run.exitCode, ExitUsage, run.stderr)
	}
	if !strings.Contains(run.stderr, "unknown shorthand flag") {
		t.Errorf("stderr does not explain reserved -v: %s", run.stderr)
	}
}

func TestAccountSelectionIsDeterministic(t *testing.T) {
	var received Operation
	executor := executorFunc(func(_ context.Context, operation Operation) (Result, error) {
		received = operation
		return Result{Human: "ok", Plain: []string{"ok"}, Data: map[string]bool{"ok": true}}, nil
	})

	t.Run("environment fallback", func(t *testing.T) {
		received = Operation{}
		run := runCLI(
			t,
			nil,
			[]string{"auth", "status"},
			"",
			false,
			map[string]string{"REGRU_ACCOUNT": "from-env"},
			executor,
		)
		if run.exitCode != ExitOK {
			t.Fatalf("exit code = %d; stderr=%q", run.exitCode, run.stderr)
		}
		if received.Account != "from-env" {
			t.Errorf("account = %q, want from-env", received.Account)
		}
	})

	t.Run("flag wins", func(t *testing.T) {
		received = Operation{}
		run := runCLI(
			t,
			nil,
			[]string{"--account", "from-flag", "auth", "status"},
			"",
			false,
			map[string]string{"REGRU_ACCOUNT": "from-env"},
			executor,
		)
		if run.exitCode != ExitOK {
			t.Fatalf("exit code = %d; stderr=%q", run.exitCode, run.stderr)
		}
		if received.Account != "from-flag" {
			t.Errorf("account = %q, want from-flag", received.Account)
		}
	})

	t.Run("missing account never prompts", func(t *testing.T) {
		run := runCLI(t, nil, []string{"auth", "status"}, "", true, nil, executor)
		if run.exitCode != ExitConfiguration {
			t.Fatalf("exit code = %d, want %d; stderr=%q", run.exitCode, ExitConfiguration, run.stderr)
		}
		if !strings.Contains(run.stderr, CodeAccountRequired) {
			t.Errorf("stderr does not contain %q: %s", CodeAccountRequired, run.stderr)
		}
	})
}

func TestInteractiveAuthenticationContract(t *testing.T) {
	t.Run("non tty login fails before executor", func(t *testing.T) {
		calls := 0
		executor := executorFunc(func(_ context.Context, _ Operation) (Result, error) {
			calls++
			return Result{}, nil
		})
		run := runCLI(
			t,
			nil,
			[]string{"--account", "personal", "auth", "login"},
			"",
			false,
			nil,
			executor,
		)
		if run.exitCode != ExitInteractionRequired {
			t.Fatalf("exit code = %d, want %d; stderr=%q", run.exitCode, ExitInteractionRequired, run.stderr)
		}
		if calls != 0 {
			t.Errorf("executor calls = %d, want 0", calls)
		}
		if !strings.Contains(run.stderr, CodeInteractiveRequired) {
			t.Errorf("stderr does not contain %q: %s", CodeInteractiveRequired, run.stderr)
		}
	})

	t.Run("no input overrides tty", func(t *testing.T) {
		run := runCLI(
			t,
			nil,
			[]string{"--account", "personal", "--no-input", "auth", "login"},
			"",
			true,
			nil,
			nil,
		)
		if run.exitCode != ExitInteractionRequired {
			t.Fatalf("exit code = %d, want %d; stderr=%q", run.exitCode, ExitInteractionRequired, run.stderr)
		}
	})

	t.Run("login timeout is bounded", func(t *testing.T) {
		run := runCLI(
			t,
			nil,
			[]string{
				"--account", "personal",
				"auth", "login",
				"--login-timeout", "30s",
			},
			"",
			true,
			nil,
			nil,
		)
		if run.exitCode != ExitUsage {
			t.Fatalf("exit code = %d, want %d; stderr=%q", run.exitCode, ExitUsage, run.stderr)
		}
		if !strings.Contains(run.stderr, "--login-timeout must be between") {
			t.Errorf("unexpected stderr: %s", run.stderr)
		}
	})
}

func TestMutationConfirmation(t *testing.T) {
	t.Run("non tty requires force", func(t *testing.T) {
		run := runCLI(
			t,
			nil,
			[]string{"--account", "personal", "auth", "logout"},
			"",
			false,
			nil,
			nil,
		)
		if run.exitCode != ExitInteractionRequired {
			t.Fatalf("exit code = %d, want %d; stderr=%q", run.exitCode, ExitInteractionRequired, run.stderr)
		}
		if !strings.Contains(run.stderr, CodeConfirmationRequired) {
			t.Errorf("stderr does not contain %q: %s", CodeConfirmationRequired, run.stderr)
		}
	})

	t.Run("tty confirmation proceeds", func(t *testing.T) {
		calls := 0
		executor := executorFunc(func(_ context.Context, _ Operation) (Result, error) {
			calls++
			return Result{Human: "Logged out", Plain: []string{"state=logged_out"}, Data: nil}, nil
		})
		run := runCLI(
			t,
			nil,
			[]string{"--account", "personal", "auth", "logout"},
			"yes\n",
			true,
			nil,
			executor,
		)
		if run.exitCode != ExitOK {
			t.Fatalf("exit code = %d; stderr=%q", run.exitCode, run.stderr)
		}
		if calls != 1 {
			t.Errorf("executor calls = %d, want 1", calls)
		}
		if !strings.Contains(run.stderr, "Confirm auth.logout?") {
			t.Errorf("stderr does not contain prompt: %q", run.stderr)
		}
	})

	t.Run("force proceeds without prompt", func(t *testing.T) {
		calls := 0
		executor := executorFunc(func(_ context.Context, _ Operation) (Result, error) {
			calls++
			return Result{Human: "Logged out"}, nil
		})
		run := runCLI(
			t,
			nil,
			[]string{"--account", "personal", "--force", "auth", "logout"},
			"",
			false,
			nil,
			executor,
		)
		if run.exitCode != ExitOK {
			t.Fatalf("exit code = %d; stderr=%q", run.exitCode, run.stderr)
		}
		if calls != 1 {
			t.Errorf("executor calls = %d, want 1", calls)
		}
		if run.stderr != "" {
			t.Errorf("stderr = %q, want no prompt", run.stderr)
		}
	})
}

func TestDryRunProvesZeroExecutorCallsAndRedactsArguments(t *testing.T) {
	calls := 0
	executor := executorFunc(func(_ context.Context, _ Operation) (Result, error) {
		calls++
		return Result{}, errors.New("executor must not be called")
	})
	const sensitiveLookingPath = "/private/customer-case.txt"
	run := runCLI(
		t,
		nil,
		[]string{
			"--json",
			"--account", "personal",
			"--dry-run",
			"support", "ticket", "attach", "ticket-123", sensitiveLookingPath,
		},
		"",
		false,
		nil,
		executor,
	)
	if run.exitCode != ExitOK {
		t.Fatalf("exit code = %d, want %d; stderr=%q", run.exitCode, ExitOK, run.stderr)
	}
	if calls != 0 {
		t.Errorf("executor calls = %d, want 0", calls)
	}
	if strings.Contains(run.stdout, sensitiveLookingPath) || strings.Contains(run.stdout, "ticket-123") {
		t.Errorf("dry-run output leaked opaque arguments: %s", run.stdout)
	}
	if !strings.Contains(run.stdout, `"dry_run":true`) ||
		!strings.Contains(run.stdout, `"argument_count":2`) {
		t.Errorf("unexpected dry-run output: %s", run.stdout)
	}
}

func TestCancellationAndTimeoutAreDeterministic(t *testing.T) {
	waitForContext := executorFunc(func(ctx context.Context, _ Operation) (Result, error) {
		<-ctx.Done()
		return Result{}, ctx.Err()
	})

	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		run := runCLI(
			t,
			ctx,
			[]string{"--account", "personal", "auth", "status"},
			"",
			false,
			nil,
			waitForContext,
		)
		if run.exitCode != ExitInterrupted {
			t.Fatalf("exit code = %d, want %d; stderr=%q", run.exitCode, ExitInterrupted, run.stderr)
		}
		if !strings.Contains(run.stderr, CodeInterrupted) {
			t.Errorf("stderr does not contain %q: %s", CodeInterrupted, run.stderr)
		}
	})

	t.Run("timed out", func(t *testing.T) {
		run := runCLI(
			t,
			nil,
			[]string{"--account", "personal", "--timeout", time.Millisecond.String(), "auth", "status"},
			"",
			false,
			nil,
			waitForContext,
		)
		if run.exitCode != ExitTimeout {
			t.Fatalf("exit code = %d, want %d; stderr=%q", run.exitCode, ExitTimeout, run.stderr)
		}
		if !strings.Contains(run.stderr, CodeTimeout) {
			t.Errorf("stderr does not contain %q: %s", CodeTimeout, run.stderr)
		}
	})
}

func TestCompletionGeneration(t *testing.T) {
	run := runCLI(t, nil, []string{"completion", "zsh"}, "", false, nil, nil)
	if run.exitCode != ExitOK {
		t.Fatalf("exit code = %d, want %d; stderr=%q", run.exitCode, ExitOK, run.stderr)
	}
	if !strings.Contains(run.stdout, "#compdef regru") {
		t.Errorf("stdout does not look like zsh completion: %.200s", run.stdout)
	}
	if run.stderr != "" {
		t.Errorf("stderr = %q, want empty", run.stderr)
	}
}

func TestCompletionRejectsEnvelopeModes(t *testing.T) {
	for _, mode := range []string{"--json", "--plain"} {
		t.Run(mode, func(t *testing.T) {
			run := runCLI(t, nil, []string{mode, "completion", "bash"}, "", false, nil, nil)
			if run.exitCode != ExitUsage {
				t.Fatalf("exit code = %d, want %d; stderr=%q", run.exitCode, ExitUsage, run.stderr)
			}
			if run.stdout != "" {
				t.Errorf("stdout = %q, want empty", run.stdout)
			}
		})
	}
}

func TestPlainFieldEscaping(t *testing.T) {
	got := escapePlainField("one\\two\tthree\nfour\rfive")
	want := `one\\two\tthree\nfour\rfive`
	if got != want {
		t.Errorf("escapePlainField() = %q, want %q", got, want)
	}
}

func TestErrorColorNeverContaminatesMachineModes(t *testing.T) {
	cliErr := UsageError("bad input")

	var human bytes.Buffer
	if err := writeError(&human, outputHuman, "", cliErr, true); err != nil {
		t.Fatalf("write human error: %v", err)
	}
	if !strings.Contains(human.String(), "\x1b[31m") {
		t.Errorf("TTY human error should contain color: %q", human.String())
	}

	var plain bytes.Buffer
	if err := writeError(&plain, outputPlain, "", cliErr, true); err != nil {
		t.Fatalf("write plain error: %v", err)
	}
	if strings.Contains(plain.String(), "\x1b") {
		t.Errorf("plain error contains ANSI: %q", plain.String())
	}

	var jsonOutput bytes.Buffer
	if err := writeError(&jsonOutput, outputJSON, "", cliErr, true); err != nil {
		t.Fatalf("write JSON error: %v", err)
	}
	if strings.Contains(jsonOutput.String(), "\x1b") {
		t.Errorf("JSON error contains ANSI: %q", jsonOutput.String())
	}
}

func TestErrorFactoriesExposeStableCodesWithoutCauses(t *testing.T) {
	tests := []struct {
		name     string
		err      *CLIError
		code     string
		exitCode int
	}{
		{"missing browser", MissingBrowser(), CodeMissingBrowser, ExitAuthentication},
		{"login cancelled", LoginCancelled(), CodeLoginCancelled, ExitAuthentication},
		{"auth expired", AuthenticationExpired(), CodeAuthExpired, ExitAuthentication},
		{"account mismatch", AccountMismatch("expected", "actual"), CodeAccountMismatch, ExitAuthentication},
		{"contract drift", PrivateContractDrift("support.private"), CodeContractDrift, ExitContractDrift},
		{"outcome unknown", OutcomeUnknown("support.private"), CodeOutcomeUnknown, ExitOutcomeUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.err.Code != test.code || test.err.ExitCode != test.exitCode {
				t.Errorf("error = %+v, want code=%s exit=%d", test.err, test.code, test.exitCode)
			}
			if test.err.Cause != nil {
				t.Errorf("public error unexpectedly exposes a cause: %v", test.err.Cause)
			}
		})
	}
}
