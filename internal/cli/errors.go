package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/adinvadim/reg-ru-cli/internal/credentialprocess"
)

const (
	ExitOK                  = 0
	ExitGeneral             = 1
	ExitUsage               = 2
	ExitConfiguration       = 3
	ExitInteractionRequired = 4
	ExitAuthentication      = 5
	ExitNetwork             = 6
	ExitCapability          = 7
	ExitContractDrift       = 8
	ExitOutcomeUnknown      = 10
	ExitTimeout             = 124
	ExitInterrupted         = 130
)

const (
	CodeInternal             = "internal_error"
	CodeInvalidUsage         = "invalid_usage"
	CodeConfiguration        = "configuration_error"
	CodeAccountRequired      = "account_required"
	CodeAccountNotFound      = "account_not_found"
	CodeSensitiveOutput      = "sensitive_output_blocked"
	CodeCredentialRequired   = "credential_required"
	CodeCredentialProcess    = "credential_process_failed"
	CodeInteractiveRequired  = "interactive_required"
	CodeConfirmationRequired = "confirmation_required"
	CodeConfirmationDeclined = "confirmation_declined"
	CodeMissingBrowser       = "missing_browser"
	CodeBrowserSession       = "browser_session_interrupted"
	CodePortalProfileBusy    = "portal_profile_busy"
	CodeLoginCancelled       = "login_cancelled"
	CodeAuthExpired          = "authentication_expired"
	CodeAccountMismatch      = "account_mismatch"
	CodeNetwork              = "network_error"
	CodeProviderContract     = "provider_contract_drift"
	CodeTimeout              = "timeout"
	CodeCapability           = "capability_unavailable"
	CodeContractDrift        = "private_contract_drift"
	CodeOutcomeUnknown       = "outcome_unknown"
	CodeInterrupted          = "interrupted"
)

type CLIError struct {
	Code      string
	Message   string
	ExitCode  int
	Retryable bool
	Details   map[string]any
	Cause     error
}

