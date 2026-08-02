package authcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/adinvadim/reg-ru-cli/internal/cli"
	"github.com/adinvadim/reg-ru-cli/internal/profile"
	"github.com/adinvadim/reg-ru-cli/internal/provider/portal/cdp"
	"github.com/adinvadim/reg-ru-cli/internal/provider/portal/session"
)

type sessionBroker interface {
	Login(context.Context, session.LoginSpec) (session.LoginResult, error)
	Status(context.Context, session.Profile) (session.Status, error)
	WithSession(
		context.Context,
		session.Profile,
		func(session.PageExecutor) error,
	) error
	Logout(context.Context, session.Profile) (session.Status, error)
	Forget(string) error
}

const regAPIIPSyncProgram session.ProgramID = "portal.auth.regapi-ip-sync"

const regAPISettingsURL = "https://www.reg.ru/user/account/settings/api/"

type Executor struct {
	profiles          profile.Repository
	broker            sessionBroker
	fallback          cli.Executor
	resolveEgressIPv4 func(context.Context) (string, error)
}

type Options struct {
	ResolveEgressIPv4 func(context.Context) (string, error)
}

func New(
	profiles profile.Repository,
	broker sessionBroker,
	fallback cli.Executor,
	options ...Options,
) *Executor {
	if fallback == nil {
		fallback = cli.UnavailableExecutor{}
	}
	resolveEgressIPv4 := resolveREGAPIIPv4
	if len(options) > 0 && options[0].ResolveEgressIPv4 != nil {
		resolveEgressIPv4 = options[0].ResolveEgressIPv4
	}
	return &Executor{
		profiles:          profiles,
		broker:            broker,
		fallback:          fallback,
		resolveEgressIPv4: resolveEgressIPv4,
	}
}

func (e *Executor) Execute(
	ctx context.Context,
	operation cli.Operation,
) (cli.Result, error) {
	switch operation.Action {
	case "auth.login":
		return e.login(ctx, operation)
	case "auth.status":
		return e.status(ctx, operation, false)
	case "auth.refresh":
		return e.status(ctx, operation, true)
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
			result := renderStatus(operation.Account, status)
			return e.withREGAPIIPSyncWarning(ctx, result, session.Profile{
				ID:         account.ID,
				SessionRef: currentRef,
			}), nil
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
	result := renderStatus(operation.Account, login.Status)
	return e.withREGAPIIPSyncWarning(ctx, result, session.Profile{
		ID:         account.ID,
		SessionRef: login.SessionRef,
	}), nil
}

func (e *Executor) withREGAPIIPSyncWarning(
	ctx context.Context,
	result cli.Result,
	profile session.Profile,
) cli.Result {
	egressIPv4, err := e.resolveEgressIPv4(ctx)
	if err != nil {
		return withREGAPIIPSyncFailure(result)
	}
	args, err := json.Marshal(struct {
		EgressIPv4 string `json:"egressIPv4"`
	}{EgressIPv4: egressIPv4})
	if err != nil {
		return withREGAPIIPSyncFailure(result)
	}
	err = e.broker.WithSession(ctx, profile, func(page session.PageExecutor) error {
		navigator, ok := page.(session.PageNavigator)
		if !ok {
			return errors.New("portal page navigation is unavailable")
		}
		if err := navigator.Navigate(ctx, regAPISettingsURL); err != nil {
			return err
		}
		var raw json.RawMessage
		if err := page.RunJSON(ctx, regAPIIPSyncProgram, args, &raw); err != nil {
			return err
		}
		var response struct {
			State string `json:"state"`
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			return err
		}
		if response.State != "added" && response.State != "unchanged" {
			return fmt.Errorf("unexpected REG.API IP sync state %q", response.State)
		}
		return nil
	})
	if err == nil {
		return result
	}
	return withREGAPIIPSyncFailure(result)
}

func withREGAPIIPSyncFailure(result cli.Result) cli.Result {
	result.Warnings = append(result.Warnings, cli.Warning{
		Code:    "regapi_ip_sync_failed",
		Message: "REG.API network access could not be synchronized automatically",
	})
	return result
}

func (e *Executor) status(
	ctx context.Context,
	operation cli.Operation,
	syncREGAPIIP bool,
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
	result := renderStatus(operation.Account, status)
	if syncREGAPIIP && status.State == session.StateActive {
		result = e.withREGAPIIPSyncWarning(ctx, result, session.Profile{
			ID:         account.ID,
			SessionRef: account.Portal.SessionRef,
		})
	}
	return result, nil
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
	human := fmt.Sprintf("Portal session for %s: %s", account, status.State)
	if status.ProviderLogin != "" {
		login := escapeOutputField(status.ProviderLogin)
		plain += "\tprovider_login=" + login
		human += fmt.Sprintf(" (REG.RU login: %s)", login)
	}
	capabilities := []string{}
	if status.State == session.StateActive {
		capabilities = append(capabilities, "auth.browser_session")
	}
	return cli.Result{
		Human: human,
		Plain: []string{plain},
		Data: map[string]any{
			"account":       account,
			"providerLogin": status.ProviderLogin,
			"state":         status.State,
			"reason":        status.Reason,
			"capabilities":  capabilities,
		},
		Warnings: []cli.Warning{{
			Code:    "experimental_private_portal",
			Message: "browser-backed REG.RU portal integration is experimental",
		}},
	}
}

func escapeOutputField(value string) string {
	return strings.NewReplacer(
		`\`, `\\`,
		"\t", `\t`,
		"\n", `\n`,
		"\r", `\r`,
	).Replace(value)
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
