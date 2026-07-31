package billing

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/adinvadim/reg-ru-cli/internal/cli"
	"github.com/adinvadim/reg-ru-cli/internal/credentialprocess"
	"github.com/adinvadim/reg-ru-cli/internal/provider/cloudvps"
	"github.com/adinvadim/reg-ru-cli/internal/provider/regapi"
)

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type ExecutorOptions struct {
	REGAPIBaseURL   string
	CloudVPSBaseURL string
	HTTPClient      HTTPDoer
}

type Executor struct {
	options  ExecutorOptions
	fallback cli.Executor
}

func NewExecutor(options ExecutorOptions, fallback cli.Executor) *Executor {
	if fallback == nil {
		fallback = cli.UnavailableExecutor{}
	}
	return &Executor{options: options, fallback: fallback}
}

func (e *Executor) Execute(ctx context.Context, operation cli.Operation) (cli.Result, error) {
	if !strings.HasPrefix(operation.Action, "billing.") {
		return e.fallback.Execute(ctx, operation)
	}
	switch operation.Action {
	case "billing.invoice.create":
		return cli.Result{}, cli.CapabilityUnavailable(
			"billing.invoice.create",
			"REG.API has no generic invoice-create operation; use a service-specific order or renewal workflow",
		)
	case "billing.invoice.payment-method.list":
		return cli.Result{}, cli.CapabilityUnavailable(
			"billing.checkout.capture_required",
			"bill-specific payment methods remain unavailable until an authorized redacted portal capture establishes their contract",
		)
	case "billing.invoice.payment-link":
		return cli.Result{}, cli.CapabilityUnavailable(
			"billing.checkout.capture_required",
			"browser checkout remains unavailable until an authorized redacted portal capture validates the bill-scoped handoff",
		)
	}
	if operation.Credentials == nil {
		return cli.Result{}, cli.ConfigurationError("billing credentials are not configured")
	}

	var result cli.Result
	var err error
	switch operation.Action {
	case "billing.balance":
		return e.balance(ctx, operation)
	case "billing.history":
		return e.history(ctx, operation)
	case "billing.invoice.list":
		result, err = e.invoiceList(ctx, operation)
	case "billing.invoice.show":
		result, err = e.invoiceShow(ctx, operation)
	case "billing.invoice.status":
		result, err = e.invoiceStatus(ctx, operation)
	case "billing.invoice.delete":
		result, err = e.invoiceDelete(ctx, operation)
	case "billing.invoice.payment-method.set":
		result, err = e.paymentMethodSet(ctx, operation)
	default:
		return e.fallback.Execute(ctx, operation)
	}
	if err != nil {
		return cli.Result{}, translateError(operation, err)
	}
	return result, nil
}

func (e *Executor) balance(ctx context.Context, operation cli.Operation) (cli.Result, error) {
	source := parameter(operation, "source")
	if source == "" {
		source = "all"
	}
	currency := parameter(operation, "currency")
	if currency == "" {
		currency = "RUR"
	}
	if source == "regapi" {
		balance, err := e.regapiBalance(ctx, operation, currency)
		if err != nil {
			return cli.Result{}, translateError(operation, err)
		}
		return renderBalances([]any{balance}, true, []map[string]any{{"source": "regapi2", "status": "ok"}}, nil), nil
	}
	if source == "cloudvps" {
		balance, err := e.cloudBalance(ctx, operation)
		if err != nil {
			return cli.Result{}, translateError(operation, err)
		}
		return renderBalances([]any{balance}, true, []map[string]any{{"source": "cloudvps", "status": "ok"}}, nil), nil
	}

	balances := []any{}
	sources := []map[string]any{}
	warnings := []cli.Warning{}
	var firstErr error
	regBalance, regErr := e.regapiBalance(ctx, operation, currency)
	if regErr == nil {
		balances = append(balances, regBalance)
		sources = append(sources, map[string]any{"source": "regapi2", "status": "ok"})
	} else {
		firstErr = regErr
		sources = append(sources, unavailableSource("regapi2", translateError(operation, regErr)))
		warnings = append(warnings, cli.Warning{Code: "source_unavailable", Message: "REG.API balance is unavailable"})
	}
	cloudBalance, cloudErr := e.cloudBalance(ctx, operation)
	if cloudErr == nil {
		balances = append(balances, cloudBalance)
		sources = append(sources, map[string]any{"source": "cloudvps", "status": "ok"})
	} else {
		if firstErr == nil {
			firstErr = cloudErr
		}
		sources = append(sources, unavailableSource("cloudvps", translateError(operation, cloudErr)))
		warnings = append(warnings, cli.Warning{Code: "source_unavailable", Message: "CloudVPS balance is unavailable"})
	}
	if len(balances) == 0 {
		return cli.Result{}, translateError(operation, firstErr)
	}
	return renderBalances(balances, len(warnings) == 0, sources, warnings), nil
}

