package authcli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/adinvadim/reg-ru-cli/internal/cli"
	"github.com/adinvadim/reg-ru-cli/internal/profile"
	"github.com/adinvadim/reg-ru-cli/internal/provider/portal/authcli"
	"github.com/adinvadim/reg-ru-cli/internal/provider/portal/session"
)

func TestCLIAuthLifecyclePersistsOnlyOpaqueSessionReference(t *testing.T) {
	t.Parallel()

	const alias = "personal"
	const profileID = "p_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	const providerLogin = "portal-login@example.test"
	profiles := profile.NewMemoryRepository(profile.Config{
		SchemaVersion: profile.SchemaVersion,
		Accounts: map[string]profile.Account{
			alias: {
				ID:       profileID,
				Provider: "reg.ru",
			},
		},
	})
	digest := bytes.Repeat([]byte{0x77}, session.IdentityDigestBytes)
	store := session.NewFileStore(t.TempDir())
	broker := session.NewBroker(
		store,
		&lifecycleBrowserFactory{digest: digest, providerLogin: providerLogin},
		session.Options{LoginURL: "https://www.reg.ru/user/account/"},
	)
	executor := authcli.New(profiles, broker, cli.UnavailableExecutor{})

	login := executeCLI(t, profiles, executor,
		"--json",
		"--force",
		"--account", alias,
		"auth", "login",
		"--login-timeout", "1m",
	)
	if login.exitCode != cli.ExitOK {
		t.Fatalf("login exit = %d; stderr=%s", login.exitCode, login.stderr)
	}
	assertAuthState(t, login.stdout, "active")
	assertProviderLogin(t, login.stdout, providerLogin)
	assertCapability(t, login.stdout, "auth.browser_session")

	config, err := profiles.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	sessionRef := config.Accounts[alias].Portal.SessionRef
	if sessionRef == "" {
		t.Fatal("profile session ref is empty after login")
	}
	for _, output := range []string{login.stdout, login.stderr} {
		if strings.Contains(output, sessionRef) ||
			strings.Contains(output, string(digest)) {
			t.Fatalf("auth output exposed session material: %q", output)
		}
	}

	reauthentication := executeCLI(t, profiles, executor,
		"--json",
		"--force",
		"--account", alias,
		"auth", "login",
		"--login-timeout", "1m",
	)
	if reauthentication.exitCode != cli.ExitOK {
		t.Fatalf(
			"reauthentication exit = %d; stderr=%s",
			reauthentication.exitCode,
			reauthentication.stderr,
		)
	}
	config, err = profiles.Load()
	if err != nil {
		t.Fatalf("Load() after reauthentication error = %v", err)
	}
	replacementRef := config.Accounts[alias].Portal.SessionRef
	if replacementRef == "" || replacementRef == sessionRef {
		t.Fatalf("replacement session ref = %q, previous = %q", replacementRef, sessionRef)
	}
	if _, err := store.Load(profileID, sessionRef); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("old session Load() error = %v, want removed session", err)
	}

	status := executeCLI(t, profiles, executor,
		"--json",
		"--account", alias,
		"auth", "status",
	)
	if status.exitCode != cli.ExitOK {
		t.Fatalf("status exit = %d; stderr=%s", status.exitCode, status.stderr)
	}
	assertAuthState(t, status.stdout, "active")
	assertProviderLogin(t, status.stdout, providerLogin)

	humanStatus := executeCLI(t, profiles, executor,
		"--account", alias,
		"auth", "status",
	)
	if humanStatus.exitCode != cli.ExitOK {
		t.Fatalf("human status exit = %d; stderr=%s", humanStatus.exitCode, humanStatus.stderr)
	}
	if want := "Portal session for personal: active (REG.RU login: portal-login@example.test)\n"; humanStatus.stdout != want {
		t.Errorf("human status = %q, want %q", humanStatus.stdout, want)
	}

	plainStatus := executeCLI(t, profiles, executor,
		"--plain",
		"--account", alias,
		"auth", "status",
	)
	if plainStatus.exitCode != cli.ExitOK {
		t.Fatalf("plain status exit = %d; stderr=%s", plainStatus.exitCode, plainStatus.stderr)
	}
	if want := "personal\tactive\tprovider_login=portal-login@example.test\n"; plainStatus.stdout != want {
		t.Errorf("plain status = %q, want %q", plainStatus.stdout, want)
	}

	refresh := executeCLI(t, profiles, executor,
		"--json",
		"--force",
		"--account", alias,
		"auth", "refresh",
	)
	if refresh.exitCode != cli.ExitOK {
		t.Fatalf("refresh exit = %d; stderr=%s", refresh.exitCode, refresh.stderr)
	}
	assertAuthState(t, refresh.stdout, "active")
	assertProviderLogin(t, refresh.stdout, providerLogin)

	logout := executeCLI(t, profiles, executor,
		"--json",
		"--force",
		"--account", alias,
		"auth", "logout",
	)
	if logout.exitCode != cli.ExitOK {
		t.Fatalf("logout exit = %d; stderr=%s", logout.exitCode, logout.stderr)
	}
	assertAuthState(t, logout.stdout, "explicit-logout")
	config, err = profiles.Load()
	if err != nil {
		t.Fatalf("Load() after logout error = %v", err)
	}
	if got := config.Accounts[alias].Portal.SessionRef; got != "" {
		t.Errorf("session ref after logout = %q, want empty", got)
	}
}

