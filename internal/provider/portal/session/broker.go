package session

import (
	"context"
	"errors"
	"time"
)

const (
	defaultStartupCap = 15 * time.Second
	defaultCleanupCap = 5 * time.Second
)

type Options struct {
	LoginURL     string
	StartupCap   time.Duration
	CleanupCap   time.Duration
	PollInterval time.Duration
}

type Broker struct {
	store   *FileStore
	factory BrowserFactory
	options Options
}

func NewBroker(
	store *FileStore,
	factory BrowserFactory,
	options Options,
) *Broker {
	if options.StartupCap <= 0 {
		options.StartupCap = defaultStartupCap
	}
	if options.CleanupCap <= 0 {
		options.CleanupCap = defaultCleanupCap
	}
	if options.PollInterval <= 0 {
		options.PollInterval = time.Second
	}
	return &Broker{store: store, factory: factory, options: options}
}

func (b *Broker) Forget(sessionRef string) error {
	if b == nil || b.store == nil {
		return sessionError(CodeState, errors.New("portal session broker is unavailable"))
	}
	if err := b.store.Delete(sessionRef); err != nil {
		return sessionError(CodeState, err)
	}
	return nil
}

func (b *Broker) Login(
	ctx context.Context,
	spec LoginSpec,
) (LoginResult, error) {
	if b == nil || b.store == nil || b.factory == nil {
		return LoginResult{}, sessionError(
			CodeState,
			errors.New("portal session broker is unavailable"),
		)
	}
	lock, err := b.store.LockProfile(spec.ProfileID)
	if errors.Is(err, errProfileBusy) {
		return LoginResult{}, sessionError(CodeProfileBusy, err)
	}
	if err != nil {
		return LoginResult{}, sessionError(CodeState, err)
	}
	defer lock.Release()

	staged, err := b.store.Stage(spec.ProfileID, spec.CurrentSessionRef)
	if err != nil {
		return LoginResult{}, sessionError(CodeState, err)
	}
	keepStaged := false
	defer func() {
		if !keepStaged {
			_ = b.store.Delete(staged.SessionRef)
		}
	}()

	browser, err := b.factory.Open(ctx, OpenSpec{
		SessionRef: staged.SessionRef,
		ProfileDir: staged.ProfileDir,
		Mode:       OpenStagedLogin,
		StartURL:   b.options.LoginURL,
		StartupCap: b.options.StartupCap,
		CleanupCap: b.options.CleanupCap,
	})
	if err != nil {
		return LoginResult{}, sessionError(CodeBrowser, err)
	}

	observation, waitErr := browser.WaitForAuthentication(ctx, staged.IdentityKey)
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), b.options.CleanupCap)
	closeErr := browser.Close(closeCtx)
	cancel()
	if waitErr != nil {
		return LoginResult{}, sessionError(CodeBrowser, waitErr)
	}
	if closeErr != nil {
		return LoginResult{}, sessionError(CodeBrowser, closeErr)
	}
	if err := validateObservation(observation); err != nil {
		return LoginResult{}, err
	}
	if len(staged.IdentityDigest) != 0 &&
		!equalDigest(staged.IdentityDigest, observation.IdentityDigest) {
		return LoginResult{}, sessionError(
			CodeAccountMismatch,
			errors.New("portal principal does not match the profile binding"),
		)
	}

	committed, err := b.store.Commit(staged, observation.IdentityDigest)
	if err != nil {
		return LoginResult{}, sessionError(CodeState, err)
	}
	keepStaged = true
	return LoginResult{
		SessionRef: committed.SessionRef,
		Status:     Status{State: StateActive},
	}, nil
}

