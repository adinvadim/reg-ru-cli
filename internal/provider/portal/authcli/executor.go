package authcli

import (
	"context"
	"errors"
	"fmt"

	"github.com/adinvadim/reg-ru-cli/internal/cli"
	"github.com/adinvadim/reg-ru-cli/internal/profile"
	"github.com/adinvadim/reg-ru-cli/internal/provider/portal/cdp"
	"github.com/adinvadim/reg-ru-cli/internal/provider/portal/session"
)

type sessionBroker interface {
	Login(context.Context, session.LoginSpec) (session.LoginResult, error)
	Status(context.Context, session.Profile) (session.Status, error)
	Logout(context.Context, session.Profile) (session.Status, error)
	Forget(string) error
}

type Executor struct {
	profiles profile.Repository
	broker   sessionBroker
	fallback cli.Executor
}

func New(
	profiles profile.Repository,
	broker sessionBroker,
	fallback cli.Executor,
) *Executor {
	if fallback == nil {
		fallback = cli.UnavailableExecutor{}
	}
	return &Executor{
		profiles: profiles,
		broker:   broker,
		fallback: fallback,
	}
}

func (e *Executor) Execute(
	ctx context.Context,
	operation cli.Operation,
) (cli.Result, error) {
	switch operation.Action {
	case "auth.login":
		return e.login(ctx, operation)
	case "auth.status", "auth.refresh":
		return e.status(ctx, operation)
	case "auth.logout":
		return e.logout(ctx, operation)
	default:
		return e.fallback.Execute(ctx, operation)
	}
}

func (e *Executor) login(
	ctx context.Context,
	operation cli.Operation,
) (cli.Result, error) {
	account, err := e.account(operation)
	if err != nil {
		return cli.Result{}, err
	}
	currentRef := account.Portal.SessionRef
	if currentRef != "" && !operation.Force {
		status, err := e.broker.Status(ctx, session.Profile{
			ID:         account.ID,
			SessionRef: currentRef,
		})
		if err != nil {
			return cli.Result{}, translate(operation, err)
		}
		if status.State == session.StateActive {
			return renderStatus(operation.Account, status), nil
		}
	}

	login, err := e.broker.Login(ctx, session.LoginSpec{
		ProfileID:         account.ID,
		CurrentSessionRef: currentRef,
		Force:             operation.Force,
	})
	if err != nil {
		return cli.Result{}, translate(operation, err)
	}
	if err := e.profiles.SetPortalSession(operation.Account, login.SessionRef); err != nil {
		_ = e.broker.Forget(login.SessionRef)
		return cli.Result{}, cli.ConfigurationError(
			"could not commit the portal session to the selected account profile",
		)
	}
	if currentRef != "" && currentRef != login.SessionRef {
		_ = e.broker.Forget(currentRef)
	}
	return renderStatus(operation.Account, login.Status), nil
}

func (e *Executor) status(
	ctx context.Context,
	operation cli.Operation,
) (cli.Result, error) {
	account, err := e.account(operation)
	if err != nil {
		return cli.Result{}, err
	}
	if account.Portal.SessionRef == "" {
		return renderStatus(operation.Account, session.Status{
			State:  session.StateNotEstablished,
			Reason: "not-established",
		}), nil
	}
	status, err := e.broker.Status(ctx, session.Profile{
		ID:         account.ID,
		SessionRef: account.Portal.SessionRef,
	})
	if err != nil {
		return cli.Result{}, translate(operation, err)
	}
	return renderStatus(operation.Account, status), nil
}

func (e *Executor) logout(
	ctx context.Context,
	operation cli.Operation,
) (cli.Result, error) {
	account, err := e.account(operation)
	if err != nil {
		return cli.Result{}, err
	}
	if account.Portal.SessionRef == "" {
		return renderStatus(operation.Account, session.Status{
			State:  session.StateNotEstablished,
			Reason: "not-established",
		}), nil
	}
	status, err := e.broker.Logout(ctx, session.Profile{
		ID:         account.ID,
		SessionRef: account.Portal.SessionRef,
	})
	if err != nil {
		return cli.Result{}, translate(operation, err)
	}
	if err := e.profiles.SetPortalSession(operation.Account, ""); err != nil {
		return cli.Result{}, cli.ConfigurationError(
			"provider logout completed but the local account profile could not be updated",
		)
	}
	return renderStatus(operation.Account, status), nil
}

func (e *Executor) account(operation cli.Operation) (profile.Account, error) {
	if e == nil || e.profiles == nil || e.broker == nil {
		return profile.Account{}, cli.ConfigurationError(
			"portal authentication is not configured",
		)
	}
	config, err := e.profiles.Load()
	if err != nil {
		return profile.Account{}, cli.ConfigurationError(
			"profile configuration is invalid",
		)
	}
	account, exists := config.Accounts[operation.Account]
	if !exists || account.ID != operation.ProfileID {
		return profile.Account{}, cli.AccountNotFound(operation.Account)
	}
	return account, nil
}

func renderStatus(account string, status session.Status) cli.Result {
	plain := fmt.Sprintf("%s\t%s", account, status.State)
	if status.Reason != "" {
		plain += "\t" + status.Reason
	}
	capabilities := []string{}
	if status.State == session.StateActive {
		capabilities = append(capabilities, "auth.browser_session")
	}
	return cli.Result{
		Human: fmt.Sprintf("Portal session for %s: %s", account, status.State),
		Plain: []string{plain},
		Data: map[string]any{
			"account":      account,
			"state":        status.State,
			"reason":       status.Reason,
			"capabilities": capabilities,
		},
		Warnings: []cli.Warning{{
			Code:    "experimental_private_portal",
			Message: "browser-backed REG.RU portal integration is experimental",
		}},
	}
}

func translate(operation cli.Operation, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	switch {
	case session.IsCode(err, session.CodeAccountMismatch):
		return cli.AccountMismatch(operation.Account, "")
	case session.IsCode(err, session.CodeProfileBusy):
		return cli.PortalProfileBusy()
	case session.IsCode(err, session.CodeContractDrift):
		return cli.PrivateContractDrift(operation.Capability)
	case session.IsCode(err, session.CodeSessionLost),
		session.IsCode(err, session.CodeNotEstablished):
		return cli.AuthenticationExpired()
	case session.IsCode(err, session.CodeLogoutUnknown):
		return cli.OutcomeUnknown(operation.Capability)
	case session.IsCode(err, session.CodeBrowser):
		if errors.Is(err, cdp.ErrBrowserNotFound) {
			return cli.MissingBrowser()
		}
		if operation.Action == "auth.login" {
			return cli.LoginCancelled()
		}
		return cli.BrowserSessionInterrupted()
	default:
		return cli.ConfigurationError("the local portal session state is invalid")
	}
}
