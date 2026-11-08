package cli

import "fmt"

const (
	CodeIdentificationRequired = "identification_required"
	CodeInsufficientFunds      = "insufficient_funds"
	CodePriceCapExceeded       = "price_cap_exceeded"
	CodeSpendingPolicyRefused  = "spending_policy_refused"
	CodeDomainUnavailable      = "domain_unavailable"
	CodeAvailabilityUnknown    = "availability_unknown"
)

// IdentificationURL is where the REG.RU account owner completes Gosuslugi (ESIA)
// identification for domain administration.
const IdentificationURL = "https://www.reg.ru/user/account/domain_identification"

// IdentificationRequired reports a REG.RU ESIA identification gate. Only the
// account owner can complete it, so it is never retried or bypassed.
func IdentificationRequired(providerCode string) *CLIError {
	return newCLIError(
		CodeIdentificationRequired,
		"REG.RU requires Gosuslugi (ESIA) identification of the domain administrator; the owner must complete it at "+IdentificationURL,
		ExitInteractionRequired,
		map[string]any{"provider_code": providerCode, "url": IdentificationURL},
	)
}

// InsufficientFunds reports that the REG.RU balance cannot pay a purchase. No
// order exists; the owner tops up the balance and reruns the command.
func InsufficientFunds(price, balance, missing, currency string, dispatched bool) *CLIError {
	return newCLIError(
		CodeInsufficientFunds,
		fmt.Sprintf(
			"the REG.RU balance is short by %s %s; top up the account balance by at least that amount (REG.RU cabinet, SBP) and rerun",
			missing, currency,
		),
		ExitInteractionRequired,
		map[string]any{
			"price": price, "balance": balance, "missing_amount": missing,
			"currency": currency, "order_created": false, "dispatched": dispatched,
		},
	)
}

// PriceCapExceeded reports a live quote above the spending caps.
func PriceCapExceeded(details map[string]any) *CLIError {
	return newCLIError(
		CodePriceCapExceeded,
		"the live price exceeds the account spending caps; rerun with --force or raise --max-price/--max-renewal after approval",
		ExitInteractionRequired,
		details,
	)
}

// SpendingPolicyRefused reports a purchase the policy cannot evaluate safely;
// --force does not override it.
func SpendingPolicyRefused(reason, message string, details map[string]any) *CLIError {
	if details == nil {
		details = map[string]any{}
	}
	details["reason"] = reason
	return newCLIError(CodeSpendingPolicyRefused, message, ExitCapability, details)
}

// DomainUnavailable reports a name the registry or registrar says cannot be registered.
func DomainUnavailable(name string, details map[string]any) *CLIError {
	if details == nil {
		details = map[string]any{}
	}
	details["domain"] = name
	return newCLIError(CodeDomainUnavailable, fmt.Sprintf("%s cannot be registered", name), ExitNetwork, details)
}

// AvailabilityUnknown reports that no definite availability verdict was reached.
func AvailabilityUnknown(details map[string]any) *CLIError {
	err := newCLIError(
		CodeAvailabilityUnknown,
		"no definite availability verdict was reached; unknown is never treated as free",
		ExitNetwork,
		details,
	)
	err.Retryable = true
	return err
}

// OutcomeUnknownWithDetails is OutcomeUnknown with reconciliation evidence.
func OutcomeUnknownWithDetails(capability, message string, details map[string]any) *CLIError {
	err := OutcomeUnknown(capability)
	if message != "" {
		err.Message = message
	}
	for key, value := range details {
		err.Details[key] = value
	}
	return err
}
