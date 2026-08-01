package capability

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adinvadim/reg-ru-cli/internal/cli"
	"github.com/adinvadim/reg-ru-cli/internal/credentialprocess"
	"github.com/adinvadim/reg-ru-cli/internal/profile"
)

const defaultProbeTimeout = 5 * time.Second

type Executor struct {
	profiles profile.Repository
	fallback cli.Executor
	timeout  time.Duration
}

func NewExecutor(profiles profile.Repository, fallback cli.Executor) *Executor {
	if fallback == nil {
		fallback = cli.UnavailableExecutor{}
	}
	return &Executor{
		profiles: profiles,
		fallback: fallback,
		timeout:  defaultProbeTimeout,
	}
}

func (e *Executor) Execute(ctx context.Context, operation cli.Operation) (cli.Result, error) {
	if operation.Action != "capability.probe" {
		return e.fallback.Execute(ctx, operation)
	}
	account, err := e.account(operation)
	if err != nil {
		return cli.Result{}, err
	}

	probes := configuredProbes(account)
	results := make([]probeResult, 0, len(probes))
	for _, probe := range probes {
		if err := ctx.Err(); err != nil {
			return cli.Result{}, err
		}
		result, err := e.run(ctx, operation, probe)
		if err != nil {
			return cli.Result{}, err
		}
		results = append(results, result)
	}
	return render(operation.Account, results), nil
}

func (e *Executor) account(operation cli.Operation) (profile.Account, error) {
	if e == nil || e.profiles == nil {
		return profile.Account{}, cli.ConfigurationError("capability probing is not configured")
	}
	config, err := e.profiles.Load()
	if err != nil {
		return profile.Account{}, cli.ConfigurationError("profile configuration is invalid")
	}
	account, exists := config.Accounts[operation.Account]
	if !exists || account.ID != operation.ProfileID {
		return profile.Account{}, cli.AccountNotFound(operation.Account)
	}
	return account, nil
}

type probeSpec struct {
	capability string
	action     string
	parameters map[string][]string
}

func configuredProbes(account profile.Account) []probeSpec {
	probes := []probeSpec{}
	if account.Portal.SessionRef != "" {
		probes = append(probes, probeSpec{
			capability: "auth.session_status",
			action:     "auth.status",
		})
	}
	if account.Credentials.CloudVPSRef != "" ||
		account.Portal.SessionRef != "" ||
		len(account.CredentialProcess.Command) > 0 {
		probes = append(probes, probeSpec{
			capability: "cloudvps.instances",
			action:     "billing.balance",
			parameters: map[string][]string{"source": {"cloudvps"}},
		})
	}
	if account.Credentials.REGAPIRef != "" || len(account.CredentialProcess.Command) > 0 {
		probes = append(probes, probeSpec{
			capability: "billing.read",
			action:     "billing.balance",
			parameters: map[string][]string{
				"source":   {"regapi"},
				"currency": {"RUR"},
			},
		})
	}
	if account.Portal.SessionRef != "" {
		probes = append(probes,
			probeSpec{capability: "s3.control_plane", action: "s3.service.show"},
			probeSpec{capability: "support.ticket.list", action: "support.ticket.list"},
		)
	}
	return probes
}

type probeResult struct {
	Capability string `json:"capability"`
	State      string `json:"state"`
	Reason     string `json:"reason"`
}

func (e *Executor) run(
	ctx context.Context,
	operation cli.Operation,
	probe probeSpec,
) (probeResult, error) {
	timeout := e.timeout
	if timeout <= 0 || operation.RequestTimeout < timeout {
		timeout = operation.RequestTimeout
	}
	if timeout <= 0 {
		timeout = defaultProbeTimeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	inner := operation
	inner.Action = probe.action
	inner.Capability = probe.capability
	inner.Arguments = nil
	inner.Parameters = cloneParameters(probe.parameters)
	inner.NoWait = false
	inner.Force = false
	inner.RequestTimeout = timeout
	inner.WaitTimeout = timeout
	inner.Input = nil
	_, err := e.fallback.Execute(probeCtx, inner)
	if err == nil {
		return probeResult{Capability: probe.capability, State: "available", Reason: "none"}, nil
	}
	if parentErr := ctx.Err(); parentErr != nil {
		return probeResult{}, parentErr
	}
	return probeResult{
		Capability: probe.capability,
		State:      "unavailable",
		Reason:     reason(err),
	}, nil
}

func cloneParameters(source map[string][]string) map[string][]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string][]string, len(source))
	for key, values := range source {
		result[key] = append([]string(nil), values...)
	}
	return result
}

func reason(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	}
	var processErr *credentialprocess.ProcessError
	if errors.As(err, &processErr) {
		switch processErr.Code {
		case "credential_process_not_configured", "credential_field_unavailable":
			return "missing_credentials"
		case "credential_process_timeout":
			return "timeout"
		case "credential_process_cancelled":
			return "cancelled"
		default:
			return "credential_process_failed"
		}
	}
	var cliErr *cli.CLIError
	if !errors.As(err, &cliErr) {
		return "internal_error"
	}
	switch cliErr.Code {
	case cli.CodeCredentialRequired:
		return "missing_credentials"
	case cli.CodeCredentialProcess:
		return "credential_process_failed"
	case cli.CodeAuthExpired, cli.CodeAccountMismatch:
		return "authentication_lost"
	case cli.CodeContractDrift:
		return "private_contract_drift"
	case cli.CodeProviderContract:
		return "provider_contract_drift"
	case cli.CodeCapability, cli.CodeMissingBrowser:
		return "adapter_unavailable"
	case cli.CodeTimeout:
		return "timeout"
	case cli.CodeInterrupted:
		return "cancelled"
	case cli.CodeNetwork, cli.CodeBrowserSession:
		return "network_unavailable"
	default:
		return "provider_rejected"
	}
}

func render(account string, results []probeResult) cli.Result {
	available := 0
	plain := make([]string, 0, len(results))
	human := make([]string, 0, len(results)+1)
	for _, result := range results {
		if result.State == "available" {
			available++
		}
		plain = append(plain, fmt.Sprintf(
			"%s\t%s\t%s",
			result.Capability,
			result.State,
			result.Reason,
		))
		human = append(human, fmt.Sprintf(
			"%s: %s (%s)",
			result.Capability,
			result.State,
			result.Reason,
		))
	}
	human = append([]string{fmt.Sprintf(
		"Capability probe for %s: %d available, %d unavailable",
		account,
		available,
		len(results)-available,
	)}, human...)
	return cli.Result{
		Human: strings.Join(human, "\n"),
		Plain: plain,
		Data: map[string]any{
			"account":      account,
			"capabilities": results,
			"summary": map[string]int{
				"configured":  len(results),
				"available":   available,
				"unavailable": len(results) - available,
			},
		},
	}
}