func (e *Executor) regapiBalance(ctx context.Context, operation cli.Operation, currency string) (regapi.Balance, error) {
	client, err := e.regapiClient(ctx, operation)
	if err != nil {
		return regapi.Balance{}, err
	}
	defer client.Close()
	return client.GetBalance(ctx, currency)
}

func (e *Executor) cloudBalance(ctx context.Context, operation cli.Operation) (cloudvps.BalanceSnapshot, error) {
	client, err := e.cloudClient(ctx, operation)
	if err != nil {
		return cloudvps.BalanceSnapshot{}, err
	}
	defer client.Close()
	return client.GetBalanceData(ctx)
}

func (e *Executor) history(ctx context.Context, operation cli.Operation) (cli.Result, error) {
	if parameter(operation, "source") == "regapi" {
		client, err := e.regapiClient(ctx, operation)
		if err != nil {
			return cli.Result{}, translateError(operation, err)
		}
		defer client.Close()
		page, err := client.ListForPeriod(ctx, regapi.PeriodRequest{
			StartDate: parameter(operation, "start-date"), EndDate: parameter(operation, "end-date"),
			PayType: parameter(operation, "pay-type"), Limit: intParameter(operation, "limit"),
			Offset: intParameter(operation, "offset"), IncludeInactive: boolParameter(operation, "all"),
		})
		if err != nil {
			return cli.Result{}, translateError(operation, err)
		}
		return renderInvoicePage("partner invoice history", page), nil
	}
	client, err := e.cloudClient(ctx, operation)
	if err != nil {
		return cli.Result{}, translateError(operation, err)
	}
	defer client.Close()
	history, err := client.GetBillingHistory(ctx)
	if err != nil {
		return cli.Result{}, translateError(operation, err)
	}
	return renderRefillHistory(history), nil
}

func (e *Executor) invoiceList(ctx context.Context, operation cli.Operation) (cli.Result, error) {
	client, err := e.regapiClient(ctx, operation)
	if err != nil {
		return cli.Result{}, err
	}
	defer client.Close()
	page, err := client.ListUnpaid(ctx, regapi.ListRequest{
		Limit: intParameter(operation, "limit"), Offset: intParameter(operation, "offset"),
	})
	return renderInvoicePage("unpaid invoices", page), err
}

func (e *Executor) invoiceShow(ctx context.Context, operation cli.Operation) (cli.Result, error) {
	client, err := e.regapiClient(ctx, operation)
	if err != nil {
		return cli.Result{}, err
	}
	defer client.Close()
	id := argument(operation, 0)
	page, err := client.ListUnpaid(ctx, regapi.ListRequest{Limit: 1024})
	if err != nil {
		return cli.Result{}, err
	}
	for _, invoice := range page.Invoices {
		if invoice.ID == id {
			return renderInvoice(invoice), nil
		}
	}
	statuses, err := client.Status(ctx, []string{id})
	if err != nil {
		return cli.Result{}, err
	}
	if len(statuses) != 1 || statuses[0].ID != id {
		return cli.Result{}, &regapi.ContractError{Message: "REG.API invoice status lookup did not identify the requested invoice"}
	}
	return cli.Result{
		Human: fmt.Sprintf("Invoice %s: %s (status only)", id, statuses[0].PayStatus),
		Plain: []string{fmt.Sprintf("%s\t%s\tstatus-only", plain(id), plain(statuses[0].PayStatus))},
		Data: map[string]any{
			"detailAvailable": false,
			"status":          statuses[0],
		},
		Warnings: []cli.Warning{{
			Code:    "invoice_detail_unavailable",
			Message: "REG.API exposes status but no full get-by-ID detail for this invoice",
		}},
	}, nil
}

