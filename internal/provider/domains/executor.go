// Package domains executes the regru domain command group: registry-first
// availability, REG.API quotes, policy-bounded purchase, listing, and
// nameserver changes.
package domains

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/adinvadim/reg-ru-cli/internal/cli"
	"github.com/adinvadim/reg-ru-cli/internal/domain"
	"github.com/adinvadim/reg-ru-cli/internal/profile"
	"github.com/adinvadim/reg-ru-cli/internal/provider/regapi"
	"github.com/adinvadim/reg-ru-cli/internal/provider/registry"
)

const defaultReconcileTimeout = 20 * time.Second

// Registry is the availability seam; production uses *registry.Checker.
type Registry interface {
	Check(context.Context, []domain.Name) []registry.Verdict
}

// ExecutorOptions wires the adapters. Tests replace every network seam.
type ExecutorOptions struct {
	Profiles         profile.Repository
	Registry         Registry
	REGAPIBaseURL    string
	HTTPClient       regapi.HTTPDoer
	NewUserServID    func() (string, error)
	ReconcileTimeout time.Duration
}

// Executor handles every domain.* action and delegates the rest.
type Executor struct {
	options  ExecutorOptions
	fallback cli.Executor
}

// NewExecutor returns a domain executor in front of fallback.
func NewExecutor(options ExecutorOptions, fallback cli.Executor) *Executor {
	if fallback == nil {
		fallback = cli.UnavailableExecutor{}
	}
	if options.Registry == nil {
		options.Registry = registry.NewChecker(registry.Options{})
	}
	if options.NewUserServID == nil {
		options.NewUserServID = newUserServID
	}
	if options.ReconcileTimeout <= 0 {
		options.ReconcileTimeout = defaultReconcileTimeout
	}
	return &Executor{options: options, fallback: fallback}
}

// Execute dispatches one operation.
func (e *Executor) Execute(ctx context.Context, operation cli.Operation) (cli.Result, error) {
	switch operation.Action {
	case "domain.search":
		return e.search(ctx, operation)
	case "domain.check":
		return e.check(ctx, operation)
	case "domain.quote":
		return e.quote(ctx, operation)
	case "domain.buy":
		return e.buy(ctx, operation)
	case "domain.list":
		return e.list(ctx, operation)
	case "domain.ns.set":
		return e.setNameservers(ctx, operation)
	default:
		return e.fallback.Execute(ctx, operation)
	}
}

func (e *Executor) search(ctx context.Context, operation cli.Operation) (cli.Result, error) {
	zones, err := parseZones(parameter(operation, "zones"))
	if err != nil {
		return cli.Result{}, cli.UsageError(err.Error())
	}
	limit, _ := strconv.Atoi(parameter(operation, "limit"))
	candidates, err := domain.Generate(operation.Arguments, zones, limit)
	if err != nil {
		return cli.Result{}, cli.UsageError(err.Error())
	}
	names := make([]domain.Name, 0, len(candidates))
	for _, candidate := range candidates {
		names = append(names, candidate.Name)
	}
	verdicts := e.options.Registry.Check(ctx, names)
	if err := ctx.Err(); err != nil {
		return cli.Result{}, err
	}
	status := e.confirmWithRegistrar(ctx, operation, verdicts)
	return renderAvailability(operation, verdicts, status, true)
}

func (e *Executor) check(ctx context.Context, operation cli.Operation) (cli.Result, error) {
	names := []domain.Name{}
	seen := map[string]bool{}
	for _, argument := range operation.Arguments {
		name, err := domain.Parse(argument)
		if err != nil {
			return cli.Result{}, cli.UsageError(err.Error())
		}
		if !seen[name.ASCII] {
			seen[name.ASCII] = true
			names = append(names, name)
		}
	}
	verdicts := e.options.Registry.Check(ctx, names)
	if err := ctx.Err(); err != nil {
		return cli.Result{}, err
	}
	status := e.confirmWithRegistrar(ctx, operation, verdicts)
	return renderAvailability(operation, verdicts, status, false)
}

// RegistrarStatus says whether the optional REG.API confirmation ran.
type RegistrarStatus struct {
	Checked bool   `json:"checked"`
	Reason  string `json:"reason"`
}

