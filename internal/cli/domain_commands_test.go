package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adinvadim/reg-ru-cli/internal/profile"
)

func TestDomainSearchAndCheckNeedNoAccount(t *testing.T) {
	var seen []Operation
	executor := executorFunc(func(_ context.Context, operation Operation) (Result, error) {
		seen = append(seen, operation)
		return Result{Human: "ok"}, nil
	})
	empty := profile.NewMemoryRepository(profile.Config{SchemaVersion: profile.SchemaVersion})
	for _, args := range [][]string{
		{"domain", "search", "кубань", "квартал|кварталы", "--zones", "ru,rf,com", "--limit", "10"},
		{"domain", "check", "domkray.ru", "домкрай.рф"},
	} {
		run := runCLIWithProfiles(t, nil, args, "", false, nil, executor, empty)
		if run.exitCode != ExitOK {
			t.Fatalf("%v exit = %d stderr = %q", args, run.exitCode, run.stderr)
		}
	}
	if len(seen) != 2 || seen[0].Account != "" || seen[0].Credentials != nil || seen[1].Action != "domain.check" {
		t.Fatalf("operations = %#v", seen)
	}
	if got := seen[0].Parameters; got["zones"][0] != "ru,rf,com" || got["limit"][0] != "10" {
		t.Fatalf("parameters = %#v", got)
	}
}

func TestDomainCommandsValidateBeforeExecutor(t *testing.T) {
	calls := 0
	executor := executorFunc(func(_ context.Context, _ Operation) (Result, error) {
		calls++
		return Result{}, nil
	})
	for _, args := range [][]string{
		{"domain", "check", "https://domkray.ru"},
		{"domain", "check", "www.domkray.ru"},
		{"domain", "check", "kubanрф.рф"},
		{"domain", "search", "kuban", "--zones", "org"},
		{"domain", "search", "kuban", "--limit", "61"},
		{"--account", "personal", "domain", "buy", "domkray.ru"},
		{"--account", "personal", "domain", "buy", "domkray.ru", "--ns", "ns1.example.com"},
		{"--account", "personal", "domain", "buy", "domkray.ru", "--ns", "nic", "--max-price", "1e3"},
		{"--account", "personal", "domain", "buy", "domkray.ru", "--ns", "nic", "--user-servid", "XYZ"},
		{"--account", "personal", "domain", "ns", "set", "domkray.ru", "--ns", "bad_host,ns2.example.com"},
	} {
		run := runCLI(t, nil, args, "", false, nil, executor)
		if run.exitCode != ExitUsage {
			t.Errorf("%v exit = %d, want %d; stderr = %q", args, run.exitCode, ExitUsage, run.stderr)
		}
	}
	if calls != 0 {
		t.Fatalf("executor calls = %d, want 0", calls)
	}
}

