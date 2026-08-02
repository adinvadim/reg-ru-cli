package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adinvadim/reg-ru-cli/internal/profile"
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
	return runCLIWithProfiles(
		t,
		ctx,
		args,
		input,
		inputTTY,
		env,
		executor,
		testProfiles(),
	)
}

func runCLIWithProfiles(
	t *testing.T,
	ctx context.Context,
	args []string,
	input string,
	inputTTY bool,
	env map[string]string,
	executor Executor,
	profiles profile.Repository,
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
		Profiles:    profiles,
	})

	return testRun{
		exitCode: exitCode,
		stdout:   stdout.String(),
		stderr:   stderr.String(),
	}
}

func testProfiles() profile.Repository {
	accounts := map[string]profile.Account{}
	for index, name := range []string{
		"personal",
		"work",
		"from-env",
		"from-flag",
	} {
		idCharacter := string(rune('a' + index))
		accounts[name] = profile.Account{
			ID:       "p_" + strings.Repeat(idCharacter, 26),
			Provider: "reg.ru",
		}
	}
	return profile.NewMemoryRepository(profile.Config{
		SchemaVersion: profile.SchemaVersion,
		Accounts:      accounts,
	})
}

func TestRootHelpAndVersion(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		run := runCLI(t, nil, []string{"--help"}, "", false, nil, nil)
		if run.exitCode != ExitOK {
			t.Fatalf("exit code = %d, want %d; stderr=%q", run.exitCode, ExitOK, run.stderr)
		}
		for _, expected := range []string{
			"Usage:",
			"regru",
			"auth",
			"vps",
			"s3",
			"billing",
			"support",
			"Commands remain visible when a provider capability is unavailable",
			"regru capability probe",
		} {
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

func TestAccountCommandsManageOnlyNonSecretProfileMetadata(t *testing.T) {
	profiles := profile.NewMemoryRepository(profile.Config{
		SchemaVersion: profile.SchemaVersion,
		Accounts:      map[string]profile.Account{},
	})

	added := runCLIWithProfiles(
		t,
		nil,
		[]string{"--json", "account", "add", "work", "--label", "Work"},
		"",
		false,
		nil,
		nil,
		profiles,
	)
	if added.exitCode != ExitOK {
		t.Fatalf("add exit code = %d; stderr=%q", added.exitCode, added.stderr)
	}
	for _, forbidden := range []string{"password", "secret", "session_ref", "_ref"} {
		if strings.Contains(strings.ToLower(added.stdout), forbidden) {
			t.Errorf("add output contains %q: %s", forbidden, added.stdout)
		}
	}

	selected := runCLIWithProfiles(
		t,
		nil,
		[]string{"--plain", "account", "use", "work"},
		"",
		false,
		nil,
		nil,
		profiles,
	)
	if selected.exitCode != ExitOK || selected.stdout != "work\tdefault\n" {
		t.Fatalf("use result = %+v", selected)
	}

	shown := runCLIWithProfiles(
		t,
		nil,
		[]string{"--json", "account", "show"},
		"",
		false,
		nil,
		nil,
		profiles,
	)
	if shown.exitCode != ExitOK {
		t.Fatalf("show exit code = %d; stderr=%q", shown.exitCode, shown.stderr)
	}
	if !strings.Contains(shown.stdout, `"account":"work"`) ||
		!strings.Contains(shown.stdout, `"provider":"reg.ru"`) {
		t.Errorf("unexpected show output: %s", shown.stdout)
	}
}

func TestAccountShowReportsCredentialProcessWithoutExposingCommand(t *testing.T) {
	const helperPath = "/private/path/credential-helper"
	run := runCLIWithProfiles(
		t,
		nil,
		[]string{"--account", "personal", "--json", "account", "show"},
		"",
		false,
		nil,
		nil,
		profilesWithCredentialProcess([]string{helperPath, "get", "personal"}),
	)
	if run.exitCode != ExitOK {
		t.Fatalf("exit code = %d; stderr=%q", run.exitCode, run.stderr)
	}
	if !strings.Contains(run.stdout, `"credentialProcess":true`) {
		t.Errorf("credential process state is missing: %s", run.stdout)
	}
	if strings.Contains(run.stdout, helperPath) {
		t.Errorf("credential process command was exposed: %s", run.stdout)
	}
}

func TestCredentialProcessIsLazyAndCannotReachOutput(t *testing.T) {
	const sentinel = "synthetic-process-password"
	executor := executorFunc(func(ctx context.Context, operation Operation) (Result, error) {
		if operation.Credentials == nil {
			t.Fatal("executor did not receive credential resolver")
		}
		value, err := operation.Credentials.Resolve(ctx, "regapi.password")
		if err != nil || string(value) != sentinel {
			t.Fatal("executor did not resolve expected synthetic credential")
		}
		return Result{
			Human: "provider returned " + string(value),
			Plain: []string{string(value)},
			Data:  map[string]string{"value": string(value)},
		}, nil
	})
	run := runCLIWithProfiles(
		t,
		nil,
		[]string{
			"--account", "personal",
			"auth", "status",
		},
		"",
		false,
		nil,
		executor,
		profilesWithCredentialProcess(cliHelperCommand("regapi")),
	)
	if run.exitCode != ExitGeneral {
		t.Fatalf("exit code = %d, want %d; stderr=%q", run.exitCode, ExitGeneral, run.stderr)
	}
	if run.stdout != "" {
		t.Errorf("stdout = %q, want empty", run.stdout)
	}
	if strings.Contains(run.stderr, sentinel) {
		t.Errorf("stderr leaked credential: %q", run.stderr)
	}
	if !strings.Contains(run.stderr, CodeSensitiveOutput) {
		t.Errorf("stderr does not contain %q: %s", CodeSensitiveOutput, run.stderr)
	}
}

func TestCredentialProcessIsNotRunForDryRunOrUnusedPlaceholder(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "credential-process-ran")
	profiles := profilesWithCredentialProcess(
		cliHelperCommand("mark", marker),
	)

	run := runCLIWithProfiles(
		t,
		nil,
		[]string{
			"--account", "personal",
			"--dry-run",
			"vps", "delete", "42",
		},
		"",
		false,
		nil,
		nil,
		profiles,
	)
	if run.exitCode != ExitOK {
		t.Fatalf("dry-run exit code = %d; stderr=%q", run.exitCode, run.stderr)
	}

	run = runCLIWithProfiles(
		t,
		nil,
		[]string{"--account", "personal", "vps", "list"},
		"",
		false,
		nil,
		nil,
		profiles,
	)
	if run.exitCode != ExitCapability {
		t.Fatalf("placeholder exit code = %d; stderr=%q", run.exitCode, run.stderr)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("credential process ran without a credential request")
	}
}

func TestCredentialMaterialInAdapterErrorIsBlocked(t *testing.T) {
	const sentinel = "synthetic-process-token"
	executor := executorFunc(func(ctx context.Context, operation Operation) (Result, error) {
		value, err := operation.Credentials.Resolve(ctx, "cloudvps.token")
		if err != nil {
			t.Fatal("executor did not receive credential")
		}
		return Result{}, &CLIError{
			Code:     CodeNetwork,
			Message:  "provider rejected request",
			ExitCode: ExitNetwork,
			Details: map[string]any{
				"response": map[string]any{"echo": string(value)},
			},
		}
	})
	run := runCLIWithProfiles(
		t,
		nil,
		[]string{
			"--account", "personal",
			"vps", "list",
		},
		"",
		false,
		nil,
		executor,
		profilesWithCredentialProcess(cliHelperCommand("cloudvps")),
	)
	if run.exitCode != ExitGeneral {
		t.Fatalf("exit code = %d; stderr=%q", run.exitCode, run.stderr)
	}
	if strings.Contains(run.stderr, sentinel) ||
		!strings.Contains(run.stderr, CodeSensitiveOutput) {
		t.Errorf("adapter error was not safely blocked: %q", run.stderr)
	}
}

func TestCredentialProcessFailureUsesStableRedactedError(t *testing.T) {
	executor := executorFunc(func(ctx context.Context, operation Operation) (Result, error) {
		_, err := operation.Credentials.Resolve(ctx, "cloudvps.token")
		return Result{}, err
	})
	run := runCLIWithProfiles(
		t,
		nil,
		[]string{"--account", "personal", "vps", "list"},
		"",
		false,
		nil,
		executor,
		profilesWithCredentialProcess(cliHelperCommand("fail")),
	)
	if run.exitCode != ExitConfiguration {
		t.Fatalf("exit code = %d; stderr=%q", run.exitCode, run.stderr)
	}
	if !strings.Contains(run.stderr, CodeCredentialProcess) ||
		strings.Contains(run.stderr, "synthetic-helper-stderr-secret") {
		t.Errorf("credential process error is not stable and redacted: %q", run.stderr)
	}
}

func TestCredentialsStdinFlagDoesNotExist(t *testing.T) {
	run := runCLI(
		t,
		nil,
		[]string{
			"--credentials-stdin",
			"--account", "personal",
			"auth", "status",
		},
		"",
		false,
		nil,
		nil,
	)
	if run.exitCode != ExitUsage {
		t.Fatalf("exit code = %d, want usage; stderr=%q", run.exitCode, run.stderr)
	}
	if !strings.Contains(run.stderr, "unknown flag") {
		t.Errorf("removed flag was not rejected clearly: %q", run.stderr)
	}
}

func profilesWithCredentialProcess(command []string) profile.Repository {
	return profile.NewMemoryRepository(profile.Config{
		SchemaVersion: profile.SchemaVersion,
		Accounts: map[string]profile.Account{
			"personal": {
				ID:       "p_" + strings.Repeat("a", 26),
				Provider: "reg.ru",
				CredentialProcess: profile.CredentialProcess{
					Command: command,
				},
			},
		},
	})
}

func cliHelperCommand(mode string, arguments ...string) []string {
	command := []string{
		os.Args[0],
		"-test.run=^TestCLICredentialProcessHelper$",
		"--",
		mode,
	}
	return append(command, arguments...)
}

func TestCLICredentialProcessHelper(t *testing.T) {
	arguments := []string{}
	for index, argument := range os.Args {
		if argument == "--" {
			arguments = os.Args[index+1:]
			break
		}
	}
	if len(arguments) == 0 {
		return
	}
	switch arguments[0] {
	case "regapi":
		fmt.Fprint(os.Stdout, `{
			"schemaVersion":"regru.credential-process/v1",
			"fields":{
				"regapi.username":"synthetic-process-user",
				"regapi.password":"synthetic-process-password"
			}
		}`)
	case "cloudvps":
		fmt.Fprint(os.Stdout, `{
			"schemaVersion":"regru.credential-process/v1",
			"fields":{"cloudvps.token":"synthetic-process-token"}
		}`)
	case "mark":
		if len(arguments) == 2 {
			if err := os.WriteFile(arguments[1], []byte("ran"), 0o600); err != nil {
				os.Exit(21)
			}
		}
		fmt.Fprint(os.Stdout, `{
			"schemaVersion":"regru.credential-process/v1",
			"fields":{"cloudvps.token":"synthetic-process-token"}
		}`)
	case "fail":
		fmt.Fprint(os.Stderr, "synthetic-helper-stderr-secret")
		os.Exit(23)
	default:
		os.Exit(22)
	}
	os.Exit(0)
}

func TestSelectionPrecedenceIncludesProjectAndUserDefaults(t *testing.T) {
	profiles := profile.NewMemoryRepository(profile.Config{
		SchemaVersion:  profile.SchemaVersion,
		DefaultAccount: "personal",
		ProjectAccount: "work",
		Accounts: map[string]profile.Account{
			"personal": {ID: "p_" + strings.Repeat("a", 26), Provider: "reg.ru"},
			"work":     {ID: "p_" + strings.Repeat("b", 26), Provider: "reg.ru"},
		},
	})
	var selected string
	executor := executorFunc(func(_ context.Context, operation Operation) (Result, error) {
		selected = operation.Account
		return Result{Human: "ok"}, nil
	})
	run := runCLIWithProfiles(
		t,
		nil,
		[]string{"auth", "status"},
		"",
		false,
		nil,
		executor,
		profiles,
	)
	if run.exitCode != ExitOK || selected != "work" {
		t.Fatalf("project selection = %q, run=%+v", selected, run)
	}

	run = runCLIWithProfiles(
		t,
		nil,
		[]string{"--account=", "auth", "status"},
		"",
		false,
		nil,
		executor,
		profiles,
	)
	if run.exitCode != ExitUsage {
		t.Fatalf("explicit empty account exit = %d; stderr=%q", run.exitCode, run.stderr)
	}
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

func TestCloudVPSCommandTreeExposesTicketSurface(t *testing.T) {
	run := runCLI(t, nil, []string{"vps", "--help"}, "", false, nil, nil)
	if run.exitCode != ExitOK {
		t.Fatalf("exit code = %d; stderr=%q", run.exitCode, run.stderr)
	}
	for _, command := range []string{
		"action",
		"backup",
		"clone",
		"create",
		"delete",
		"image",
		"ip",
		"password-reset",
		"plan",
		"rebuild",
		"rename",
		"resize",
		"snapshot",
		"ssh-key",
		"start",
		"stop",
	} {
		if !strings.Contains(run.stdout, command) {
			t.Errorf("vps help does not expose %q:\n%s", command, run.stdout)
		}
	}
	if !strings.Contains(run.stdout, "--no-wait") {
		t.Errorf("vps help does not expose --no-wait:\n%s", run.stdout)
	}
}

func TestCloudVPSBackupStatusIsReadOnlyAndVisible(t *testing.T) {
	help := runCLI(t, nil, []string{"vps", "backup", "--help"}, "", false, nil, nil)
	if help.exitCode != ExitOK || !strings.Contains(help.stdout, "status") {
		t.Fatalf("backup help exit code = %d; stdout=%q stderr=%q", help.exitCode, help.stdout, help.stderr)
	}

	var received Operation
	executor := executorFunc(func(_ context.Context, operation Operation) (Result, error) {
		received = operation
		return Result{Human: "status"}, nil
	})
	run := runCLI(
		t,
		nil,
		[]string{"--account", "personal", "vps", "backup", "status", "42"},
		"",
		false,
		nil,
		executor,
	)
	if run.exitCode != ExitOK {
		t.Fatalf("exit code = %d; stderr=%q", run.exitCode, run.stderr)
	}
	if received.Action != "vps.backup.status" || len(received.Arguments) != 1 || received.Arguments[0] != "42" {
		t.Fatalf("operation = %+v", received)
	}
	if run.stderr != "" {
		t.Errorf("read-only status prompted unexpectedly: %q", run.stderr)
	}
}

func TestCloudVPSBackupDisableConfirmationExplainsRetention(t *testing.T) {
	calls := 0
	executor := executorFunc(func(_ context.Context, _ Operation) (Result, error) {
		calls++
		return Result{Human: "disabled"}, nil
	})
	run := runCLI(
		t,
		nil,
		[]string{"--account", "personal", "vps", "backup", "disable", "42"},
		"n\n",
		true,
		nil,
		executor,
	)
	if run.exitCode != ExitInteractionRequired || calls != 0 {
		t.Fatalf("exit code = %d, calls = %d; stderr=%q", run.exitCode, calls, run.stderr)
	}
	if !strings.Contains(run.stderr, "three calendar days") ||
		!strings.Contains(run.stderr, "Confirm vps.backup.disable?") {
		t.Errorf("confirmation did not explain retention: %q", run.stderr)
	}
}

func TestBillingCommandTreeExposesDocumentedAndGatedSurface(t *testing.T) {
	run := runCLI(t, nil, []string{"billing", "--help"}, "", false, nil, nil)
	if run.exitCode != ExitOK {
		t.Fatalf("exit code = %d; stderr=%q", run.exitCode, run.stderr)
	}
	for _, command := range []string{"balance", "history", "invoice"} {
		if !strings.Contains(run.stdout, command) {
			t.Errorf("billing help does not expose %q:\n%s", command, run.stdout)
		}
	}

	run = runCLI(t, nil, []string{"billing", "invoice", "--help"}, "", false, nil, nil)
	if run.exitCode != ExitOK {
		t.Fatalf("invoice help exit code = %d; stderr=%q", run.exitCode, run.stderr)
	}
	for _, command := range []string{
		"create", "delete", "list", "payment-link", "payment-method", "show", "status",
	} {
		if !strings.Contains(run.stdout, command) {
			t.Errorf("invoice help does not expose %q:\n%s", command, run.stdout)
		}
	}
}

func TestBillingCommandsPassTypedParameters(t *testing.T) {
	var received Operation
	executor := executorFunc(func(_ context.Context, operation Operation) (Result, error) {
		received = operation
		return Result{Human: "ok"}, nil
	})
	run := runCLI(
		t,
		nil,
		[]string{
			"--account", "personal",
			"billing", "invoice", "list",
			"--limit", "25", "--offset", "50",
		},
		"",
		false,
		nil,
		executor,
	)
	if run.exitCode != ExitOK {
		t.Fatalf("exit code = %d; stderr=%q", run.exitCode, run.stderr)
	}
	if received.Action != "billing.invoice.list" {
		t.Fatalf("operation = %+v", received)
	}
	if values := received.Parameters["limit"]; len(values) != 1 || values[0] != "25" {
		t.Errorf("limit = %v", values)
	}
	if values := received.Parameters["offset"]; len(values) != 1 || values[0] != "50" {
		t.Errorf("offset = %v", values)
	}
}

func TestBillingPaymentTypeMutationRequiresConfirmation(t *testing.T) {
	calls := 0
	executor := executorFunc(func(_ context.Context, operation Operation) (Result, error) {
		calls++
		return Result{Human: operation.Action}, nil
	})
	run := runCLI(
		t,
		nil,
		[]string{
			"--account", "personal",
			"billing", "invoice", "payment-method", "set", "123",
			"--type", "prepay", "--currency", "RUR",
		},
		"",
		false,
		nil,
		executor,
	)
	if run.exitCode != ExitInteractionRequired || calls != 0 {
		t.Fatalf("exit code = %d, calls = %d, stderr=%q", run.exitCode, calls, run.stderr)
	}
}

func TestBillingCommandsRejectUnsafeProviderInputsBeforeExecutor(t *testing.T) {
	calls := 0
	executor := executorFunc(func(_ context.Context, _ Operation) (Result, error) {
		calls++
		return Result{}, nil
	})
	for _, args := range [][]string{
		{"--account", "personal", "billing", "invoice", "list", "--limit", "1025"},
		{"--account", "personal", "--force", "billing", "invoice", "payment-method", "set", "123", "--type", "yacard", "--currency", "RUR"},
		{"--account", "personal", "billing", "balance", "--currency", "RUB"},
	} {
		run := runCLI(t, nil, args, "", false, nil, executor)
		if run.exitCode != ExitUsage {
			t.Errorf("args %v exit code = %d; stderr=%q", args, run.exitCode, run.stderr)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid billing input reached executor %d time(s)", calls)
	}
}

func TestGatedInvoiceCreationFailsBeforeDryRun(t *testing.T) {
	calls := 0
	executor := executorFunc(func(_ context.Context, _ Operation) (Result, error) {
		calls++
		return Result{}, nil
	})
	for _, args := range [][]string{{"--account", "personal", "--dry-run", "billing", "invoice", "create"}} {
		run := runCLI(t, nil, args, "", false, nil, executor)
		if run.exitCode != ExitCapability {
			t.Errorf("args %v exit code = %d; stdout=%q stderr=%q", args, run.exitCode, run.stdout, run.stderr)
		}
		if !strings.Contains(run.stderr, CodeCapability) {
			t.Errorf("args %v stderr = %q", args, run.stderr)
		}
	}
	if calls != 0 {
		t.Fatalf("gated billing command reached executor %d time(s)", calls)
	}
}

func TestBillingPaymentLinkRequiresConfirmationBeforeBrowserHandoff(t *testing.T) {
	calls := 0
	executor := executorFunc(func(_ context.Context, operation Operation) (Result, error) {
		calls++
		return Result{Human: operation.Action}, nil
	})
	run := runCLI(t, nil, []string{
		"--account", "personal", "billing", "invoice", "payment-link", "42",
	}, "", false, nil, executor)
	if run.exitCode != ExitInteractionRequired || calls != 0 || !strings.Contains(run.stderr, CodeConfirmationRequired) {
		t.Fatalf("exit code = %d, calls = %d, stderr=%q", run.exitCode, calls, run.stderr)
	}

	run = runCLI(t, nil, []string{
		"--account", "personal", "--force", "billing", "invoice", "payment-link", "42",
	}, "", false, nil, executor)
	if run.exitCode != ExitOK || calls != 1 {
		t.Fatalf("forced exit code = %d, calls = %d, stderr=%q", run.exitCode, calls, run.stderr)
	}

	run = runCLI(t, nil, []string{
		"--account", "personal", "--no-input", "--force", "billing", "invoice", "payment-link", "42",
	}, "", false, nil, executor)
	if run.exitCode != ExitInteractionRequired || calls != 1 || !strings.Contains(run.stderr, CodeInteractiveRequired) {
		t.Fatalf("no-input exit code = %d, calls = %d, stderr=%q", run.exitCode, calls, run.stderr)
	}
}

func TestCloudVPSCreatePassesTypedParametersAndNoWait(t *testing.T) {
	var received Operation
	executor := executorFunc(func(_ context.Context, operation Operation) (Result, error) {
		received = operation
		return Result{
			Human: "created",
			Plain: []string{"created"},
			Data:  map[string]bool{"created": true},
		}, nil
	})
	run := runCLI(
		t,
		nil,
		[]string{
			"--account", "personal",
			"--force",
			"vps", "--no-wait",
			"create",
			"--name", "fixture",
			"--size", "cloud-2",
			"--image", "ubuntu-24-04-amd64",
			"--region", "openstack-msk3",
			"--ssh-key", "6",
			"--backups",
		},
		"",
		false,
		nil,
		executor,
	)
	if run.exitCode != ExitOK {
		t.Fatalf("exit code = %d; stderr=%q", run.exitCode, run.stderr)
	}
	if received.Action != "vps.create" || !received.NoWait {
		t.Fatalf("operation = %+v", received)
	}
	for key, expected := range map[string]string{
		"name":    "fixture",
		"size":    "cloud-2",
		"image":   "ubuntu-24-04-amd64",
		"region":  "openstack-msk3",
		"ssh-key": "6",
		"backups": "true",
	} {
		if values := received.Parameters[key]; len(values) != 1 || values[0] != expected {
			t.Errorf("parameter %q = %v, want %q", key, values, expected)
		}
	}
}

func TestCloudVPSCommandsValidateBeforeExecutor(t *testing.T) {
	calls := 0
	executor := executorFunc(func(_ context.Context, _ Operation) (Result, error) {
		calls++
		return Result{}, nil
	})

	for name, args := range map[string][]string{
		"create requires catalog selections": {
			"--account", "personal", "--force", "vps", "create",
		},
		"IP allocation requires a bounded count": {
			"--account", "personal", "--force", "vps", "ip", "add", "42", "--ipv4-count", "5",
		},
		"plan discovery requires a region": {
			"--account", "personal", "vps", "plan", "list",
		},
		"wait timeout is bounded": {
			"--account", "personal", "vps", "--wait-timeout", "500ms", "action", "wait", "1",
		},
		"server identifiers are positive decimals": {
			"--account", "personal", "vps", "get", "server-name",
		},
	} {
		t.Run(name, func(t *testing.T) {
			run := runCLI(t, nil, args, "", false, nil, executor)
			if run.exitCode != ExitUsage {
				t.Fatalf("exit code = %d, want %d; stderr=%q", run.exitCode, ExitUsage, run.stderr)
			}
		})
	}
	if calls != 0 {
		t.Fatalf("executor called %d time(s) for invalid CloudVPS input", calls)
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
