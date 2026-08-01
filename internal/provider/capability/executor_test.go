package capability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/adinvadim/reg-ru-cli/internal/cli"
	"github.com/adinvadim/reg-ru-cli/internal/credentialprocess"
	"github.com/adinvadim/reg-ru-cli/internal/profile"
)

type executorFunc func(context.Context, cli.Operation) (cli.Result, error)

func (f executorFunc) Execute(ctx context.Context, operation cli.Operation) (cli.Result, error) {
	return f(ctx, operation)
}

func TestProbeCallsOnlyConfiguredCapabilitiesAndRedactsProviderResults(t *testing.T) {
	const (
		accountID = "p_aaaaaaaaaaaaaaaaaaaaaaaaaa"
		sentinel  = "private-provider-result"
	)
	profiles := profile.NewMemoryRepository(profile.Config{
		SchemaVersion: profile.SchemaVersion,
		Accounts: map[string]profile.Account{
			"work": {
				ID:       accountID,
				Provider: "reg.ru",
				Portal:   profile.Portal{SessionRef: "s_private-session-reference"},
			},
		},
	})
	var calls []cli.Operation
	fallback := executorFunc(func(_ context.Context, operation cli.Operation) (cli.Result, error) {
		calls = append(calls, operation)
		return cli.Result{
			Human: sentinel,
			Plain: []string{sentinel},
			Data:  map[string]string{"private": sentinel},
		}, nil
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := cli.Execute(context.Background(), []string{
		"--account", "work", "--json", "capability", "probe",
	}, cli.Options{
		In:          strings.NewReader(""),
		Out:         &stdout,
		ErrOut:      &stderr,
		Getenv:      func(string) string { return "" },
		InputIsTTY:  func() bool { return false },
		OutputIsTTY: func() bool { return false },
		ErrorIsTTY:  func() bool { return false },
		Executor:    NewExecutor(profiles, fallback),
		Profiles:    profiles,
	})
	if exitCode != cli.ExitOK {
		t.Fatalf("exit code = %d; stderr=%q", exitCode, stderr.String())
	}
	if strings.Contains(stdout.String(), sentinel) || strings.Contains(stdout.String(), accountID) {
		t.Fatalf("probe exposed a private provider result: %s", stdout.String())
	}

	var envelope struct {
		Data struct {
			Account      string `json:"account"`
			Capabilities []struct {
				Capability string `json:"capability"`
				State      string `json:"state"`
				Reason     string `json:"reason"`
			} `json:"capabilities"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode stdout: %v\n%s", err, stdout.String())
	}
	if envelope.Data.Account != "work" || len(envelope.Data.Capabilities) != 4 {
		t.Fatalf("unexpected capability report: %+v", envelope.Data)
	}
	wantActions := []string{
		"auth.status",
		"billing.balance",
		"s3.service.show",
		"support.ticket.list",
	}
	if len(calls) != len(wantActions) {
		t.Fatalf("calls = %d, want %d: %+v", len(calls), len(wantActions), calls)
	}
	for index, want := range wantActions {
		if calls[index].Action != want {
			t.Errorf("call %d action = %q, want %q", index, calls[index].Action, want)
		}
		if calls[index].ProfileID != accountID || calls[index].Account != "work" {
			t.Errorf("call %d lost selected profile routing: %+v", index, calls[index])
		}
		if calls[index].Force {
			t.Errorf("call %d inherited --force", index)
		}
		if calls[index].RequestTimeout <= 0 || calls[index].RequestTimeout > defaultProbeTimeout {
			t.Errorf("call %d timeout = %s, want bounded by %s", index, calls[index].RequestTimeout, defaultProbeTimeout)
		}
	}
	if source := calls[1].Parameters["source"]; len(source) != 1 || source[0] != "cloudvps" {
		t.Errorf("CloudVPS probe source = %v", source)
	}
}

func TestProbeSkipsCapabilitiesWithoutLocalRouting(t *testing.T) {
	profiles := profile.NewMemoryRepository(profile.Config{
		SchemaVersion: profile.SchemaVersion,
		Accounts: map[string]profile.Account{
			"work": {
				ID:       "p_aaaaaaaaaaaaaaaaaaaaaaaaaa",
				Provider: "reg.ru",
			},
		},
	})
	calls := 0
	executor := NewExecutor(profiles, executorFunc(func(_ context.Context, _ cli.Operation) (cli.Result, error) {
		calls++
		return cli.Result{}, nil
	}))

	result, err := executor.Execute(context.Background(), probeOperation())
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	results := result.Data.(map[string]any)["capabilities"].([]probeResult)
	if calls != 0 || len(results) != 0 {
		t.Fatalf("calls=%d results=%+v, want no provider probes", calls, results)
	}
}

func TestProbeReportsPartialAvailabilityWithStableReasons(t *testing.T) {
	profiles := configuredProfiles()
	fallback := executorFunc(func(_ context.Context, operation cli.Operation) (cli.Result, error) {
		switch {
		case operation.Action == "auth.status":
			return cli.Result{Data: map[string]string{"state": "active"}}, nil
		case operation.Action == "billing.balance" && operation.Parameters["source"][0] == "cloudvps":
			return cli.Result{}, cli.AuthenticationExpired()
		case operation.Action == "billing.balance" && operation.Parameters["source"][0] == "regapi":
			return cli.Result{}, &credentialprocess.ProcessError{Code: "credential_field_unavailable"}
		case operation.Action == "s3.service.show":
			return cli.Result{}, cli.PrivateContractDrift(operation.Capability)
		case operation.Action == "support.ticket.list":
			return cli.Result{}, cli.CapabilityUnavailableWithReason(
				operation.Capability,
				"private-provider-reason-that-must-not-pass-through",
				"private provider message that must not pass through",
			)
		default:
			return cli.Result{}, errors.New("unexpected probe")
		}
	})

	result, err := NewExecutor(profiles, fallback).Execute(context.Background(), probeOperation())
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	data := result.Data.(map[string]any)
	results := data["capabilities"].([]probeResult)
	want := []probeResult{
		{Capability: "auth.session_status", State: "available", Reason: "none"},
		{Capability: "cloudvps.instances", State: "unavailable", Reason: "authentication_lost"},
		{Capability: "billing.read", State: "unavailable", Reason: "missing_credentials"},
		{Capability: "s3.control_plane", State: "unavailable", Reason: "private_contract_drift"},
		{Capability: "support.ticket.list", State: "unavailable", Reason: "adapter_unavailable"},
	}
	if len(results) != len(want) {
		t.Fatalf("results = %+v", results)
	}
	for index := range want {
		if results[index] != want[index] {
			t.Errorf("result %d = %+v, want %+v", index, results[index], want[index])
		}
	}
	for _, output := range append([]string{result.Human}, result.Plain...) {
		if strings.Contains(output, "private-provider") || strings.Contains(output, "private provider") {
			t.Errorf("probe exposed provider error content: %q", output)
		}
	}
}

func TestProbeBoundsEachCallAndContinuesAfterProbeTimeout(t *testing.T) {
	profiles := configuredProfiles()
	var calls []string
	fallback := executorFunc(func(ctx context.Context, operation cli.Operation) (cli.Result, error) {
		calls = append(calls, operation.Action)
		if operation.Action == "auth.status" {
			<-ctx.Done()
			return cli.Result{}, ctx.Err()
		}
		return cli.Result{}, nil
	})
	executor := NewExecutor(profiles, fallback)
	executor.timeout = 10 * time.Millisecond

	result, err := executor.Execute(context.Background(), probeOperation())
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	results := result.Data.(map[string]any)["capabilities"].([]probeResult)
	if len(calls) != 5 || len(results) != 5 {
		t.Fatalf("calls=%v results=%+v", calls, results)
	}
	if results[0].Reason != "timeout" {
		t.Errorf("first result = %+v, want timeout", results[0])
	}
	for _, result := range results[1:] {
		if result.State != "available" {
			t.Errorf("probe did not continue after timeout: %+v", results)
		}
	}
}

func TestProbeStopsOnParentCancellation(t *testing.T) {
	profiles := configuredProfiles()
	ctx, cancel := context.WithCancel(context.Background())
	var calls int
	fallback := executorFunc(func(callCtx context.Context, _ cli.Operation) (cli.Result, error) {
		calls++
		cancel()
		<-callCtx.Done()
		return cli.Result{}, callCtx.Err()
	})

	_, err := NewExecutor(profiles, fallback).Execute(ctx, probeOperation())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestProbeHumanAndPlainOutputExposeOnlyNormalizedStates(t *testing.T) {
	profiles := profile.NewMemoryRepository(profile.Config{
		SchemaVersion: profile.SchemaVersion,
		Accounts: map[string]profile.Account{
			"work": {
				ID:       "p_aaaaaaaaaaaaaaaaaaaaaaaaaa",
				Provider: "reg.ru",
				Portal:   profile.Portal{SessionRef: "s_private-session-reference"},
			},
		},
	})
	fallback := executorFunc(func(_ context.Context, _ cli.Operation) (cli.Result, error) {
		return cli.Result{Human: "private-provider-result"}, nil
	})
	for _, testCase := range []struct {
		name     string
		flag     string
		contains string
	}{
		{name: "human", contains: "Capability probe for work: 4 available, 0 unavailable"},
		{name: "plain", flag: "--plain", contains: "auth.session_status\tavailable\tnone\n"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			args := []string{"--account", "work"}
			if testCase.flag != "" {
				args = append(args, testCase.flag)
			}
			args = append(args, "capability", "probe")
			stdout, stderr, exitCode := executeCLI(args, profiles, NewExecutor(profiles, fallback))
			if exitCode != cli.ExitOK {
				t.Fatalf("exit code = %d; stderr=%q", exitCode, stderr)
			}
			if !strings.Contains(stdout, testCase.contains) {
				t.Errorf("stdout does not contain %q:\n%s", testCase.contains, stdout)
			}
			if strings.Contains(stdout, "private-provider-result") {
				t.Errorf("stdout exposed provider result: %s", stdout)
			}
		})
	}
}

func configuredProfiles() profile.Repository {
	return profile.NewMemoryRepository(profile.Config{
		SchemaVersion: profile.SchemaVersion,
		Accounts: map[string]profile.Account{
			"work": {
				ID:       "p_aaaaaaaaaaaaaaaaaaaaaaaaaa",
				Provider: "reg.ru",
				Portal:   profile.Portal{SessionRef: "s_private-session-reference"},
				CredentialProcess: profile.CredentialProcess{
					Command: []string{"/private/credential-helper"},
				},
			},
		},
	})
}

func probeOperation() cli.Operation {
	return cli.Operation{
		Capability:     "account.capabilities",
		Action:         "capability.probe",
		ProfileID:      "p_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		Account:        "work",
		RequestTimeout: 30 * time.Second,
		Force:          true,
	}
}

func executeCLI(args []string, profiles profile.Repository, executor cli.Executor) (string, string, int) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := cli.Execute(context.Background(), args, cli.Options{
		In:          strings.NewReader(""),
		Out:         &stdout,
		ErrOut:      &stderr,
		Getenv:      func(string) string { return "" },
		InputIsTTY:  func() bool { return false },
		OutputIsTTY: func() bool { return false },
		ErrorIsTTY:  func() bool { return false },
		Executor:    executor,
		Profiles:    profiles,
	})
	return stdout.String(), stderr.String(), exitCode
}