func (e *Executor) invoiceStatus(ctx context.Context, operation cli.Operation) (cli.Result, error) {
	client, err := e.regapiClient(ctx, operation)
	if err != nil {
		return cli.Result{}, err
	}
	defer client.Close()
	statuses, err := client.Status(ctx, operation.Arguments)
	if err != nil {
		return cli.Result{}, err
	}
	return renderStatuses(statuses), nil
}

func (e *Executor) invoiceDelete(ctx context.Context, operation cli.Operation) (cli.Result, error) {
	client, err := e.regapiClient(ctx, operation)
	if err != nil {
		return cli.Result{}, err
	}
	defer client.Close()
	outcomes, err := client.Delete(ctx, operation.Arguments)
	if err != nil {
		return cli.Result{}, err
	}
	for _, outcome := range outcomes {
		if !outcome.Successful {
			return cli.Result{}, cli.ProviderBulkOperationFailed("REG.API", "invoice deletion", asAny(outcomes))
		}
	}
	return renderDeleteOutcomes(outcomes), nil
}

func (e *Executor) paymentMethodSet(ctx context.Context, operation cli.Operation) (cli.Result, error) {
	client, err := e.regapiClient(ctx, operation)
	if err != nil {
		return cli.Result{}, err
	}
	defer client.Close()
	changes, err := client.ChangePaymentType(
		ctx, operation.Arguments, parameter(operation, "type"), parameter(operation, "currency"),
	)
	if err != nil {
		return cli.Result{}, err
	}
	for _, change := range changes {
		if change.ErrorCode != "" {
			return cli.Result{}, cli.ProviderBulkOperationFailed("REG.API", "payment-type change", asAny(changes))
		}
	}
	return renderPaymentChanges(changes), nil
}

