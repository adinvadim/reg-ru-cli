package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	CodeInteractiveRequired  = "interactive_required"
	CodeConfirmationRequired = "confirmation_required"
	CodeConfirmationDeclined = "confirmation_declined"
	CodeMissingBrowser       = "missing_browser"
	CodeLoginCancelled       = "login_cancelled"
	CodeAuthExpired          = "authentication_expired"
	CodeAccountMismatch      = "account_mismatch"
	CodeNetwork              = "network_error"
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

func MissingBrowser() *CLIError {
	return newCLIError(
		CodeMissingBrowser,
		"no supported Chrome or Chromium browser was found",
		ExitAuthentication,
		nil,
	)
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
		"the authenticated portal session has expired; run regru auth login again",
		ExitAuthentication,
		nil,
	)
}

func AccountMismatch(expected, actual string) *CLIError {
	return newCLIError(
		CodeAccountMismatch,
		"the authenticated REG.RU principal does not match the selected account profile",
		ExitAuthentication,
		map[string]any{"expected_account": expected, "actual_account": actual},
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
		"accepts ",
		"arg(s)",
	} {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}
