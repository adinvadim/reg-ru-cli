package session_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adinvadim/reg-ru-cli/internal/provider/portal/session"
)

const fixtureProviderLogin = "portal-login@example.test"

func TestLoginCommitsAnIsolatedSessionWithoutPersistingRawIdentity(t *testing.T) {
	t.Parallel()

	expectedDigest := bytes.Repeat([]byte{0x2a}, session.IdentityDigestBytes)
	store := session.NewFileStore(t.TempDir())
	browser := &fakeBrowser{
		waitObservation: session.Observation{
			State:          session.ObservedAuthenticated,
			IdentityDigest: expectedDigest,
			ProviderLogin:  fixtureProviderLogin,
		},
	}
	broker := session.NewBroker(store, &fakeBrowserFactory{browser: browser}, session.Options{
		LoginURL:     "https://www.reg.ru/user/account/",
		PollInterval: time.Millisecond,
	})

	result, err := broker.Login(context.Background(), session.LoginSpec{
		ProfileID: "p_aaaaaaaaaaaaaaaaaaaaaaaaaa",
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if result.Status.State != session.StateActive {
		t.Fatalf("state = %q, want %q", result.Status.State, session.StateActive)
	}
	if result.Status.ProviderLogin != fixtureProviderLogin {
		t.Errorf("provider login = %q, want %q", result.Status.ProviderLogin, fixtureProviderLogin)
	}
	if result.SessionRef == "" {
		t.Fatal("session ref is empty")
	}
	if !browser.closed {
		t.Fatal("headed browser was not closed after commit")
	}

	record, err := store.Load(
		"p_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		result.SessionRef,
	)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if record.State != session.StateActive {
		t.Errorf("stored state = %q, want %q", record.State, session.StateActive)
	}
	if !bytes.Equal(record.IdentityDigest, expectedDigest) {
		t.Error("stored identity digest does not match the reduced browser result")
	}

	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("json.Marshal(record): %v", err)
	}
	for _, forbidden := range []string{fixtureProviderLogin, "user@example.test", "password", "cookie"} {
		if bytes.Contains(bytes.ToLower(encoded), []byte(forbidden)) {
			t.Errorf("stored record contains forbidden identity/session material %q", forbidden)
		}
	}
}

func TestLoginRejectsAuthenticatedObservationWithoutProviderLogin(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	broker := session.NewBroker(
		session.NewFileStore(root),
		&fakeBrowserFactory{browser: &fakeBrowser{
			waitObservation: session.Observation{
				State:          session.ObservedAuthenticated,
				IdentityDigest: bytes.Repeat([]byte{0x2b}, session.IdentityDigestBytes),
			},
		}},
		session.Options{LoginURL: "https://www.reg.ru/user/account/"},
	)

	_, err := broker.Login(context.Background(), session.LoginSpec{
		ProfileID: "p_aaaaaaaaaaaaaaaaaaaaaaaaab",
	})
	if !session.IsCode(err, session.CodeContractDrift) {
		t.Fatalf("Login() error = %v, want contract drift", err)
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil {
		t.Fatalf("ReadDir() error = %v", readErr)
	}
	if sessionDirectories := entryNames(entries); len(sessionDirectories) != 0 {
		t.Errorf("staged session directories remain: %v", sessionDirectories)
	}
}

func TestLoginRejectsAnotherPrincipalAndPreservesCommittedSession(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := session.NewFileStore(root)
	firstDigest := bytes.Repeat([]byte{0x11}, session.IdentityDigestBytes)
	firstBroker := session.NewBroker(
		store,
		&fakeBrowserFactory{browser: &fakeBrowser{
			waitObservation: session.Observation{
				State:          session.ObservedAuthenticated,
				IdentityDigest: firstDigest,
				ProviderLogin:  fixtureProviderLogin,
			},
		}},
		session.Options{LoginURL: "https://www.reg.ru/user/account/"},
	)
	first, err := firstBroker.Login(context.Background(), session.LoginSpec{
		ProfileID: "p_bbbbbbbbbbbbbbbbbbbbbbbbbb",
	})
	if err != nil {
		t.Fatalf("first Login() error = %v", err)
	}

	secondBroker := session.NewBroker(
		store,
		&fakeBrowserFactory{browser: &fakeBrowser{
			waitObservation: session.Observation{
				State:          session.ObservedAuthenticated,
				IdentityDigest: bytes.Repeat([]byte{0x22}, session.IdentityDigestBytes),
				ProviderLogin:  "another-login@example.test",
			},
		}},
		session.Options{LoginURL: "https://www.reg.ru/user/account/"},
	)
	_, err = secondBroker.Login(context.Background(), session.LoginSpec{
		ProfileID:         "p_bbbbbbbbbbbbbbbbbbbbbbbbbb",
		CurrentSessionRef: first.SessionRef,
		Force:             true,
	})
	if !session.IsCode(err, session.CodeAccountMismatch) {
		t.Fatalf("second Login() error = %v, want account mismatch", err)
	}
	if strings.Contains(err.Error(), "another-login@example.test") {
		t.Fatal("account mismatch error exposed the observed provider login")
	}

	record, err := store.Load(
		"p_bbbbbbbbbbbbbbbbbbbbbbbbbb",
		first.SessionRef,
	)
	if err != nil {
		t.Fatalf("Load(committed) error = %v", err)
	}
	if record.State != session.StateActive ||
		!bytes.Equal(record.IdentityDigest, firstDigest) {
		t.Errorf("committed session changed after mismatch: %+v", record)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	sessionDirectories := entryNames(entries)
	if len(sessionDirectories) != 1 || sessionDirectories[0] != first.SessionRef {
		t.Errorf("session directories = %v, want only %q", sessionDirectories, first.SessionRef)
	}
}

func TestLoginTimeoutDiscardsStagedBrowserProfile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := session.NewFileStore(root)
	broker := session.NewBroker(
		store,
		&fakeBrowserFactory{browser: &fakeBrowser{waitForContext: true}},
		session.Options{LoginURL: "https://www.reg.ru/user/account/"},
	)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := broker.Login(ctx, session.LoginSpec{
		ProfileID: "p_33333333333333333333333333",
	})
	if !session.IsCode(err, session.CodeBrowser) ||
		!errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Login() error = %v, want browser deadline", err)
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil {
		t.Fatalf("ReadDir() error = %v", readErr)
	}
	if sessionDirectories := entryNames(entries); len(sessionDirectories) != 0 {
		t.Errorf("staged session directories remain: %v", sessionDirectories)
	}
}

func TestStatusReportsPreviouslyActiveSessionAsLost(t *testing.T) {
	t.Parallel()

	store := session.NewFileStore(t.TempDir())
	digest := bytes.Repeat([]byte{0x33}, session.IdentityDigestBytes)
	loginBroker := session.NewBroker(
		store,
		&fakeBrowserFactory{browser: &fakeBrowser{
			waitObservation: session.Observation{
				State:          session.ObservedAuthenticated,
				IdentityDigest: digest,
				ProviderLogin:  fixtureProviderLogin,
			},
		}},
		session.Options{LoginURL: "https://www.reg.ru/user/account/"},
	)
	login, err := loginBroker.Login(context.Background(), session.LoginSpec{
		ProfileID: "p_cccccccccccccccccccccccccc",
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	statusBrowser := &fakeBrowser{
		refreshObservation: session.Observation{State: session.ObservedNoSession},
	}
	statusBroker := session.NewBroker(
		store,
		&fakeBrowserFactory{browser: statusBrowser},
		session.Options{},
	)
	status, err := statusBroker.Status(context.Background(), session.Profile{
		ID:         "p_cccccccccccccccccccccccccc",
		SessionRef: login.SessionRef,
	})
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.State != session.StateSessionLost || status.Reason != "session-lost" {
		t.Errorf("status = %+v, want session-lost", status)
	}
	if status.ProviderLogin != "" {
		t.Errorf("lost-session provider login = %q, want empty", status.ProviderLogin)
	}
	if !statusBrowser.closed {
		t.Error("browser was not closed after status probe")
	}

	record, err := store.Load(
		"p_cccccccccccccccccccccccccc",
		login.SessionRef,
	)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if record.State != session.StateSessionLost {
		t.Errorf("stored state = %q, want %q", record.State, session.StateSessionLost)
	}
}

func TestConcurrentUseOfOneProfileFailsBeforeOpeningAnotherBrowser(t *testing.T) {
	t.Parallel()

	store := session.NewFileStore(t.TempDir())
	digest := bytes.Repeat([]byte{0x44}, session.IdentityDigestBytes)
	loginBroker := session.NewBroker(
		store,
		&fakeBrowserFactory{browser: &fakeBrowser{
			waitObservation: session.Observation{
				State:          session.ObservedAuthenticated,
				IdentityDigest: digest,
				ProviderLogin:  fixtureProviderLogin,
			},
		}},
		session.Options{LoginURL: "https://www.reg.ru/user/account/"},
	)
	login, err := loginBroker.Login(context.Background(), session.LoginSpec{
		ProfileID: "p_dddddddddddddddddddddddddd",
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	factory := &concurrentBrowserFactory{
		digest:  digest,
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	broker := session.NewBroker(store, factory, session.Options{})
	profile := session.Profile{
		ID:         "p_dddddddddddddddddddddddddd",
		SessionRef: login.SessionRef,
	}
	firstDone := make(chan error, 1)
	go func() {
		_, firstErr := broker.Status(context.Background(), profile)
		firstDone <- firstErr
	}()
	<-factory.started

	_, secondErr := broker.Status(context.Background(), profile)
	if !session.IsCode(secondErr, session.CodeProfileBusy) {
		t.Fatalf("second Status() error = %v, want profile busy", secondErr)
	}
	if calls := factory.opens.Load(); calls != 1 {
		t.Errorf("browser opens = %d, want 1", calls)
	}

	close(factory.release)
	if firstErr := <-firstDone; firstErr != nil {
		t.Fatalf("first Status() error = %v", firstErr)
	}
}

func TestLogoutDeletesLocalProfileOnlyAfterProviderConfirmsNoSession(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := session.NewFileStore(root)
	digest := bytes.Repeat([]byte{0x55}, session.IdentityDigestBytes)
	loginBroker := session.NewBroker(
		store,
		&fakeBrowserFactory{browser: &fakeBrowser{
			waitObservation: session.Observation{
				State:          session.ObservedAuthenticated,
				IdentityDigest: digest,
				ProviderLogin:  fixtureProviderLogin,
			},
		}},
		session.Options{LoginURL: "https://www.reg.ru/user/account/"},
	)
	login, err := loginBroker.Login(context.Background(), session.LoginSpec{
		ProfileID: "p_eeeeeeeeeeeeeeeeeeeeeeeeee",
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	logoutBrowser := &fakeBrowser{
		logoutObservation: session.Observation{State: session.ObservedNoSession},
	}
	broker := session.NewBroker(
		store,
		&fakeBrowserFactory{browser: logoutBrowser},
		session.Options{},
	)
	status, err := broker.Logout(context.Background(), session.Profile{
		ID:         "p_eeeeeeeeeeeeeeeeeeeeeeeeee",
		SessionRef: login.SessionRef,
	})
	if err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if status.State != session.StateExplicitLogout {
		t.Errorf("status = %+v, want explicit logout", status)
	}
	if _, err := store.Load(
		"p_eeeeeeeeeeeeeeeeeeeeeeeeee",
		login.SessionRef,
	); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Load() error = %v, want removed session", err)
	}
}

func TestLogoutPreservesLocalProfileWhenProviderOutcomeIsUnknown(t *testing.T) {
	t.Parallel()

	store := session.NewFileStore(t.TempDir())
	digest := bytes.Repeat([]byte{0x56}, session.IdentityDigestBytes)
	loginBroker := session.NewBroker(
		store,
		&fakeBrowserFactory{browser: &fakeBrowser{
			waitObservation: session.Observation{
				State:          session.ObservedAuthenticated,
				IdentityDigest: digest,
				ProviderLogin:  fixtureProviderLogin,
			},
		}},
		session.Options{LoginURL: "https://www.reg.ru/user/account/"},
	)
	login, err := loginBroker.Login(context.Background(), session.LoginSpec{
		ProfileID: "p_22222222222222222222222222",
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	logoutBroker := session.NewBroker(
		store,
		&fakeBrowserFactory{browser: &fakeBrowser{
			logoutErr: errors.New("connection closed after request"),
		}},
		session.Options{},
	)
	_, err = logoutBroker.Logout(context.Background(), session.Profile{
		ID:         "p_22222222222222222222222222",
		SessionRef: login.SessionRef,
	})
	if !session.IsCode(err, session.CodeLogoutUnknown) {
		t.Fatalf("Logout() error = %v, want unknown outcome", err)
	}
	if _, err := store.Load(
		"p_22222222222222222222222222",
		login.SessionRef,
	); err != nil {
		t.Fatalf("Load() after ambiguous logout error = %v", err)
	}
}

func TestWithSessionRunsTypedProgramAfterIdentityCheckedRefresh(t *testing.T) {
	t.Parallel()

	store := session.NewFileStore(t.TempDir())
	digest := bytes.Repeat([]byte{0x66}, session.IdentityDigestBytes)
	loginBroker := session.NewBroker(
		store,
		&fakeBrowserFactory{browser: &fakeBrowser{
			waitObservation: session.Observation{
				State:          session.ObservedAuthenticated,
				IdentityDigest: digest,
				ProviderLogin:  fixtureProviderLogin,
			},
		}},
		session.Options{LoginURL: "https://www.reg.ru/user/account/"},
	)
	login, err := loginBroker.Login(context.Background(), session.LoginSpec{
		ProfileID: "p_ffffffffffffffffffffffffff",
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	executor := &fixtureExecutor{
		result: json.RawMessage(`{"balance":{"currency":"RUB","amount":"10.00"}}`),
	}
	browser := &fakeBrowser{
		refreshObservation: session.Observation{
			State:          session.ObservedAuthenticated,
			IdentityDigest: digest,
			ProviderLogin:  fixtureProviderLogin,
		},
		executor: executor,
	}
	broker := session.NewBroker(
		store,
		&fakeBrowserFactory{browser: browser},
		session.Options{},
	)
	var result json.RawMessage
	err = broker.WithSession(
		context.Background(),
		session.Profile{
			ID:         "p_ffffffffffffffffffffffffff",
			SessionRef: login.SessionRef,
		},
		func(page session.PageExecutor) error {
			return page.RunJSON(
				context.Background(),
				session.ProgramID("fixture.billing.balance"),
				json.RawMessage(`{"account":"selected"}`),
				&result,
			)
		},
	)
	if err != nil {
		t.Fatalf("WithSession() error = %v", err)
	}
	if string(result) != `{"balance":{"currency":"RUB","amount":"10.00"}}` {
		t.Errorf("result = %s", result)
	}
	if executor.program != "fixture.billing.balance" {
		t.Errorf("program = %q", executor.program)
	}
	if !browser.closed {
		t.Error("browser was not closed after typed program")
	}
}

func TestHandoffKeepsVerifiedVisibleBrowserOpenAfterProgramDispatch(t *testing.T) {
	t.Parallel()

	store := session.NewFileStore(t.TempDir())
	digest := bytes.Repeat([]byte{0x77}, session.IdentityDigestBytes)
	loginBroker := session.NewBroker(store, &fakeBrowserFactory{browser: &fakeBrowser{
		waitObservation: session.Observation{State: session.ObservedAuthenticated, IdentityDigest: digest, ProviderLogin: fixtureProviderLogin},
	}}, session.Options{LoginURL: "https://www.reg.ru/user/account/"})
	login, err := loginBroker.Login(context.Background(), session.LoginSpec{
		ProfileID: "p_77777777777777777777777777",
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	executor := &fixtureExecutor{result: json.RawMessage(`{"state":"browser-opened"}`)}
	browser := &fakeBrowser{
		refreshObservation: session.Observation{State: session.ObservedAuthenticated, IdentityDigest: digest, ProviderLogin: fixtureProviderLogin},
		executor:           executor,
	}
	factory := &fakeBrowserFactory{browser: browser}
	broker := session.NewBroker(store, factory, session.Options{LoginURL: "https://www.reg.ru/user/account/"})
	err = broker.Handoff(context.Background(), session.Profile{
		ID: "p_77777777777777777777777777", SessionRef: login.SessionRef,
	}, func(page session.PageExecutor) error {
		var result json.RawMessage
		return page.RunJSON(context.Background(), "portal.billing.checkout", json.RawMessage(`{"invoiceId":"42"}`), &result)
	})
	if err != nil {
		t.Fatalf("Handoff() error = %v", err)
	}
	if factory.spec.Mode != session.OpenHandoff {
		t.Errorf("open mode = %q, want %q", factory.spec.Mode, session.OpenHandoff)
	}
	if browser.closed {
		t.Fatal("successful handoff closed the visible browser")
	}
	if executor.program != "portal.billing.checkout" {
		t.Errorf("program = %q", executor.program)
	}

	dispatchErr := errors.New("checkout dispatch was not confirmed")
	failedBrowser := &fakeBrowser{
		refreshObservation: session.Observation{State: session.ObservedAuthenticated, IdentityDigest: digest, ProviderLogin: fixtureProviderLogin},
		executor:           executor,
	}
	failedBroker := session.NewBroker(store, &fakeBrowserFactory{browser: failedBrowser}, session.Options{})
	err = failedBroker.Handoff(context.Background(), session.Profile{
		ID: "p_77777777777777777777777777", SessionRef: login.SessionRef,
	}, func(session.PageExecutor) error { return dispatchErr })
	if !errors.Is(err, dispatchErr) {
		t.Fatalf("failed Handoff() error = %v, want dispatch error", err)
	}
	if !failedBrowser.closed {
		t.Fatal("ambiguous handoff left the visible browser open")
	}
}

func entryNames(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if len(entry.Name()) > 2 && entry.Name()[:2] == "s_" {
			names = append(names, entry.Name())
		}
	}
	return names
}

type fakeBrowserFactory struct {
	browser *fakeBrowser
	spec    session.OpenSpec
}

func (f *fakeBrowserFactory) Open(
	_ context.Context,
	spec session.OpenSpec,
) (session.Browser, error) {
	f.spec = spec
	return f.browser, nil
}

type fakeBrowser struct {
	waitObservation    session.Observation
	refreshObservation session.Observation
	logoutObservation  session.Observation
	logoutErr          error
	executor           session.PageExecutor
	closed             bool
	waitForContext     bool
}

func (b *fakeBrowser) WaitForAuthentication(
	ctx context.Context,
	_ []byte,
) (session.Observation, error) {
	if b.waitForContext {
		<-ctx.Done()
		return session.Observation{}, ctx.Err()
	}
	return b.waitObservation, nil
}

func (b *fakeBrowser) Refresh(
	_ context.Context,
	_ []byte,
) (session.Observation, error) {
	return b.refreshObservation, nil
}

func (b *fakeBrowser) Logout(
	_ context.Context,
	_ []byte,
) (session.Observation, error) {
	return b.logoutObservation, b.logoutErr
}

func (b *fakeBrowser) Executor() session.PageExecutor {
	return b.executor
}

func (b *fakeBrowser) Close(context.Context) error {
	b.closed = true
	return nil
}

type concurrentBrowserFactory struct {
	digest  []byte
	started chan struct{}
	release chan struct{}
	opens   atomic.Int32
}

func (f *concurrentBrowserFactory) Open(
	_ context.Context,
	_ session.OpenSpec,
) (session.Browser, error) {
	open := f.opens.Add(1)
	if open == 1 {
		return &blockingBrowser{
			digest:  f.digest,
			started: f.started,
			release: f.release,
		}, nil
	}
	return &fakeBrowser{
		refreshObservation: session.Observation{
			State:          session.ObservedAuthenticated,
			IdentityDigest: f.digest,
			ProviderLogin:  fixtureProviderLogin,
		},
	}, nil
}

type blockingBrowser struct {
	digest  []byte
	started chan struct{}
	release chan struct{}
}

func (*blockingBrowser) WaitForAuthentication(
	context.Context,
	[]byte,
) (session.Observation, error) {
	return session.Observation{}, nil
}

func (b *blockingBrowser) Refresh(
	context.Context,
	[]byte,
) (session.Observation, error) {
	close(b.started)
	<-b.release
	return session.Observation{
		State:          session.ObservedAuthenticated,
		IdentityDigest: b.digest,
		ProviderLogin:  fixtureProviderLogin,
	}, nil
}

func (*blockingBrowser) Logout(
	context.Context,
	[]byte,
) (session.Observation, error) {
	return session.Observation{}, nil
}

func (*blockingBrowser) Executor() session.PageExecutor {
	return nil
}

func (*blockingBrowser) Close(context.Context) error {
	return nil
}

type fixtureExecutor struct {
	program session.ProgramID
	result  json.RawMessage
}

func (e *fixtureExecutor) RunJSON(
	_ context.Context,
	program session.ProgramID,
	_ json.RawMessage,
	result *json.RawMessage,
) error {
	e.program = program
	*result = append((*result)[:0], e.result...)
	return nil
}
