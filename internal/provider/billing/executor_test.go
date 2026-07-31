package billing

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adinvadim/reg-ru-cli/internal/cli"
	"github.com/adinvadim/reg-ru-cli/internal/credentialprocess"
	"github.com/adinvadim/reg-ru-cli/internal/profile"
	"github.com/adinvadim/reg-ru-cli/internal/provider/regapi"
)

func TestExecutorReturnsSourceDiscriminatedBalances(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/regapi/user/get_balance":
			_, _ = io.WriteString(writer, `{"result":"success","answer":{"currency":"RUR","prepay":"25.00"}}`)
		case "/cloud/v1/balance_data":
			_, _ = io.WriteString(writer, `{"balance_data":{"balance":10.50,"bonus_balance":2,"days_left":3,"detalization":[],"hourly_cost":0.5,"hours_left":72,"monthly_cost":360}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	executor := NewExecutor(ExecutorOptions{
		REGAPIBaseURL:   server.URL + "/regapi",
		CloudVPSBaseURL: server.URL + "/cloud",
		HTTPClient:      server.Client(),
	}, nil)
	result, err := executor.Execute(context.Background(), operation("billing.balance", nil, map[string][]string{
		"source": {"all"}, "currency": {"RUR"},
	}, &credentialMap{values: map[string][]byte{
		"regapi.username": []byte("fixture-user"), "regapi.password": []byte("fixture-password"),
		"cloudvps.token": []byte("fixture-token"),
	}}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	data := result.Data.(map[string]any)
	if data["complete"] != true {
		t.Fatalf("data = %#v", data)
	}
	balances := data["balances"].([]any)
	if len(balances) != 2 {
		t.Fatalf("balances = %#v", balances)
	}
	if len(result.Plain) != 2 || len(result.Warnings) != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestExecutorReturnsPartialBalanceWithoutSubstitutingSources(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/regapi/user/get_balance" {
			t.Fatalf("unexpected request path %q", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"result":"success","answer":{"currency":"RUR","prepay":"25.00"}}`)
	}))
	defer server.Close()

	executor := NewExecutor(ExecutorOptions{
		REGAPIBaseURL: server.URL + "/regapi", HTTPClient: server.Client(),
	}, nil)
	result, err := executor.Execute(context.Background(), operation("billing.balance", nil, map[string][]string{
		"source": {"all"}, "currency": {"RUR"},
	}, &credentialMap{values: map[string][]byte{
		"regapi.username": []byte("fixture-user"), "regapi.password": []byte("fixture-password"),
	}}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	data := result.Data.(map[string]any)
	if data["complete"] != false || len(data["balances"].([]any)) != 1 {
		t.Fatalf("data = %#v", data)
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Code != "source_unavailable" {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
}

func TestExecutorInvoiceShowKeepsStatusOnlyDistinctFromDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/regapi/bill/get_not_payed":
			_, _ = io.WriteString(writer, `{"result":"success","answer":{"bills":[]}}`)
		case "/regapi/bill/nop":
			_, _ = io.WriteString(writer, `{"result":"success","answer":{"bills":[{"bill_id":"42","pay_status":"payed"}]}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	executor := NewExecutor(ExecutorOptions{REGAPIBaseURL: server.URL + "/regapi", HTTPClient: server.Client()}, nil)
	result, err := executor.Execute(context.Background(), operation("billing.invoice.show", []string{"42"}, nil, regapiCredentials()))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	data := result.Data.(map[string]any)
	if data["detailAvailable"] != false || data["status"] == nil {
		t.Fatalf("data = %#v", data)
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Code != "invoice_detail_unavailable" {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
}

func TestExecutorReportsEveryBulkDeleteOutcomeAndFailsOverall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"result":"success","answer":{"bills":[
			{"bill_id":"1","result":"success","status":"deleted","pay_status":"notpayed"},
			{"bill_id":"2","status":"active","pay_status":"payed","error_code":"BILL_CAN_NOT_REMOVED"}
		]}}`)
	}))
	defer server.Close()

	executor := NewExecutor(ExecutorOptions{REGAPIBaseURL: server.URL + "/regapi", HTTPClient: server.Client()}, nil)
	_, err := executor.Execute(context.Background(), operation("billing.invoice.delete", []string{"1", "2"}, nil, regapiCredentials()))
	var cliErr *cli.CLIError
	if !errors.As(err, &cliErr) || cliErr.Code != cli.CodeNetwork {
		t.Fatalf("error = %#v", err)
	}
	outcomes, ok := cliErr.Details["outcomes"].([]any)
	if !ok || len(outcomes) != 2 {
		t.Fatalf("details = %#v", cliErr.Details)
	}
}

func TestExecutorFailsClosedForUnavailableInvoiceCreationAndMethodList(t *testing.T) {
	resolver := credentialMap{}
	executor := NewExecutor(ExecutorOptions{}, nil)
	for _, action := range []string{
		"billing.invoice.create",
		"billing.invoice.payment-method.list",
	} {
		_, err := executor.Execute(context.Background(), operation(action, []string{"42"}, nil, &resolver))
		var cliErr *cli.CLIError
		if !errors.As(err, &cliErr) || cliErr.Code != cli.CodeCapability {
			t.Errorf("%s error = %#v", action, err)
		}
	}
	if resolver.calls != 0 {
		t.Fatalf("gated capabilities resolved credentials %d time(s)", resolver.calls)
	}
}