func (b *Broker) Status(
	ctx context.Context,
	profile Profile,
) (Status, error) {
	if b == nil || b.store == nil || b.factory == nil {
		return Status{}, sessionError(
			CodeState,
			errors.New("portal session broker is unavailable"),
		)
	}
	lock, err := b.store.LockProfile(profile.ID)
	if errors.Is(err, errProfileBusy) {
		return Status{}, sessionError(CodeProfileBusy, err)
	}
	if err != nil {
		return Status{}, sessionError(CodeState, err)
	}
	defer lock.Release()

	record, err := b.store.Load(profile.ID, profile.SessionRef)
	if err != nil {
		return Status{}, sessionError(CodeState, err)
	}
	browser, err := b.factory.Open(ctx, OpenSpec{
		SessionRef: record.SessionRef,
		ProfileDir: record.ProfileDir,
		Mode:       OpenCommitted,
		StartURL:   b.options.LoginURL,
		StartupCap: b.options.StartupCap,
		CleanupCap: b.options.CleanupCap,
	})
	if err != nil {
		return Status{}, sessionError(CodeBrowser, err)
	}

	observation, refreshErr := browser.Refresh(ctx, record.IdentityKey)
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), b.options.CleanupCap)
	closeErr := browser.Close(closeCtx)
	cancel()
	if refreshErr != nil {
		_, _ = b.store.SetState(profile.ID, profile.SessionRef, StateUnknown)
		return Status{}, sessionError(CodeBrowser, refreshErr)
	}
	if closeErr != nil {
		_, _ = b.store.SetState(profile.ID, profile.SessionRef, StateUnknown)
		return Status{}, sessionError(CodeBrowser, closeErr)
	}

	switch observation.State {
	case ObservedNoSession:
		if _, err := b.store.SetState(
			profile.ID,
			profile.SessionRef,
			StateSessionLost,
		); err != nil {
			return Status{}, sessionError(CodeState, err)
		}
		return Status{
			State:  StateSessionLost,
			Reason: "session-lost",
		}, nil
	case ObservedAuthenticated:
		if err := validateObservation(observation); err != nil {
			return Status{}, err
		}
		if !equalDigest(record.IdentityDigest, observation.IdentityDigest) {
			return Status{}, sessionError(
				CodeAccountMismatch,
				errors.New("portal principal does not match the profile binding"),
			)
		}
		if _, err := b.store.SetState(
			profile.ID,
			profile.SessionRef,
			StateActive,
		); err != nil {
			return Status{}, sessionError(CodeState, err)
		}
		return Status{State: StateActive}, nil
	default:
		return Status{}, sessionError(
			CodeContractDrift,
			errors.New("provider contract changed"),
		)
	}
}

func (b *Broker) Logout(
	ctx context.Context,
	profile Profile,
) (Status, error) {
	if b == nil || b.store == nil || b.factory == nil {
		return Status{}, sessionError(
			CodeState,
			errors.New("portal session broker is unavailable"),
		)
	}
	lock, err := b.store.LockProfile(profile.ID)
	if errors.Is(err, errProfileBusy) {
		return Status{}, sessionError(CodeProfileBusy, err)
	}
	if err != nil {
		return Status{}, sessionError(CodeState, err)
	}
	defer lock.Release()

	record, err := b.store.Load(profile.ID, profile.SessionRef)
	if err != nil {
		return Status{}, sessionError(CodeState, err)
	}
	browser, err := b.factory.Open(ctx, OpenSpec{
		SessionRef: record.SessionRef,
		ProfileDir: record.ProfileDir,
		Mode:       OpenCommitted,
		StartURL:   b.options.LoginURL,
		StartupCap: b.options.StartupCap,
		CleanupCap: b.options.CleanupCap,
	})
	if err != nil {
		return Status{}, sessionError(CodeBrowser, err)
	}

	observation, logoutErr := browser.Logout(ctx, record.IdentityKey)
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), b.options.CleanupCap)
	closeErr := browser.Close(closeCtx)
	cancel()
	if logoutErr != nil || closeErr != nil {
		return Status{}, sessionError(
			CodeLogoutUnknown,
			errors.Join(logoutErr, closeErr),
		)
	}
	if observation.State != ObservedNoSession {
		return Status{}, sessionError(
			CodeLogoutUnknown,
			errors.New("provider logout was not confirmed"),
		)
	}
	if err := b.store.Delete(profile.SessionRef); err != nil {
		return Status{}, sessionError(CodeState, err)
	}
	return Status{
		State:  StateExplicitLogout,
		Reason: "explicit-logout",
	}, nil
}