func TestPortalSessionsAreIsolatedAcrossAccountProfiles(t *testing.T) {
	t.Parallel()

	const firstID = "p_bbbbbbbbbbbbbbbbbbbbbbbbbb"
	const secondID = "p_cccccccccccccccccccccccccc"
	profiles := profile.NewMemoryRepository(profile.Config{
		SchemaVersion: profile.SchemaVersion,
		Accounts: map[string]profile.Account{
			"first":  {ID: firstID, Provider: "reg.ru"},
			"second": {ID: secondID, Provider: "reg.ru"},
		},
	})
	store := session.NewFileStore(t.TempDir())
	broker := session.NewBroker(
		store,
		&lifecycleBrowserFactory{
			digest:        bytes.Repeat([]byte{0x31}, session.IdentityDigestBytes),
			providerLogin: "shared-login@example.test",
		},
		session.Options{LoginURL: "https://www.reg.ru/user/account/"},
	)
	executor := authcli.New(profiles, broker, nil)

	for _, alias := range []string{"first", "second"} {
		run := executeCLI(t, profiles, executor,
			"--json",
			"--force",
			"--account", alias,
			"auth", "login",
			"--login-timeout", "1m",
		)
		if run.exitCode != cli.ExitOK {
			t.Fatalf("%s login exit = %d; stderr=%s", alias, run.exitCode, run.stderr)
		}
	}

	config, err := profiles.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	firstRef := config.Accounts["first"].Portal.SessionRef
	secondRef := config.Accounts["second"].Portal.SessionRef
	if firstRef == "" || secondRef == "" || firstRef == secondRef {
		t.Fatalf("session refs are not isolated: %q, %q", firstRef, secondRef)
	}
	first, err := store.Load(firstID, firstRef)
	if err != nil {
		t.Fatalf("Load(first) error = %v", err)
	}
	second, err := store.Load(secondID, secondRef)
	if err != nil {
		t.Fatalf("Load(second) error = %v", err)
	}
	if first.ProfileDir == second.ProfileDir {
		t.Errorf("profile directories are shared: %q", first.ProfileDir)
	}
}