func TestExecutorEnrichesREGAPIInvoiceOnlyWhenPortalInvariantsMatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"result":"success","answer":{"bills":[{"bill_id":"42","bill_date":"2026-07-31","currency":"RUR","payment":"100.00","total_payment":"100.00","pay_type":"card","pay_status":"notpayed","items":[]}]}}`)
	}))
	defer server.Close()

	portal := &fakePortalControl{history: PortalHistory{Invoices: []PortalInvoice{{
		ID: "42", Amount: "100", State: "notpaid", PayStatus: "notpayed", IsPrepayment: true,
	}}}}
	executor := NewExecutor(ExecutorOptions{
		REGAPIBaseURL: server.URL, HTTPClient: server.Client(),
		Profiles: billingProfiles(), Portal: portal,
	}, nil)
	result, err := executor.Execute(context.Background(), operation("billing.invoice.list", nil, map[string][]string{
		"limit": {"100"}, "offset": {"0"},
	}, regapiCredentials()))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	data := result.Data.(map[string]any)
	enrichments := data["portalEnrichment"].([]portalEnrichment)
	if len(enrichments) != 1 || !enrichments[0].Available || !enrichments[0].Payable || !enrichments[0].IsPrepayment {
		t.Fatalf("enrichments = %#v", enrichments)
	}
	if portal.historyCalls != 1 {
		t.Fatalf("history calls = %d", portal.historyCalls)
	}
}

func TestExecutorDiscardsConflictingPortalEnrichmentWithoutLosingREGAPIResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"result":"success","answer":{"bills":[{"bill_id":"42","bill_date":"2026-07-31","currency":"RUR","payment":"100.00","total_payment":"100.00","pay_type":"card","pay_status":"notpayed","items":[]}]}}`)
	}))
	defer server.Close()

	executor := NewExecutor(ExecutorOptions{
		REGAPIBaseURL: server.URL, HTTPClient: server.Client(), Profiles: billingProfiles(),
		Portal: &fakePortalControl{history: PortalHistory{Invoices: []PortalInvoice{{
			ID: "42", Amount: "101", State: "notpaid", PayStatus: "notpayed",
		}}}},
	}, nil)
	result, err := executor.Execute(context.Background(), operation("billing.invoice.list", nil, map[string][]string{
		"limit": {"100"}, "offset": {"0"},
	}, regapiCredentials()))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	data := result.Data.(map[string]any)
	if data["page"].(regapi.InvoicePage).Invoices[0].ID != "42" || data["portalEnrichment"] != nil {
		t.Fatalf("data = %#v", data)
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Code != "private_contract_incompatible" {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
}

func TestExecutorReturnsNonShareableVisibleCheckoutHandoffWithoutResolvingCredentials(t *testing.T) {
	portal := &fakePortalControl{handoff: CheckoutHandoff{
		Handoff: "browser_opened", Destination: "reg-ru-checkout", Shareable: false,
	}}
	resolver := &credentialMap{}
	executor := NewExecutor(ExecutorOptions{Profiles: billingProfiles(), Portal: portal}, nil)
	result, err := executor.Execute(context.Background(), operation(
		"billing.invoice.payment-link", []string{"42"}, nil, resolver,
	))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resolver.calls != 0 || portal.checkoutID != "42" {
		t.Fatalf("resolver calls = %d; checkout ID = %q", resolver.calls, portal.checkoutID)
	}
	handoff := result.Data.(CheckoutHandoff)
	if handoff.Handoff != "browser_opened" || handoff.Shareable || handoff.ExpiresAt != nil {
		t.Fatalf("handoff = %#v", handoff)
	}
}

type credentialMap struct {
	values map[string][]byte
	calls  int
}

func (r *credentialMap) Resolve(_ context.Context, key string) ([]byte, error) {
	r.calls++
	value, exists := r.values[key]
	if !exists {
		return nil, &credentialprocess.ProcessError{Code: "credential_field_unavailable"}
	}
	return append([]byte(nil), value...), nil
}

func regapiCredentials() *credentialMap {
	return &credentialMap{values: map[string][]byte{
		"regapi.username": []byte("fixture-user"),
		"regapi.password": []byte("fixture-password"),
	}}
}

func operation(action string, arguments []string, parameters map[string][]string, credentials cli.CredentialResolver) cli.Operation {
	return cli.Operation{
		Action: action, Capability: "billing", Account: "fixture", ProfileID: "p_fixture",
		Arguments: arguments, Parameters: parameters, RequestTimeout: 0, Credentials: credentials,
	}
}

type fakePortalControl struct {
	history      PortalHistory
	historyErr   error
	handoff      CheckoutHandoff
	handoffErr   error
	historyCalls int
	checkoutID   string
}

func (f *fakePortalControl) History(context.Context, profile.Account) (PortalHistory, error) {
	f.historyCalls++
	return f.history, f.historyErr
}

func (f *fakePortalControl) OpenCheckout(_ context.Context, _ profile.Account, invoiceID string) (CheckoutHandoff, error) {
	f.checkoutID = invoiceID
	return f.handoff, f.handoffErr
}

func billingProfiles() *profile.MemoryRepository {
	return profile.NewMemoryRepository(profile.Config{Accounts: map[string]profile.Account{
		"fixture": {
			ID: "p_fixture", Provider: "reg.ru",
			Portal: profile.Portal{SessionRef: "s_fixture"},
		},
	}})
}
