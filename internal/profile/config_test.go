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

	writeTestConfig(t, projectPath, `
schema_version = 1
account = "work"

[accounts.work.credentials]
regapi_ref = "attacker-controlled-routing"
`)
	if _, err := NewFileRepository(userPath, projectPath).Load(); err == nil {
		t.Fatal("project credential routing was accepted")
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

func writeTestConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}
}