func (b *Broker) WithSession(
	ctx context.Context,
	profile Profile,
	use func(PageExecutor) error,
) error {
	if b == nil || b.store == nil || b.factory == nil {
		return sessionError(CodeState, errors.New("portal session broker is unavailable"))
	}
	if use == nil {
		return sessionError(CodeState, errors.New("portal session consumer is missing"))
	}
	lock, err := b.store.LockProfile(profile.ID)
	if errors.Is(err, errProfileBusy) {
		return sessionError(CodeProfileBusy, err)
	}
	if err != nil {
		return sessionError(CodeState, err)
	}
	defer lock.Release()

	record, err := b.store.Load(profile.ID, profile.SessionRef)
	if err != nil {
		return sessionError(CodeState, err)
	}
	browser, err := b.factory.Open(ctx, OpenSpec{
		SessionRef: record.SessionRef,
		ProfileDir: record.ProfileDir,
		Mode:       OpenCommitted,
		StartURL:   b.options.LoginURL,
		StartupCap: b.options.StartupCap,
		CleanupCap: b.options.CleanupCap,
	})
	if err != nil {
		return sessionError(CodeBrowser, err)
	}

	observation, refreshErr := browser.Refresh(ctx, record.IdentityKey)
	if refreshErr != nil {
		_, _ = b.store.SetState(profile.ID, profile.SessionRef, StateUnknown)
		return b.closeAfter(
			ctx,
			browser,
			sessionError(CodeBrowser, refreshErr),
		)
	}
	switch observation.State {
	case ObservedNoSession:
		_, _ = b.store.SetState(profile.ID, profile.SessionRef, StateSessionLost)
		return b.closeAfter(
			ctx,
			browser,
			sessionError(CodeSessionLost, errors.New("provider session is lost")),
		)
	case ObservedAuthenticated:
		if err := validateObservation(observation); err != nil {
			return b.closeAfter(ctx, browser, err)
		}
		if !equalDigest(record.IdentityDigest, observation.IdentityDigest) {
			return b.closeAfter(
				ctx,
				browser,
				sessionError(
					CodeAccountMismatch,
					errors.New("portal principal does not match the profile binding"),
				),
			)
		}
	default:
		return b.closeAfter(
			ctx,
			browser,
			sessionError(CodeContractDrift, errors.New("provider contract changed")),
		)
	}

	page := browser.Executor()
	if page == nil {
		return b.closeAfter(
			ctx,
			browser,
			sessionError(CodeContractDrift, errors.New("page executor is unavailable")),
		)
	}
	useErr := use(page)
	return b.closeAfter(ctx, browser, useErr)
}

// Handoff opens the committed profile visibly, verifies its bound principal,
// and transfers the browser to a human workflow. A successful handoff is not
// closed by the broker: the visible browser becomes user-owned for the rest of
// its lifetime.
func (b *Broker) Handoff(
	ctx context.Context,
	profile Profile,
	use func(PageExecutor) error,
) error {
	if b == nil || b.store == nil || b.factory == nil {
		return sessionError(CodeState, errors.New("portal session broker is unavailable"))
	}
	if use == nil {
		return sessionError(CodeState, errors.New("portal session consumer is missing"))
	}
	lock, err := b.store.LockProfile(profile.ID)
	if errors.Is(err, errProfileBusy) {
		return sessionError(CodeProfileBusy, err)
	}
	if err != nil {
		return sessionError(CodeState, err)
	}
	defer lock.Release()

	record, err := b.store.Load(profile.ID, profile.SessionRef)
	if err != nil {
		return sessionError(CodeState, err)
	}
	browser, err := b.factory.Open(ctx, OpenSpec{
		SessionRef: record.SessionRef,
		ProfileDir: record.ProfileDir,
		Mode:       OpenHandoff,
		StartURL:   b.options.LoginURL,
		StartupCap: b.options.StartupCap,
		CleanupCap: b.options.CleanupCap,
	})
	if err != nil {
		return sessionError(CodeBrowser, err)
	}

	observation, refreshErr := browser.Refresh(ctx, record.IdentityKey)
	if refreshErr != nil {
		_, _ = b.store.SetState(profile.ID, profile.SessionRef, StateUnknown)
		return b.closeAfter(ctx, browser, sessionError(CodeBrowser, refreshErr))
	}
	switch observation.State {
	case ObservedNoSession:
		_, _ = b.store.SetState(profile.ID, profile.SessionRef, StateSessionLost)
		return b.closeAfter(ctx, browser, sessionError(CodeSessionLost, errors.New("provider session is lost")))
	case ObservedAuthenticated:
		if err := validateObservation(observation); err != nil {
			return b.closeAfter(ctx, browser, err)
		}
		if !equalDigest(record.IdentityDigest, observation.IdentityDigest) {
			return b.closeAfter(ctx, browser, sessionError(
				CodeAccountMismatch,
				errors.New("portal principal does not match the profile binding"),
			))
		}
	default:
		return b.closeAfter(ctx, browser, sessionError(CodeContractDrift, errors.New("provider contract changed")))
	}

	page := browser.Executor()
	if page == nil {
		return b.closeAfter(ctx, browser, sessionError(CodeContractDrift, errors.New("page executor is unavailable")))
	}
	if err := use(page); err != nil {
		return b.closeAfter(ctx, browser, err)
	}
	return nil
}

func (b *Broker) closeAfter(
	ctx context.Context,
	browser Browser,
	operationErr error,
) error {
	closeCtx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		b.options.CleanupCap,
	)
	closeErr := browser.Close(closeCtx)
	cancel()
	if closeErr != nil {
		return sessionError(CodeBrowser, errors.Join(operationErr, closeErr))
	}
	return operationErr
}
