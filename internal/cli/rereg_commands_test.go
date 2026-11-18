package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adinvadim/reg-ru-cli/internal/profile"
)

var bidArgs = []string{"domain", "rereg", "bid", "moonbot.ru", "--price", "1950", "--max-price", "2000", "--contact", "profile", "--ns", "nic"}

func withReregClock(t *testing.T, now time.Time) {
	t.Helper()
	previous := reregNow
	reregNow = func() time.Time { return now }
	t.Cleanup(func() { reregNow = previous })
}

var moscow = time.FixedZone("MSK", 3*3600)

func TestReregPublicCommandsNeedNoAccount(t *testing.T) {
	var seen []Operation
	executor := executorFunc(func(_ context.Context, operation Operation) (Result, error) {
		seen = append(seen, operation)
		return Result{Human: "ok"}, nil
	})
	empty := profile.NewMemoryRepository(profile.Config{SchemaVersion: profile.SchemaVersion})
	for _, args := range [][]string{
		{"domain", "rereg", "catalog", "fetch"},
		{"domain", "rereg", "catalog", "search", "kuban", "--date", "tomorrow", "--zones", "ru,rf", "--with-bids", "--limit", "10"},
		{"--no-input", "--json", "domain", "rereg", "quote", "Домкрай.рф"},
	} {
		if run := runCLIWithProfiles(t, nil, args, "", false, nil, executor, empty); run.exitCode != ExitOK {
			t.Fatalf("%v exit = %d stderr = %q", args, run.exitCode, run.stderr)
		}
	}
	if len(seen) != 3 || seen[0].Action != "domain.rereg.catalog.fetch" || seen[2].Action != "domain.rereg.quote" {
		t.Fatalf("operations = %#v", seen)
	}
	for _, operation := range seen {
		if operation.Account != "" || operation.Credentials != nil {
			t.Fatalf("operation %s got an account or credentials", operation.Action)
		}
	}
	if got := seen[1].Parameters; got["date"][0] != "tomorrow" || got["with-bids"][0] != "true" || got["limit"][0] != "10" {
		t.Fatalf("search parameters = %#v", got)
	}
}

func TestReregCommandsValidateBeforeExecutor(t *testing.T) {
	withReregClock(t, time.Date(2026, 11, 18, 12, 0, 0, 0, moscow))
	calls := 0
	executor := executorFunc(func(_ context.Context, _ Operation) (Result, error) {
		calls++
		return Result{}, nil
	})
	without := func(flag string) []string {
		args := []string{"--account", "personal"}
		for index := 0; index < len(bidArgs); index++ {
			if bidArgs[index] == flag {
				index++
				continue
			}
			args = append(args, bidArgs[index])
		}
		return args
	}
	replace := func(flag, value string) []string {
		args := append([]string{"--account", "personal"}, bidArgs...)
		for index := range args {
			if args[index] == flag {
				args[index+1] = value
			}
		}
		return args
	}
	for _, args := range [][]string{
		{"domain", "rereg", "quote", "moonbot.com"},
		{"domain", "rereg", "quote", "https://moonbot.ru"},
		{"domain", "rereg", "catalog", "search", "--date", "19.11.2026"},
		{"domain", "rereg", "catalog", "search", "--zones", "com"},
		{"domain", "rereg", "catalog", "search", "--limit", "0"},
		without("--contact"), without("--max-price"), without("--ns"), without("--price"),
		replace("--contact", "passport"),
		replace("--price", "2500"),
		replace("--price", "1e3"),
		replace("--max-price", "0"),
		replace("--ns", "ns1.example.com"),
	} {
		if run := runCLI(t, nil, args, "", false, nil, executor); run.exitCode != ExitUsage {
			t.Errorf("%v exit = %d, want %d; stderr = %q", args, run.exitCode, ExitUsage, run.stderr)
		}
	}
	if calls != 0 {
		t.Fatalf("executor calls = %d, want 0", calls)
	}
}

