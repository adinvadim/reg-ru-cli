package credentialprocess

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestResolverLazilyRunsConfiguredProcessAndReturnsFields(t *testing.T) {
	t.Parallel()

	resolver := New(
		helperCommand("valid"),
		5*time.Second,
	)
	t.Cleanup(resolver.Close)

	value, err := resolver.Resolve(context.Background(), "regapi.password")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if string(value) != "synthetic-process-password" {
		t.Error("Resolve() returned unexpected value")
	}
	value[0] = 'X'
	again, err := resolver.Resolve(context.Background(), "regapi.password")
	if err != nil || string(again) != "synthetic-process-password" {
		t.Error("Resolve() returned mutable internal storage")
	}
}

func TestResolverNeverIncludesHelperStderrInErrors(t *testing.T) {
	t.Parallel()

	resolver := New(helperCommand("fail"), 5*time.Second)
	t.Cleanup(resolver.Close)
	_, err := resolver.Resolve(context.Background(), "cloudvps.token")
	if err == nil {
		t.Fatal("Resolve() succeeded")
	}
	if strings.Contains(err.Error(), "synthetic-helper-stderr-secret") {
		t.Errorf("error leaked helper stderr: %q", err)
	}
}

func TestEnvelopeRejectsDuplicateUnknownAndPartialFields(t *testing.T) {
	t.Parallel()

	tests := []string{
		`{"schemaVersion":"regru.credential-process/v1","schemaVersion":"regru.credential-process/v1","fields":{"cloudvps.token":"value"}}`,
		`{"schemaVersion":"regru.credential-process/v1","fields":{"regapi.password":"value"}}`,
		`{"schemaVersion":"regru.credential-process/v1","fields":{"unknown.password":"value"}}`,
	}
	for _, input := range tests {
		values, err := parseEnvelope(strings.NewReader(input))
		wipe(values)
		if err == nil {
			t.Error("parseEnvelope accepted invalid helper output")
		}
	}
}

func TestResolverBoundsCredentialProcessTime(t *testing.T) {
	t.Parallel()

	resolver := New(helperCommand("slow"), 20*time.Millisecond)
	t.Cleanup(resolver.Close)
	started := time.Now()
	_, err := resolver.Resolve(context.Background(), "cloudvps.token")
	if err == nil {
		t.Fatal("Resolve() succeeded")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("credential process cancellation took %s", elapsed)
	}
	var processErr *ProcessError
	if !errors.As(err, &processErr) ||
		processErr.Code != "credential_process_timeout" {
		t.Errorf("error = %v, want credential_process_timeout", err)
	}
}

func TestResolverHonorsCancellationAndBoundsOutput(t *testing.T) {
	t.Parallel()

	cancelled := New(helperCommand("valid"), 5*time.Second)
	t.Cleanup(cancelled.Close)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := cancelled.Resolve(ctx, "regapi.password")
	var processErr *ProcessError
	if !errors.As(err, &processErr) ||
		processErr.Code != "credential_process_cancelled" {
		t.Errorf("cancelled error = %v, want credential_process_cancelled", err)
	}

	oversized := New(helperCommand("oversized"), 5*time.Second)
	t.Cleanup(oversized.Close)
	_, err = oversized.Resolve(context.Background(), "cloudvps.token")
	if !errors.As(err, &processErr) ||
		processErr.Code != "credential_process_output_too_large" {
		t.Errorf("oversized error = %v, want credential_process_output_too_large", err)
	}
}

func helperCommand(mode string) []string {
	return []string{
		os.Args[0],
		"-test.run=^TestCredentialProcessHelper$",
		"--",
		mode,
	}
}

func TestCredentialProcessHelper(t *testing.T) {
	mode := ""
	for index, argument := range os.Args {
		if argument == "--" && index+1 < len(os.Args) {
			mode = os.Args[index+1]
			break
		}
	}
	switch mode {
	case "":
		return
	case "valid":
		fmt.Fprint(os.Stdout, `{
			"schemaVersion":"regru.credential-process/v1",
			"fields":{
				"regapi.username":"synthetic-process-user",
				"regapi.password":"synthetic-process-password"
			}
		}`)
		os.Exit(0)
	case "fail":
		fmt.Fprint(os.Stderr, "synthetic-helper-stderr-secret")
		os.Exit(17)
	case "slow":
		time.Sleep(10 * time.Second)
		os.Exit(0)
	case "oversized":
		fmt.Fprint(os.Stdout, strings.Repeat("x", MaxBundleBytes+1))
		os.Exit(0)
	default:
		os.Exit(19)
	}
}