func TestAuthStatusEscapesProviderLoginInTextModes(t *testing.T) {
	t.Parallel()

	const alias = "work"
	const profileID = "p_dddddddddddddddddddddddddd"
	const providerLogin = "portal\\login\tline\nnext\r"
	profiles := profile.NewMemoryRepository(profile.Config{
		SchemaVersion: profile.SchemaVersion,
		Accounts: map[string]profile.Account{
			alias: {ID: profileID, Provider: "reg.ru"},
		},
	})
	broker := session.NewBroker(
		session.NewFileStore(t.TempDir()),
		&lifecycleBrowserFactory{
			digest:        bytes.Repeat([]byte{0x41}, session.IdentityDigestBytes),
			providerLogin: providerLogin,
		},
		session.Options{LoginURL: "https://www.reg.ru/user/account/"},
	)
	executor := authcli.New(profiles, broker, nil)
	login := executeCLI(t, profiles, executor,
		"--json", "--force", "--account", alias,
		"auth", "login", "--login-timeout", "1m",
	)
	if login.exitCode != cli.ExitOK {
		t.Fatalf("login exit = %d; stderr=%s", login.exitCode, login.stderr)
	}
	assertProviderLogin(t, login.stdout, providerLogin)

	human := executeCLI(t, profiles, executor, "--account", alias, "auth", "status")
	if want := "Portal session for work: active (REG.RU login: portal\\\\login\\tline\\nnext\\r)\n"; human.stdout != want {
		t.Errorf("human status = %q, want %q", human.stdout, want)
	}
	plain := executeCLI(t, profiles, executor, "--plain", "--account", alias, "auth", "status")
	if want := "work\tactive\tprovider_login=portal\\\\login\\tline\\nnext\\r\n"; plain.stdout != want {
		t.Errorf("plain status = %q, want %q", plain.stdout, want)
	}
}

type cliRun struct {
	exitCode int
	stdout   string
	stderr   string
}

func executeCLI(
	t *testing.T,
	profiles profile.Repository,
	executor cli.Executor,
	args ...string,
) cliRun {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := cli.Execute(context.Background(), args, cli.Options{
		In:          strings.NewReader(""),
		Out:         &stdout,
		ErrOut:      &stderr,
		Getenv:      func(string) string { return "" },
		InputIsTTY:  func() bool { return true },
		OutputIsTTY: func() bool { return false },
		ErrorIsTTY:  func() bool { return false },
		Profiles:    profiles,
		Executor:    executor,
	})
	return cliRun{exitCode: exitCode, stdout: stdout.String(), stderr: stderr.String()}
}

func assertAuthState(t *testing.T, output, expected string) {
	t.Helper()
	var envelope struct {
		Data struct {
			State string `json:"state"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatalf("decode output: %v\n%s", err, output)
	}
	if envelope.Data.State != expected {
		t.Errorf("state = %q, want %q", envelope.Data.State, expected)
	}
}

func assertCapability(t *testing.T, output, expected string) {
	t.Helper()
	var envelope struct {
		Data struct {
			Capabilities []string `json:"capabilities"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatalf("decode output: %v\n%s", err, output)
	}
	for _, capability := range envelope.Data.Capabilities {
		if capability == expected {
			return
		}
	}
	t.Errorf("capabilities = %v, want %q", envelope.Data.Capabilities, expected)
}

func assertProviderLogin(t *testing.T, output, expected string) {
	t.Helper()
	var envelope struct {
		Data struct {
			ProviderLogin string `json:"providerLogin"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatalf("decode output: %v\n%s", err, output)
	}
	if envelope.Data.ProviderLogin != expected {
		t.Errorf("provider login = %q, want %q", envelope.Data.ProviderLogin, expected)
	}
}

type lifecycleBrowserFactory struct {
	digest        []byte
	providerLogin string
}

func (f *lifecycleBrowserFactory) Open(
	context.Context,
	session.OpenSpec,
) (session.Browser, error) {
	return &lifecycleBrowser{digest: f.digest, providerLogin: f.providerLogin}, nil
}

type lifecycleBrowser struct {
	digest        []byte
	providerLogin string
}

func (b *lifecycleBrowser) WaitForAuthentication(
	context.Context,
	[]byte,
) (session.Observation, error) {
	return session.Observation{
		State:          session.ObservedAuthenticated,
		IdentityDigest: b.digest,
		ProviderLogin:  b.providerLogin,
	}, nil
}

func (b *lifecycleBrowser) Refresh(
	context.Context,
	[]byte,
) (session.Observation, error) {
	return session.Observation{
		State:          session.ObservedAuthenticated,
		IdentityDigest: b.digest,
		ProviderLogin:  b.providerLogin,
	}, nil
}

func (*lifecycleBrowser) Logout(
	context.Context,
	[]byte,
) (session.Observation, error) {
	return session.Observation{State: session.ObservedNoSession}, nil
}

func (*lifecycleBrowser) Executor() session.PageExecutor {
	return nil
}

func (*lifecycleBrowser) Close(context.Context) error {
	return nil
}