func TestDomainBuyDryRunMakesNoCallsAndShowsPlan(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "credential-process-ran")
	calls := 0
	executor := executorFunc(func(_ context.Context, _ Operation) (Result, error) {
		calls++
		return Result{}, errors.New("executor must not be called")
	})
	run := runCLIWithProfiles(t, nil,
		[]string{"--json", "--account", "personal", "--dry-run", "domain", "buy", "Домкрай.РФ", "--ns", "nic", "--max-price", "300"},
		"", false, nil, executor, profilesWithCredentialProcess(cliHelperCommand("mark", marker)))
	if run.exitCode != ExitOK || calls != 0 {
		t.Fatalf("exit = %d calls = %d stderr = %q", run.exitCode, calls, run.stderr)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("credential process ran during dry-run")
	}
	var envelope struct {
		Data struct {
			DryRun bool `json:"dry_run"`
			Plan   struct {
				Domain struct {
					Unicode string `json:"unicode"`
					ASCII   string `json:"ascii"`
				} `json:"domain"`
				Nameservers []string `json:"nameservers"`
				MaxPrice    string   `json:"maxPrice"`
				Payment     string   `json:"payment"`
			} `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(run.stdout), &envelope); err != nil {
		t.Fatalf("decode %q: %v", run.stdout, err)
	}
	plan := envelope.Data.Plan
	if !envelope.Data.DryRun || plan.Domain.Unicode != "домкрай.рф" || plan.Domain.ASCII != "xn--80ahqcjlq.xn--p1ai" ||
		len(plan.Nameservers) != 5 || plan.Nameservers[0] != "ns3-l2.nic.ru" || plan.MaxPrice != "300" || plan.Payment != "balance" {
		t.Fatalf("plan = %#v", envelope.Data)
	}
}

func TestDomainNameserverDryRunMakesNoCalls(t *testing.T) {
	calls := 0
	executor := executorFunc(func(_ context.Context, _ Operation) (Result, error) {
		calls++
		return Result{}, nil
	})
	run := runCLI(t, nil, []string{"--json", "--account", "personal", "--dry-run", "domain", "ns", "set", "domkray.ru", "--ns", "nic"}, "", false, nil, executor)
	if run.exitCode != ExitOK || calls != 0 || !strings.Contains(run.stdout, "ns8-cloud.nic.ru") {
		t.Fatalf("exit = %d calls = %d stdout = %q", run.exitCode, calls, run.stdout)
	}
}

func TestDomainBuyAppliesPolicyInsteadOfPrompt(t *testing.T) {
	var received Operation
	executor := executorFunc(func(_ context.Context, operation Operation) (Result, error) {
		received = operation
		return Result{Human: "bought"}, nil
	})
	run := runCLI(t, nil,
		[]string{"--account", "personal", "--no-input", "domain", "buy", "domkray.ru", "--ns", "nic", "--max-renewal", "1200"},
		"", false, nil, executor)
	if run.exitCode != ExitOK || strings.Contains(run.stderr, "Confirm") {
		t.Fatalf("exit = %d stderr = %q", run.exitCode, run.stderr)
	}
	if received.Action != "domain.buy" || received.Force || received.Parameters["ns"][0] != "nic" || received.Parameters["max-renewal"][0] != "1200" {
		t.Fatalf("operation = %#v", received)
	}

	run = runCLI(t, nil, []string{"--account", "personal", "--force", "domain", "buy", "domkray.ru", "--ns", "nic"}, "", false, nil, executor)
	if run.exitCode != ExitOK || !received.Force {
		t.Fatalf("--force was not passed to the spending policy: exit %d", run.exitCode)
	}
}

func TestDomainNameserverChangeRequiresConfirmation(t *testing.T) {
	calls := 0
	executor := executorFunc(func(_ context.Context, _ Operation) (Result, error) {
		calls++
		return Result{Human: "set"}, nil
	})
	run := runCLI(t, nil, []string{"--account", "personal", "--no-input", "domain", "ns", "set", "domkray.ru", "--ns", "nic"}, "", false, nil, executor)
	if run.exitCode != ExitInteractionRequired || calls != 0 {
		t.Fatalf("exit = %d calls = %d", run.exitCode, calls)
	}
	run = runCLI(t, nil, []string{"--account", "personal", "domain", "ns", "set", "domkray.ru", "--ns", "nic"}, "y\n", true, nil, executor)
	if run.exitCode != ExitOK || calls != 1 || !strings.Contains(run.stderr, "24 hours") {
		t.Fatalf("exit = %d calls = %d stderr = %q", run.exitCode, calls, run.stderr)
	}
}

func TestDomainOutputBlocksCredentialMaterial(t *testing.T) {
	const sentinel = "synthetic-process-password"
	leaking := executorFunc(func(ctx context.Context, operation Operation) (Result, error) {
		value, _ := operation.Credentials.Resolve(ctx, "regapi.password")
		return Result{}, OutcomeUnknownWithDetails("domain.buy", "", map[string]any{"echo": string(value)})
	})
	run := runCLIWithProfiles(t, nil, []string{"--account", "personal", "domain", "buy", "domkray.ru", "--ns", "nic"},
		"", false, nil, leaking, profilesWithCredentialProcess(cliHelperCommand("regapi")))
	if strings.Contains(run.stderr, sentinel) || !strings.Contains(run.stderr, CodeSensitiveOutput) {
		t.Fatalf("credential material was not blocked: %q", run.stderr)
	}
}

func TestDomainCapabilitiesAreListed(t *testing.T) {
	profiles := profile.NewMemoryRepository(profile.Config{
		SchemaVersion: profile.SchemaVersion,
		Accounts: map[string]profile.Account{"personal": {
			ID: "p_" + strings.Repeat("a", 26), Provider: "reg.ru",
			CredentialProcess: profile.CredentialProcess{Command: []string{"/bin/true"}},
			Domain:            profile.Domain{ContactProfileType: "RU.PP", ContactProfileName: "owner"},
		}},
	})
	run := runCLIWithProfiles(t, nil, []string{"--plain", "--account", "personal", "capability", "list"}, "", false, nil, nil, profiles)
	for _, line := range []string{
		"domain.search\tconfigured", "domain.quote\tconfigured", "domain.buy\tconfigured",
		"domain.list\tconfigured", "domain.ns\tconfigured", "domain.registrar_check\tnot_configured",
	} {
		if !strings.Contains(run.stdout, line) {
			t.Errorf("capability list lacks %q:\n%s", line, run.stdout)
		}
	}
	if strings.Contains(run.stdout, "owner") {
		t.Error("capability list leaked the contact profile name")
	}
}
