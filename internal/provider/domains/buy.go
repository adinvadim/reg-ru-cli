package domains

import (
	"context"
	"errors"

	"github.com/adinvadim/reg-ru-cli/internal/cli"
	"github.com/adinvadim/reg-ru-cli/internal/domain"
	"github.com/adinvadim/reg-ru-cli/internal/provider/regapi"
	"github.com/adinvadim/reg-ru-cli/internal/provider/registry"
)

type purchasePlan struct {
	name        domain.Name
	nameservers []string
	caps        domain.SpendingCaps
	price       regapi.DomainPrice
	evaluation  domain.Evaluation
	balance     string
	userServID  string
	profileType string
	profileName string
}

func (e *Executor) buy(ctx context.Context, operation cli.Operation) (cli.Result, error) {
	name, err := domain.Parse(argument(operation, 0))
	if err != nil {
		return cli.Result{}, cli.UsageError(err.Error())
	}
	nameservers, err := domain.ParseNameservers(parameter(operation, "ns"))
	if err != nil {
		return cli.Result{}, cli.UsageError(err.Error())
	}
	account, err := e.account(operation)
	if err != nil {
		return cli.Result{}, err
	}
	if account.Domain.ContactProfileType == "" || account.Domain.ContactProfileName == "" {
		return cli.Result{}, cli.ConfigurationError(
			"set contact_profile_type and contact_profile_name under [accounts." + operation.Account + ".domain] in the user profile; REG.RU contact profiles are created in the web cabinet",
		)
	}
	plan := purchasePlan{
		name: name, nameservers: nameservers, caps: spendingCaps(operation, account),
		profileType: account.Domain.ContactProfileType, profileName: account.Domain.ContactProfileName,
		userServID: parameter(operation, "user-servid"),
	}

	verdict := e.options.Registry.Check(ctx, []domain.Name{name})[0]
	if err := ctx.Err(); err != nil {
		return cli.Result{}, err
	}
	switch verdict.State {
	case registry.StateTaken:
		return cli.Result{}, cli.DomainUnavailable(name.Display(), map[string]any{"source": verdict.Source, "state": string(verdict.State)})
	case registry.StateUnknown:
		return cli.Result{}, cli.AvailabilityUnknown(map[string]any{"domain": name.Display(), "source": verdict.Source, "reason": verdict.Reason})
	}

	client, err := e.regapiClient(ctx, operation)
	if err != nil {
		return cli.Result{}, translateError(operation, err)
	}
	defer client.Close()
	plan.price, err = zonePrice(ctx, client, name.Zone)
	if err != nil {
		return cli.Result{}, translateError(operation, err)
	}
	if plan.price.MinPeriod != "" && plan.price.MinPeriod != "1" {
		return cli.Result{}, cli.SpendingPolicyRefused("multi_year_minimum", "the zone requires a registration period longer than one year", map[string]any{"min_period": plan.price.MinPeriod})
	}
	plan.evaluation = domain.Evaluate(domain.Quote{FirstYear: plan.price.RegPrice, Renewal: plan.price.RenewPrice}, plan.caps, operation.Force)
	switch plan.evaluation.Decision {
	case domain.DecisionRefuse:
		return cli.Result{}, cli.SpendingPolicyRefused(plan.evaluation.Reason, "the purchase cannot be evaluated against the spending policy; an unknown renewal price is never bought", quoteDetails(plan))
	case domain.DecisionNeedsForce:
		return cli.Result{}, cli.PriceCapExceeded(quoteDetails(plan))
	}

	balance, err := client.GetBalance(ctx, "RUR")
	if err != nil {
		return cli.Result{}, translateError(operation, err)
	}
	plan.balance = balance.Prepay
	if missing, short := domain.Shortfall(plan.price.RegPrice, balance.Prepay); short {
		return cli.Result{}, cli.InsufficientFunds(plan.price.RegPrice, balance.Prepay, missing, "RUR", false)
	}
	if plan.userServID == "" {
		plan.userServID, err = e.options.NewUserServID()
		if err != nil || !regapi.ValidUserServID(plan.userServID) {
			return cli.Result{}, cli.ConfigurationError("could not generate a REG.API user_servid")
		}
	}
	if err := ctx.Err(); err != nil {
		return cli.Result{}, err
	}

	created, err := client.CreateDomain(ctx, regapi.DomainCreateRequest{
		Name: name.ASCII, ProfileType: plan.profileType, ProfileName: plan.profileName,
		Nameservers: nameservers, UserServID: plan.userServID,
	})
	if err == nil {
		return renderPurchase(plan, created, nil), nil
	}
	return e.purchaseFailure(ctx, operation, client, plan, err)
}