func (e *CLIError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *CLIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func newCLIError(code, message string, exitCode int, details map[string]any) *CLIError {
	return &CLIError{
		Code:     code,
		Message:  message,
		ExitCode: exitCode,
		Details:  details,
	}
}

func UsageError(message string) *CLIError {
	return newCLIError(CodeInvalidUsage, message, ExitUsage, nil)
}

func AccountRequired() *CLIError {
	return newCLIError(
		CodeAccountRequired,
		"an account profile is required; pass --account or set REGRU_ACCOUNT",
		ExitConfiguration,
		nil,
	)
}

func AccountNotFound(account string) *CLIError {
	return newCLIError(
		CodeAccountNotFound,
		"the selected account profile does not exist",
		ExitConfiguration,
		map[string]any{"account": account},
	)
}

func ConfigurationError(message string) *CLIError {
	return newCLIError(CodeConfiguration, message, ExitConfiguration, nil)
}

func SensitiveOutputBlocked() *CLIError {
	return newCLIError(
		CodeSensitiveOutput,
		"command output was blocked because it contained credential material",
		ExitGeneral,
		nil,
	)
}

func InteractiveRequired(action string) *CLIError {
	return newCLIError(
		CodeInteractiveRequired,
		fmt.Sprintf("%s requires an interactive terminal and cannot run with --no-input", action),
		ExitInteractionRequired,
		map[string]any{"action": action},
	)
}

func ConfirmationRequired(action string) *CLIError {
	return newCLIError(
		CodeConfirmationRequired,
		fmt.Sprintf("%s requires confirmation; rerun in a terminal or pass --force", action),
		ExitInteractionRequired,
		map[string]any{"action": action},
	)
}

func ConfirmationDeclined(action string) *CLIError {
	return newCLIError(
		CodeConfirmationDeclined,
		fmt.Sprintf("%s was not confirmed", action),
		ExitInteractionRequired,
		map[string]any{"action": action},
	)
}

func CapabilityUnavailable(capability, message string) *CLIError {
	return newCLIError(
		CodeCapability,
		message,
		ExitCapability,
		map[string]any{"capability": capability},
	)
}

func CapabilityUnavailableWithReason(capability, reason, message string) *CLIError {
	err := CapabilityUnavailable(capability, message)
	if reason != "" {
		err.Details["reason"] = reason
	}
	return err
}

func ProviderError(
	provider string,
	code string,
	status int,
	retryable bool,
	requestID string,
) *CLIError {
	if code == "" {
		code = "unknown"
	}
	details := map[string]any{
		"provider":      provider,
		"provider_code": code,
		"http_status":   status,
	}
	if requestID != "" {
		details["request_id"] = requestID
	}
	err := newCLIError(
		CodeNetwork,
		fmt.Sprintf("%s rejected the request", provider),
		ExitNetwork,
		details,
	)
	err.Retryable = retryable
	return err
}

func ProviderBulkOperationFailed(provider, operation string, outcomes []any) *CLIError {
	if outcomes == nil {
		outcomes = []any{}
	}
	return newCLIError(
		CodeNetwork,
		fmt.Sprintf("%s did not complete every %s item", provider, operation),
		ExitNetwork,
		map[string]any{
			"provider":  provider,
			"operation": operation,
			"outcomes":  outcomes,
		},
	)
}

func ProviderAuthenticationError(provider string) *CLIError {
	return newCLIError(
		CodeAuthExpired,
		fmt.Sprintf("%s credentials were rejected; refresh the selected account credential", provider),
		ExitAuthentication,
		map[string]any{"provider": provider},
	)
}

func ProviderContractDrift(provider string) *CLIError {
	return newCLIError(
		CodeProviderContract,
		fmt.Sprintf("%s response no longer matches the documented API contract", provider),
		ExitNetwork,
		map[string]any{"provider": provider},
	)
}

func ProviderWaitStopped(
	provider string,
	actionID string,
	status string,
	cause error,
	retryable bool,
) *CLIError {
	code := CodeTimeout
	message := fmt.Sprintf("%s action wait timed out; resume it with the action identifier", provider)
	exitCode := ExitTimeout
	if errors.Is(cause, context.Canceled) {
		code = CodeInterrupted
		message = fmt.Sprintf("%s action wait was interrupted; the provider action was not cancelled", provider)
		exitCode = ExitInterrupted
	}
	err := newCLIError(
		code,
		message,
		exitCode,
		map[string]any{
			"provider":  provider,
			"action_id": actionID,
			"status":    status,
		},
	)
	err.Retryable = retryable
	err.Cause = cause
	return err
}

func ProviderActionFailed(provider, actionID, status string) *CLIError {
	return newCLIError(
		CodeNetwork,
		fmt.Sprintf("%s action ended with status %s", provider, status),
		ExitNetwork,
		map[string]any{
			"provider":  provider,
			"action_id": actionID,
			"status":    status,
		},
	)
}

func NetworkError(provider string, retryable bool) *CLIError {
	err := newCLIError(
		CodeNetwork,
		fmt.Sprintf("%s request failed before a usable response was received", provider),
		ExitNetwork,
		map[string]any{"provider": provider},
	)
	err.Retryable = retryable
	return err
}

func MissingBrowser() *CLIError {
	return newCLIError(
		CodeMissingBrowser,
		"no supported Chrome or Chromium browser was found",
		ExitAuthentication,
		nil,
	)
}

func BrowserSessionInterrupted() *CLIError {
	return newCLIError(
		CodeBrowserSession,
		"the managed browser session was interrupted; retry the read or run regru auth status",
		ExitNetwork,
		nil,
	)
}

func PortalProfileBusy() *CLIError {
	err := newCLIError(
		CodePortalProfileBusy,
		"another regru command is already using this account's browser profile",
		ExitAuthentication,
		nil,
	)
	err.Retryable = true
	return err
}

func LoginCancelled() *CLIError {
	return newCLIError(
		CodeLoginCancelled,
		"browser login was cancelled before authentication completed",
		ExitAuthentication,
		nil,
	)
}

func AuthenticationExpired() *CLIError {
	return newCLIError(
		CodeAuthExpired,
		"the portal session is no longer active; run regru auth login again",
		ExitAuthentication,
		nil,
	)
}

func AccountMismatch(expected, actual string) *CLIError {
	_ = actual
	return newCLIError(
		CodeAccountMismatch,
		"the authenticated REG.RU principal does not match the selected account profile",
		ExitAuthentication,
		map[string]any{"selected_account": expected, "identity_match": false},
	)
}

func PrivateContractDrift(capability string) *CLIError {
	return newCLIError(
		CodeContractDrift,
		"the private REG.RU portal contract failed its version probe; the operation was not attempted",
		ExitContractDrift,
		map[string]any{"capability": capability},
	)
}

func OutcomeUnknown(capability string) *CLIError {
	return newCLIError(
		CodeOutcomeUnknown,
		"the mutation may have been delivered but its result could not be verified; reconcile state before retrying",
		ExitOutcomeUnknown,
		map[string]any{"capability": capability},
	)
}

func classifyError(err error) *CLIError {
	if err == nil {
		return nil
	}

	var cliErr *CLIError
	if errors.As(err, &cliErr) {
		return cliErr
	}

	var processErr *credentialprocess.ProcessError
	if errors.As(err, &processErr) {
		switch processErr.Code {
		case "credential_process_not_configured":
			return newCLIError(
				CodeCredentialRequired,
				"credentials are required; configure credential_process in the user account profile",
				ExitConfiguration,
				nil,
			)
		case "credential_field_unavailable":
			return newCLIError(
				CodeCredentialRequired,
				"the credential process did not return a required field",
				ExitConfiguration,
				nil,
			)
		case "credential_process_timeout":
			return newCLIError(
				CodeTimeout,
				"the credential process timed out",
				ExitTimeout,
				nil,
			)
		case "credential_process_cancelled":
			return newCLIError(
				CodeInterrupted,
				"the credential process was interrupted",
				ExitInterrupted,
				nil,
			)
		default:
			return newCLIError(
				CodeCredentialProcess,
				"the credential process failed or returned invalid output",
				ExitConfiguration,
				nil,
			)
		}
	}

	switch {
	case errors.Is(err, context.Canceled):
		return &CLIError{
			Code:     CodeInterrupted,
			Message:  "operation interrupted",
			ExitCode: ExitInterrupted,
			Cause:    err,
		}
	case errors.Is(err, context.DeadlineExceeded):
		return &CLIError{
			Code:     CodeTimeout,
			Message:  "operation timed out before completion",
			ExitCode: ExitTimeout,
			Cause:    err,
		}
	case looksLikeUsageError(err):
		return &CLIError{
			Code:     CodeInvalidUsage,
			Message:  err.Error(),
			ExitCode: ExitUsage,
			Cause:    err,
		}
	default:
		return &CLIError{
			Code:     CodeInternal,
			Message:  "the command failed unexpectedly",
			ExitCode: ExitGeneral,
			Cause:    err,
		}
	}
}

func looksLikeUsageError(err error) bool {
	message := strings.ToLower(err.Error())
	for _, fragment := range []string{
		"unknown command",
		"unknown flag",
		"requires at least",
		"requires at most",
		"required flag",
		"accepts ",
		"arg(s)",
	} {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}