func (e *Executor) confirmWithRegistrar(ctx context.Context, operation cli.Operation, verdicts []registry.Verdict) RegistrarStatus {
	pending := []int{}
	for index, verdict := range verdicts {
		if verdict.State == registry.StateLikelyFree {
			pending = append(pending, index)
		}
	}
	if len(pending) == 0 {
		return RegistrarStatus{Reason: "no_likely_free_names"}
	}
	if operation.Account == "" || operation.Credentials == nil {
		return e.markRegistrar(verdicts, pending, "no_account_selected")
	}
	account, err := e.account(operation)
	if err != nil || !account.Domain.RegistrarCheck {
		return e.markRegistrar(verdicts, pending, "registrar_check_disabled")
	}
	client, err := e.regapiClient(ctx, operation)
	if err != nil {
		return e.markRegistrar(verdicts, pending, "credentials_unavailable")
	}
	defer client.Close()
	asciiNames := make([]string, 0, len(pending))
	for _, index := range pending {
		asciiNames = append(asciiNames, verdicts[index].Name.ASCII)
	}
	checks, err := client.CheckDomains(ctx, asciiNames)
	if err != nil {
		reason := "registrar_unavailable"
		var apiErr *regapi.APIError
		if errors.As(err, &apiErr) {
			switch {
			case apiErr.Code == "RESELLER_AUTH_FAILED":
				reason = "registrar_check_partner_only"
			case apiErr.AuthenticationFailure():
				reason = "registrar_authentication_failed"
			}
		}
		return e.markRegistrar(verdicts, pending, reason)
	}
	byName := make(map[string]regapi.DomainCheck, len(checks))
	for _, check := range checks {
		byName[check.Name] = check
	}
	for _, index := range pending {
		verdict := &verdicts[index]
		check, exists := byName[verdict.Name.ASCII]
		if !exists {
			verdict.Registrar = &registry.Registrar{Reason: "registrar_answer_missing"}
			continue
		}
		verdict.Registrar = &registry.Registrar{
			Checked: true, Result: check.Result, ErrorCode: check.ErrorCode, Premium: check.Premium,
			Price: check.Price, RenewPrice: check.RenewPrice, Currency: check.Currency,
		}
		switch {
		case check.Available:
			verdict.State, verdict.Source = registry.StateFree, registry.SourceRegistrar
		case check.ErrorCode == "CONNECTION_FAILED":
			verdict.Registrar.Reason = "registrar_registry_check_failed"
		default:
			verdict.State, verdict.Source, verdict.Reason = registry.StateTaken, registry.SourceRegistrar, strings.ToLower(check.ErrorCode)
		}
	}
	return RegistrarStatus{Checked: true, Reason: "confirmed"}
}

func (e *Executor) markRegistrar(verdicts []registry.Verdict, indexes []int, reason string) RegistrarStatus {
	for _, index := range indexes {
		verdicts[index].Registrar = &registry.Registrar{Reason: reason}
	}
	return RegistrarStatus{Reason: reason}
}

func (e *Executor) quote(ctx context.Context, operation cli.Operation) (cli.Result, error) {
	name, err := domain.Parse(argument(operation, 0))
	if err != nil {
		return cli.Result{}, cli.UsageError(err.Error())
	}
	account, err := e.account(operation)
	if err != nil {
		return cli.Result{}, err
	}
	client, err := e.regapiClient(ctx, operation)
	if err != nil {
		return cli.Result{}, translateError(operation, err)
	}
	defer client.Close()
	price, err := zonePrice(ctx, client, name.Zone)
	if err != nil {
		return cli.Result{}, translateError(operation, err)
	}
	caps := spendingCaps(operation, account)
	evaluation := domain.Evaluate(domain.Quote{FirstYear: price.RegPrice, Renewal: price.RenewPrice}, caps, false)
	return renderQuote(name, price, caps, evaluation), nil
}

func (e *Executor) list(ctx context.Context, operation cli.Operation) (cli.Result, error) {
	client, err := e.regapiClient(ctx, operation)
	if err != nil {
		return cli.Result{}, translateError(operation, err)
	}
	defer client.Close()
	services, err := client.ListDomains(ctx)
	if err != nil {
		return cli.Result{}, translateError(operation, err)
	}
	return renderList(services), nil
}

func (e *Executor) setNameservers(ctx context.Context, operation cli.Operation) (cli.Result, error) {
	name, err := domain.Parse(argument(operation, 0))
	if err != nil {
		return cli.Result{}, cli.UsageError(err.Error())
	}
	nameservers, err := domain.ParseNameservers(parameter(operation, "ns"))
	if err != nil {
		return cli.Result{}, cli.UsageError(err.Error())
	}
	client, err := e.regapiClient(ctx, operation)
	if err != nil {
		return cli.Result{}, translateError(operation, err)
	}
	defer client.Close()
	if err := ctx.Err(); err != nil {
		return cli.Result{}, err
	}
	update, err := client.UpdateNameservers(ctx, name.ASCII, nameservers)
	if err != nil {
		return cli.Result{}, translateError(operation, err)
	}
	result := renderNameservers(name, nameservers, update)
	confirmNameservers(ctx, client, name, nameservers, &result)
	return result, nil
}

func (e *Executor) account(operation cli.Operation) (profile.Account, error) {
	if e.options.Profiles == nil {
		return profile.Account{}, cli.ConfigurationError("domain profiles are not configured")
	}
	config, err := e.options.Profiles.Load()
	if err != nil {
		return profile.Account{}, cli.ConfigurationError("profile configuration is invalid")
	}
	account, exists := config.Accounts[operation.Account]
	if !exists || account.ID != operation.ProfileID {
		return profile.Account{}, cli.AccountNotFound(operation.Account)
	}
	return account, nil
}

