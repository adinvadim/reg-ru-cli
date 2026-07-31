package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryProjectConfigMayOnlySelectAUserAccount(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	userPath := filepath.Join(root, "user", "config.toml")
	projectPath := filepath.Join(root, "project", ".regru", "config.toml")
	writeTestConfig(t, userPath, `
schema_version = 1
default_account = "personal"

[accounts.personal]
id = "p_aaaaaaaaaaaaaaaaaaaaaaaaaa"
provider = "reg.ru"

[accounts.work]
id = "p_bbbbbbbbbbbbbbbbbbbbbbbbbb"
provider = "reg.ru"

[accounts.work.credentials]
regapi_ref = "opaque-reference"

[accounts.work.cloud]
environment_id = "opaque-environment"
s3_endpoint = "https://s3.regru.cloud"
s3_signing_region = "us-east-1"
s3_key_pair_id = "42"

[accounts.work.credential_process]
command = ["/usr/local/bin/credential-helper", "get", "work"]
`)
	writeTestConfig(t, projectPath, `
schema_version = 1
account = "work"
`)

	config, err := NewFileRepository(userPath, projectPath).Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if config.ProjectAccount != "work" || config.DefaultAccount != "personal" {
		t.Fatalf("selection config = %#v", config)
	}
	if config.Accounts["work"].Credentials.REGAPIRef != "opaque-reference" {
		t.Error("project selection replaced user-owned account metadata")
	}
	cloud := config.Accounts["work"].Cloud
	if cloud.EnvironmentID != "opaque-environment" ||
		cloud.S3Endpoint != "https://s3.regru.cloud" ||
		cloud.S3SigningRegion != "us-east-1" || cloud.S3KeyPairID != "42" {
		t.Errorf("S3 routing metadata = %#v", cloud)
	}
	command := config.Accounts["work"].CredentialProcess.Command
	if len(command) != 3 || command[0] != "/usr/local/bin/credential-helper" {
		t.Errorf("credential process command = %#v", command)
	}

	writeTestConfig(t, projectPath, `
schema_version = 1
account = "work"

[accounts.work.credentials]
regapi_ref = "attacker-controlled-routing"
`)
	if _, err := NewFileRepository(userPath, projectPath).Load(); err == nil {
		t.Fatal("project credential routing was accepted")
	}

	writeTestConfig(t, projectPath, `
schema_version = 1
account = "work"

[credential_process]
command = ["/tmp/project-controlled-helper"]
`)
	if _, err := NewFileRepository(userPath, projectPath).Load(); err == nil {
		t.Fatal("project credential process was accepted")
	}
}

func TestRepositoryAddsAnAccountWithoutWritingSecrets(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	userPath := filepath.Join(root, "config", "regru", "config.toml")
	repository := NewFileRepository(userPath, "")

	added, err := repository.Add("work", Account{Label: "Work"})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if !strings.HasPrefix(added.ID, "p_") || added.Provider != "reg.ru" {
		t.Errorf("generated account = %#v", added)
	}

	info, err := os.Stat(userPath)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("config mode = %#o, want 0600", got)
	}

	data, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := strings.ToLower(string(data))
	for _, forbidden := range []string{"password", "secret_value", "token ="} {
		if strings.Contains(text, forbidden) {
			t.Errorf("config contains forbidden secret field %q:\n%s", forbidden, text)
		}
	}
}

func TestRepositoryAtomicallyUpdatesOnlyOpaquePortalSessionReference(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	repository := NewFileRepository(filepath.Join(directory, "config.toml"), "")
	account, err := repository.Add("work", Account{
		ID:       "p_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		Provider: "reg.ru",
	})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if account.Portal.SessionRef != "" {
		t.Fatalf("initial session ref = %q, want empty", account.Portal.SessionRef)
	}

	const sessionRef = "s_bbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := repository.SetPortalSession("work", sessionRef); err != nil {
		t.Fatalf("SetPortalSession() error = %v", err)
	}
	config, err := repository.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := config.Accounts["work"].Portal.SessionRef; got != sessionRef {
		t.Errorf("session ref = %q, want %q", got, sessionRef)
	}

	if err := repository.SetPortalSession("work", ""); err != nil {
		t.Fatalf("clear SetPortalSession() error = %v", err)
	}
	config, err = repository.Load()
	if err != nil {
		t.Fatalf("Load() after clear error = %v", err)
	}
	if got := config.Accounts["work"].Portal.SessionRef; got != "" {
		t.Errorf("session ref after clear = %q, want empty", got)
	}
}

func writeTestConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}
}