func TestReregBidDryRunMakesNoCallsAndShowsPlan(t *testing.T) {
	withReregClock(t, time.Date(2026, 11, 18, 12, 0, 0, 0, moscow))
	marker := filepath.Join(t.TempDir(), "credential-process-ran")
	calls := 0
	executor := executorFunc(func(_ context.Context, _ Operation) (Result, error) {
		calls++
		return Result{}, errors.New("executor must not be called")
	})
	args := append([]string{"--json", "--account", "personal", "--dry-run"}, bidArgs...)
	run := runCLIWithProfiles(t, nil, args, "", false, nil, executor, profilesWithCredentialProcess(cliHelperCommand("mark", marker)))
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
				Domain          struct{ ASCII string } `json:"domain"`
				PriceKopecks    int64                  `json:"priceKopecks"`
				MaxPriceKopecks int64                  `json:"maxPriceKopecks"`
				Contact         string                 `json:"contact"`
				Nameservers     []string               `json:"nameservers"`
			} `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(run.stdout), &envelope); err != nil {
		t.Fatalf("decode %q: %v", run.stdout, err)
	}
	plan := envelope.Data.Plan
	if !envelope.Data.DryRun || plan.Domain.ASCII != "moonbot.ru" || plan.PriceKopecks != 195000 || plan.MaxPriceKopecks != 200000 ||
		plan.Contact != "profile" || len(plan.Nameservers) != 5 {
		t.Fatalf("plan = %#v", envelope.Data)
	}
	plain := runCLI(t, nil, append([]string{"--plain", "--account", "personal", "--dry-run"}, bidArgs...), "", false, nil, executor)
	if !strings.Contains(plain.stdout, "plan\tmoonbot.ru\tmoonbot.ru\t195000\t200000\tprofile\t") || calls != 0 {
		t.Fatalf("plain = %q calls = %d", plain.stdout, calls)
	}
}

func TestReregBidGuardWindowIsAPolicyRefusal(t *testing.T) {
	calls := 0
	executor := executorFunc(func(_ context.Context, _ Operation) (Result, error) {
		calls++
		return Result{Human: "ok"}, nil
	})
	withReregClock(t, time.Date(2026, 11, 18, 14, 0, 0, 0, time.UTC)) // 17:00 MSK
	run := runCLI(t, nil, append([]string{"--json", "--account", "personal", "--force"}, bidArgs...), "", false, nil, executor)
	var envelope struct {
		Error struct {
			Code     string         `json:"code"`
			ExitCode int            `json:"exitCode"`
			Details  map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(run.stderr), &envelope); err != nil {
		t.Fatalf("decode %q: %v", run.stderr, err)
	}
	if run.exitCode != ExitPolicy || envelope.Error.Code != CodePolicyRefused || envelope.Error.Details["reason"] != "guard_window" ||
		envelope.Error.Details["override"] != "--allow-release-window" || calls != 0 {
		t.Fatalf("exit = %d envelope = %#v calls = %d", run.exitCode, envelope, calls)
	}
	allowed := runCLI(t, nil, append([]string{"--account", "personal", "--force", "--no-input"}, append(bidArgs, "--allow-release-window")...), "", false, nil, executor)
	if allowed.exitCode != ExitOK || calls != 1 {
		t.Fatalf("allowed exit = %d calls = %d stderr = %q", allowed.exitCode, calls, allowed.stderr)
	}
}

func TestReregBidRequiresConfirmationOrForce(t *testing.T) {
	withReregClock(t, time.Date(2026, 11, 18, 12, 0, 0, 0, moscow))
	calls := 0
	executor := executorFunc(func(_ context.Context, operation Operation) (Result, error) {
		calls++
		if !operation.Force {
			t.Error("the executor ran without --force")
		}
		return Result{Human: "ok"}, nil
	})
	run := runCLI(t, nil, append([]string{"--account", "personal", "--no-input"}, bidArgs...), "", false, nil, executor)
	if run.exitCode != ExitInteractionRequired || calls != 0 || !strings.Contains(run.stderr, CodeConfirmationRequired) {
		t.Fatalf("exit = %d calls = %d stderr = %q", run.exitCode, calls, run.stderr)
	}
	declined := runCLI(t, nil, append([]string{"--account", "personal"}, bidArgs...), "n\n", true, nil, executor)
	if declined.exitCode != ExitInteractionRequired || calls != 0 || !strings.Contains(declined.stderr, "cannot be cancelled") {
		t.Fatalf("declined exit = %d stderr = %q", declined.exitCode, declined.stderr)
	}
	forced := runCLI(t, nil, append([]string{"--account", "personal", "--no-input", "--force"}, bidArgs...), "", false, nil, executor)
	if forced.exitCode != ExitOK || calls != 1 {
		t.Fatalf("forced exit = %d calls = %d", forced.exitCode, calls)
	}
}

func TestReregCapabilitiesAreListed(t *testing.T) {
	for _, check := range []struct {
		command []string
		want    []string
	}{
		{[]string{"/bin/true"}, []string{"domain.rereg.bid\tconfigured", "domain.rereg.bids\tconfigured", "domain.rereg.catalog\tconfigured", "domain.rereg.quote\tconfigured"}},
		{nil, []string{"domain.rereg.bid\tnot_configured", "domain.rereg.bids\tnot_configured", "domain.rereg.quote\tconfigured"}},
	} {
		profiles := profile.NewMemoryRepository(profile.Config{
			SchemaVersion: profile.SchemaVersion,
			Accounts: map[string]profile.Account{"personal": {
				ID: "p_" + strings.Repeat("a", 26), Provider: "reg.ru",
				CredentialProcess: profile.CredentialProcess{Command: check.command},
			}},
		})
		run := runCLIWithProfiles(t, nil, []string{"--plain", "--account", "personal", "capability", "list"}, "", false, nil, nil, profiles)
		for _, line := range check.want {
			if !strings.Contains(run.stdout, line) {
				t.Errorf("capability list lacks %q:\n%s", line, run.stdout)
			}
		}
	}
}