func (e *Executor) regapiClient(ctx context.Context, operation cli.Operation) (*regapi.Client, error) {
	if operation.Credentials == nil {
		return nil, cli.ConfigurationError("REG.API credentials are not configured")
	}
	username, err := operation.Credentials.Resolve(ctx, "regapi.username")
	if err != nil {
		return nil, err
	}
	defer wipe(username)
	password, err := operation.Credentials.Resolve(ctx, "regapi.password")
	if err != nil {
		return nil, err
	}
	defer wipe(password)
	return regapi.New(username, password, regapi.ClientOptions{
		BaseURL: e.options.REGAPIBaseURL, HTTPClient: e.options.HTTPClient,
		RequestTimeout: operation.RequestTimeout,
	})
}

func zonePrice(ctx context.Context, client *regapi.Client, zone domain.Zone) (regapi.DomainPrice, error) {
	prices, err := client.GetDomainPrices(ctx, "RUR")
	if err != nil {
		return regapi.DomainPrice{}, err
	}
	for _, key := range []string{string(zone), zone.ASCII()} {
		if price, exists := prices[key]; exists {
			return price, nil
		}
	}
	return regapi.DomainPrice{}, cli.CapabilityUnavailableWithReason(
		"domain.quote", "zone_price_missing", "REG.API returned no price for the ."+string(zone)+" zone",
	)
}

func spendingCaps(operation cli.Operation, account profile.Account) domain.SpendingCaps {
	caps := domain.SpendingCaps{MaxPrice: domain.DefaultMaxPrice, MaxRenewal: domain.DefaultMaxRenewal}
	if account.Domain.MaxPrice != "" {
		caps.MaxPrice = account.Domain.MaxPrice
	}
	if account.Domain.MaxRenewal != "" {
		caps.MaxRenewal = account.Domain.MaxRenewal
	}
	if value := parameter(operation, "max-price"); value != "" {
		caps.MaxPrice = value
	}
	if value := parameter(operation, "max-renewal"); value != "" {
		caps.MaxRenewal = value
	}
	return caps
}

func translateError(operation cli.Operation, err error) error {
	if err == nil {
		return nil
	}
	var cliErr *cli.CLIError
	if errors.As(err, &cliErr) {
		return cliErr
	}
	var ambiguous *regapi.AmbiguousMutationError
	if errors.As(err, &ambiguous) {
		return cli.OutcomeUnknown(operation.Capability)
	}
	var itemErr *regapi.DomainItemError
	if errors.As(err, &itemErr) {
		if identificationCode(itemErr.Code) {
			return cli.IdentificationRequired(itemErr.Code)
		}
		return cli.ProviderError("REG.API", itemErr.Code, 200, false, "")
	}
	var contract *regapi.ContractError
	if errors.As(err, &contract) {
		return cli.ProviderContractDrift("REG.API")
	}
	var apiErr *regapi.APIError
	if errors.As(err, &apiErr) {
		switch {
		case identificationCode(apiErr.Code):
			return cli.IdentificationRequired(apiErr.Code)
		case apiErr.Code == "ACCESS_DENIED_FROM_IP":
			authErr := cli.ProviderAuthenticationError("REG.API")
			authErr.Message = "REG.API rejected this IP address; run regru auth refresh to sync the REG.API allowlist"
			authErr.Details["provider_code"] = apiErr.Code
			return authErr
		case apiErr.Code == "RESELLER_AUTH_FAILED":
			return cli.CapabilityUnavailableWithReason(operation.Capability, "partner_only", "this REG.API function requires a partner account")
		case apiErr.AuthenticationFailure():
			return cli.ProviderAuthenticationError("REG.API")
		}
		return cli.ProviderError("REG.API", apiErr.Code, apiErr.StatusCode, apiErr.Retryable, "")
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return cli.NetworkError("REG.API", netErr.Timeout())
	}
	return err
}

func identificationCode(code string) bool {
	return code == "ESIA_IDENTIFICATION_REQUIRED" || code == "NOT_ESIA_IDENTIFIED"
}

func parseZones(value string) ([]domain.Zone, error) {
	if strings.TrimSpace(value) == "" {
		return append([]domain.Zone(nil), domain.DefaultZones...), nil
	}
	zones := []domain.Zone{}
	seen := map[domain.Zone]bool{}
	for _, token := range strings.Split(value, ",") {
		zone, err := domain.ParseZone(token)
		if err != nil {
			return nil, err
		}
		if !seen[zone] {
			seen[zone] = true
			zones = append(zones, zone)
		}
	}
	return zones, nil
}

func newUserServID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(random[:]), nil
}

func argument(operation cli.Operation, index int) string {
	if index < 0 || index >= len(operation.Arguments) {
		return ""
	}
	return operation.Arguments[index]
}

func parameter(operation cli.Operation, name string) string {
	values := operation.Parameters[name]
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func wipe(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