func (e *Executor) purchaseFailure(
	ctx context.Context,
	operation cli.Operation,
	client *regapi.Client,
	plan purchasePlan,
	err error,
) (cli.Result, error) {
	var apiErr *regapi.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case "NOT_ENOUGH_MONEY":
			missing, _ := domain.Shortfall(plan.price.RegPrice, plan.balance)
			if missing == "" {
				missing = plan.price.RegPrice
			}
			return cli.Result{}, cli.InsufficientFunds(plan.price.RegPrice, plan.balance, missing, "RUR", true)
		case "USER_SERVID_IS_NOT_UNIQUE":
			return e.reconcile(ctx, operation, client, plan, "user_servid_already_used")
		}
	}
	var ambiguous *regapi.AmbiguousMutationError
	if errors.As(err, &ambiguous) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return e.reconcile(ctx, operation, client, plan, "dispatch_outcome_unknown")
	}
	var itemErr *regapi.DomainItemError
	if errors.As(err, &itemErr) && !identificationCode(itemErr.Code) {
		return cli.Result{}, cli.DomainUnavailable(plan.name.Display(), map[string]any{
			"source": registry.SourceRegistrar, "provider_code": itemErr.Code, "order_created": false,
		})
	}
	return cli.Result{}, translateError(operation, err)
}

// Evidence collected after an ambiguous purchase; it never triggers a retry.
type Evidence struct {
	Trigger        string `json:"trigger"`
	UserServID     string `json:"userServid"`
	ServiceLookup  string `json:"serviceLookup"`
	BalanceBefore  string `json:"balanceBefore,omitempty"`
	BalanceAfter   string `json:"balanceAfter,omitempty"`
	BalanceChanged *bool  `json:"balanceChanged,omitempty"`
	RegistryState  string `json:"registryState,omitempty"`
}

func (e *Executor) reconcile(
	ctx context.Context,
	operation cli.Operation,
	client *regapi.Client,
	plan purchasePlan,
	trigger string,
) (cli.Result, error) {
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), e.options.ReconcileTimeout)
	defer cancel()
	evidence := Evidence{Trigger: trigger, UserServID: plan.userServID, BalanceBefore: plan.balance}
	info, found, err := client.ServiceByUserServID(reconcileCtx, plan.userServID)
	switch {
	case err != nil:
		evidence.ServiceLookup = "failed"
	case found:
		evidence.ServiceLookup = "found"
		return renderPurchase(plan, regapi.DomainCreateResult{Name: plan.name.ASCII, ServiceID: info.ServiceID}, &reconciled{evidence: evidence, state: info.State}), nil
	default:
		evidence.ServiceLookup = "not_found"
	}
	if balance, err := client.GetBalance(reconcileCtx, "RUR"); err == nil {
		evidence.BalanceAfter = balance.Prepay
		changed := !domain.SameAmount(plan.balance, balance.Prepay)
		evidence.BalanceChanged = &changed
	}
	verdict := e.options.Registry.Check(reconcileCtx, []domain.Name{plan.name})[0]
	evidence.RegistryState = string(verdict.State)
	return cli.Result{}, cli.OutcomeUnknownWithDetails(
		operation.Capability,
		"the domain order may have been delivered but could not be confirmed; do not rerun blindly: check regru domain list, and if you retry pass --user-servid with the same value so a landed order is detected",
		map[string]any{"domain": plan.name.Display(), "evidence": evidence},
	)
}

func quoteDetails(plan purchasePlan) map[string]any {
	return map[string]any{
		"domain":      plan.name.Display(),
		"first_year":  plan.price.RegPrice,
		"renewal":     plan.price.RenewPrice,
		"currency":    plan.price.Currency,
		"max_price":   plan.caps.MaxPrice,
		"max_renewal": plan.caps.MaxRenewal,
		"evaluation":  plan.evaluation,
	}
}