func (e *Executor) regapiClient(ctx context.Context, operation cli.Operation) (*regapi.Client, error) {
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

func (e *Executor) cloudClient(ctx context.Context, operation cli.Operation) (*cloudvps.Client, error) {
	token, err := operation.Credentials.Resolve(ctx, "cloudvps.token")
	if err != nil {
		return nil, err
	}
	defer wipe(token)
	return cloudvps.New(token, cloudvps.ClientOptions{
		BaseURL: e.options.CloudVPSBaseURL, HTTPClient: e.options.HTTPClient,
		RequestTimeout: operation.RequestTimeout,
	})
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
	var regContract *regapi.ContractError
	if errors.As(err, &regContract) {
		return cli.ProviderContractDrift("REG.API")
	}
	var cloudContract *cloudvps.ContractError
	if errors.As(err, &cloudContract) {
		return cli.ProviderContractDrift("CloudVPS")
	}
	var regError *regapi.APIError
	if errors.As(err, &regError) {
		if regError.Code == "RESELLER_AUTH_FAILED" {
			return cli.CapabilityUnavailable("billing.partner_history", "REG.API invoice history requires a partner account")
		}
		if regError.AuthenticationFailure() {
			return cli.ProviderAuthenticationError("REG.API")
		}
		return cli.ProviderError("REG.API", regError.Code, regError.StatusCode, regError.Retryable, "")
	}
	var cloudError *cloudvps.APIError
	if errors.As(err, &cloudError) {
		if cloudError.StatusCode == http.StatusUnauthorized {
			return cli.ProviderAuthenticationError("CloudVPS")
		}
		return cli.ProviderError("CloudVPS", cloudError.Code, cloudError.StatusCode, cloudError.Retryable, cloudError.RequestID)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) {
		return cli.NetworkError("REG.RU billing", networkErr.Timeout() || networkErr.Temporary())
	}
	return err
}

func unavailableSource(source string, err error) map[string]any {
	code := cli.CodeInternal
	var cliErr *cli.CLIError
	if errors.As(err, &cliErr) {
		code = cliErr.Code
	} else {
		var processErr *credentialprocess.ProcessError
		if errors.As(err, &processErr) {
			switch processErr.Code {
			case "credential_process_not_configured", "credential_field_unavailable":
				code = cli.CodeCredentialRequired
			default:
				code = cli.CodeCredentialProcess
			}
		}
	}
	return map[string]any{"source": source, "status": "unavailable", "errorCode": code}
}

func renderBalances(balances []any, complete bool, sources []map[string]any, warnings []cli.Warning) cli.Result {
	lines := make([]string, 0, len(balances))
	for _, balance := range balances {
		switch value := balance.(type) {
		case regapi.Balance:
			lines = append(lines, fmt.Sprintf("regapi2\t%s\t%s\t%s\t%s", plain(value.Currency), plain(value.Prepay), plain(value.Blocked), plain(value.Credit)))
		case cloudvps.BalanceSnapshot:
			lines = append(lines, fmt.Sprintf("cloudvps\t%s\t%s\t%s\t%s\t%s", plain(value.Currency), plain(value.Cash), plain(value.Bonus), plain(value.HourlyCost), plain(value.MonthlyCost)))
		}
	}
	return cli.Result{
		Human: fmt.Sprintf("%d billing balance source(s)", len(balances)), Plain: lines,
		Data:     map[string]any{"complete": complete, "sources": sources, "balances": balances},
		Warnings: warnings,
	}
}

func renderInvoicePage(label string, page regapi.InvoicePage) cli.Result {
	lines := make([]string, 0, len(page.Invoices))
	for _, invoice := range page.Invoices {
		lines = append(lines, invoiceLine(invoice))
	}
	return cli.Result{Human: fmt.Sprintf("%d %s", len(page.Invoices), label), Plain: lines, Data: map[string]any{"page": page}}
}

func renderInvoice(invoice regapi.Invoice) cli.Result {
	return cli.Result{
		Human: fmt.Sprintf("Invoice %s: %s %s (%s)", invoice.ID, invoice.TotalPayment, invoice.Currency, invoice.PayStatus),
		Plain: []string{invoiceLine(invoice)},
		Data:  map[string]any{"detailAvailable": true, "invoice": invoice},
	}
}

func renderStatuses(statuses []regapi.InvoiceStatus) cli.Result {
	lines := make([]string, 0, len(statuses))
	for _, status := range statuses {
		lines = append(lines, fmt.Sprintf("%s\t%s", plain(status.ID), plain(status.PayStatus)))
	}
	return cli.Result{Human: fmt.Sprintf("%d invoice status result(s)", len(statuses)), Plain: lines, Data: map[string]any{"statuses": statuses}}
}

func renderRefillHistory(history cloudvps.RefillHistory) cli.Result {
	lines := make([]string, 0, len(history.Refills))
	for _, refill := range history.Refills {
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s\t%s", plain(refill.Kind), plain(refill.Amount), plain(history.Currency), plain(refill.ProviderDate)))
	}
	return cli.Result{Human: fmt.Sprintf("%d CloudVPS refill(s)", len(history.Refills)), Plain: lines, Data: map[string]any{"history": history}}
}

func renderDeleteOutcomes(outcomes []regapi.DeleteOutcome) cli.Result {
	lines := make([]string, 0, len(outcomes))
	for _, outcome := range outcomes {
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s", plain(outcome.ID), plain(outcome.Status), plain(outcome.PayStatus)))
	}
	return cli.Result{Human: fmt.Sprintf("Deleted %d invoice(s)", len(outcomes)), Plain: lines, Data: map[string]any{"outcomes": outcomes}}
}

func renderPaymentChanges(changes []regapi.PaymentChange) cli.Result {
	lines := make([]string, 0, len(changes))
	for _, change := range changes {
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s\t%s", plain(change.ID), plain(change.PayType), plain(change.Currency), plain(change.PayStatus)))
	}
	return cli.Result{Human: fmt.Sprintf("Changed payment type for %d invoice(s)", len(changes)), Plain: lines, Data: map[string]any{"outcomes": changes}}
}

func invoiceLine(invoice regapi.Invoice) string {
	return fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s", plain(invoice.ID), plain(invoice.CreatedAt), plain(invoice.Currency), plain(invoice.TotalPayment), plain(invoice.PayType), plain(invoice.PayStatus))
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

func intParameter(operation cli.Operation, name string) int {
	value, _ := strconv.Atoi(parameter(operation, name))
	return value
}

func boolParameter(operation cli.Operation, name string) bool {
	value, _ := strconv.ParseBool(parameter(operation, name))
	return value
}

func plain(value string) string {
	return strings.NewReplacer(`\`, `\\`, "\t", `\t`, "\n", `\n`, "\r", `\r`).Replace(value)
}

func asAny[T any](values []T) []any {
	result := make([]any, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}

func wipe(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
