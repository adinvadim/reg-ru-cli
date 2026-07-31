package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestS3BucketCreateBuildsTypedOperation(t *testing.T) {
	var received Operation
	executor := executorFunc(func(_ context.Context, operation Operation) (Result, error) {
		received = operation
		return Result{Human: "created", Data: map[string]bool{"created": true}}, nil
	})
	run := runCLI(t, nil, []string{
		"--account", "personal", "--force",
		"s3", "bucket", "create", "regru-test-bucket",
		"--quota-gb", "25", "--access", "public",
	}, "", false, nil, executor)
	if run.exitCode != ExitOK {
		t.Fatalf("exit code = %d; stderr=%q", run.exitCode, run.stderr)
	}
	if received.Action != "s3.bucket.create" || received.Capability != "s3.control_plane" {
		t.Fatalf("operation = %#v", received)
	}
	if received.Parameters["quota-gb"][0] != "25" || received.Parameters["access"][0] != "public" {
		t.Errorf("parameters = %#v", received.Parameters)
	}
}

func TestS3CommandsRejectUnsafeLocalInputBeforeExecutor(t *testing.T) {
	calls := 0
	executor := executorFunc(func(_ context.Context, _ Operation) (Result, error) {
		calls++
		return Result{Human: "unexpected"}, nil
	})
	for _, args := range [][]string{
		{"--account", "personal", "--force", "s3", "bucket", "create", "INVALID"},
		{"--account", "personal", "--force", "s3", "bucket", "update", "valid-bucket"},
		{"--account", "personal", "--force", "s3", "service", "quota", "set", "--quota-gb", "0"},
	} {
		run := runCLI(t, nil, args, "", false, nil, executor)
		if run.exitCode != ExitUsage {
			t.Errorf("args=%v exit code = %d; stderr=%q", args, run.exitCode, run.stderr)
		}
	}
	if calls != 0 {
		t.Fatalf("executor called %d times", calls)
	}
}

func TestS3ConfigurationDryRunValidatesDocumentWithoutExecutor(t *testing.T) {
	directory := t.TempDir()
	validPath := filepath.Join(directory, "cors.json")
	if err := os.WriteFile(validPath, []byte(`{"rules":[{"allowedMethods":["GET"],"allowedOrigins":["https://example.test"]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	executor := executorFunc(func(_ context.Context, _ Operation) (Result, error) {
		calls++
		return Result{}, nil
	})
	run := runCLI(t, nil, []string{
		"--account", "personal", "--dry-run",
		"s3", "bucket", "cors", "set", "valid-bucket", "--file", validPath,
	}, "", false, nil, executor)
	if run.exitCode != ExitOK {
		t.Fatalf("valid dry-run exit code = %d; stderr=%q", run.exitCode, run.stderr)
	}
	if calls != 0 {
		t.Fatal("dry-run reached the executor")
	}

	run = runCLI(t, nil, []string{
		"--account", "personal", "--dry-run",
		"s3", "bucket", "policy", "set", "valid-bucket",
		"--file", filepath.Join(directory, "missing.json"),
	}, "", false, nil, executor)
	if run.exitCode != ExitUsage {
		t.Fatalf("missing-file exit code = %d; stderr=%q", run.exitCode, run.stderr)
	}
}
