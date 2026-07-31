package regapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
	"time"
)

func TestClientUsesDocumentedFormTransportAndDecodesBalance(t *testing.T) {
	var received url.Values
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/regru2/user/get_balance" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("content type = %q", request.Header.Get("Content-Type"))
		}
		if err := request.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		received = request.PostForm
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{
			"result":"success",
			"answer":{"currency":"RUR","prepay":"123.40","blocked":"5.00"}
		}`)
	}))
	defer server.Close()

	client := testClient(t, server.URL, server.Client())
	defer client.Close()
	balance, err := client.GetBalance(context.Background(), "RUR")
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if balance.Currency != "RUR" || balance.Prepay != "123.40" || balance.Blocked != "5.00" {
		t.Fatalf("balance = %+v", balance)
	}
	for key, expected := range map[string]string{
		"username":      "fixture-user",
		"password":      "fixture-password",
		"input_format":  "json",
		"input_data":    `{"currency":"RUR"}`,
		"output_format": "json",
	} {
		if received.Get(key) != expected {
			t.Errorf("form %s = %q, want %q", key, received.Get(key), expected)
		}
	}
}

func TestClientDecodesUnpaidInvoicePageWithoutNormalizingProviderEnums(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/regru2/bill/get_not_payed" {
			t.Errorf("path = %q", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{
			"result":"success",
			"answer":{"bills":[{
				"bill_id":"9007199254740993",
				"bill_date":"2026-07-31",
				"currency":"RUR",
				"payment":"10.00",
				"total_payment":"10.30",
				"pay_type":"yacard",
				"pay_status":"notpayed",
				"items":[{"itemtype":"service","servtype":"domain","service_id":"42","action":"renew"}]
			}]}
		}`)
	}))
	defer server.Close()

	client := testClient(t, server.URL, server.Client())
	defer client.Close()
	page, err := client.ListUnpaid(context.Background(), ListRequest{Limit: 25, Offset: 50})
	if err != nil {
		t.Fatalf("ListUnpaid: %v", err)
	}
	if page.Offset != 50 || page.Received != 1 || page.NextOffset != 51 {
		t.Fatalf("page metadata = %+v", page)
	}
	if len(page.Invoices) != 1 {
		t.Fatalf("invoices = %+v", page.Invoices)
	}
	invoice := page.Invoices[0]
	if invoice.ID != "9007199254740993" || invoice.PayType != "yacard" || invoice.PayStatus != "notpayed" {
		t.Fatalf("invoice = %+v", invoice)
	}
	if len(invoice.Items) != 1 || invoice.Items[0].ServiceID != "42" {
		t.Fatalf("items = %+v", invoice.Items)
	}
}

func TestClientPreservesBulkDeleteOutcomes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{
			"result":"success",
			"answer":{"bills":[
				{"bill_id":"1","result":"success","status":"deleted","pay_status":"notpayed"},
				{"bill_id":"2","status":"active","pay_status":"payed","error_code":"BILL_CAN_NOT_REMOVED"}
			]}
		}`)
	}))
	defer server.Close()

	client := testClient(t, server.URL, server.Client())
	defer client.Close()
	outcomes, err := client.Delete(context.Background(), []string{"1", "2"})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	want := []DeleteOutcome{
		{ID: "1", Result: "success", Status: "deleted", PayStatus: "notpayed", Successful: true},
		{ID: "2", Status: "active", PayStatus: "payed", ErrorCode: "BILL_CAN_NOT_REMOVED"},
	}
	if !reflect.DeepEqual(outcomes, want) {
		t.Fatalf("outcomes = %#v, want %#v", outcomes, want)
	}
}

func TestClientChangesPaymentTypeUsingMethodSpecificVocabulary(t *testing.T) {
	var inputData string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/regru2/bill/change_pay_type" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if err := request.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		inputData = request.PostForm.Get("input_data")
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{
			"result":"success",
			"answer":{"bills":[{
				"bill_id":"1","currency":"RUR","payment":"10.00",
				"total_payment":"10.00","old_pay_type":"bank",
				"pay_type":"prepay","pay_status":"payed"
			}]}
		}`)
	}))
	defer server.Close()

	client := testClient(t, server.URL, server.Client())
	defer client.Close()
	changes, err := client.ChangePaymentType(context.Background(), []string{"1"}, "prepay", "RUR")
	if err != nil {
		t.Fatalf("ChangePaymentType: %v", err)
	}
	if inputData != `{"bill_id":"1","currency":"RUR","pay_type":"prepay"}` {
		t.Errorf("input_data = %q", inputData)
	}
	if len(changes) != 1 || changes[0].OldPayType != "bank" || changes[0].PayStatus != "payed" {
		t.Fatalf("changes = %+v", changes)
	}
}

func TestMutationTransportFailureIsAmbiguousAndNeverRetried(t *testing.T) {
	doer := &failingDoer{}
	client := testClient(t, "https://api.invalid", doer)
	defer client.Close()
	_, err := client.Delete(context.Background(), []string{"1"})
	var ambiguous *AmbiguousMutationError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("error = %T %v", err, err)
	}
	if doer.calls != 1 {
		t.Fatalf("mutation attempts = %d", doer.calls)
	}
}

func TestMutationRequiresOneOutcomePerRequestedInvoice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{
			"result":"success",
			"answer":{"bills":[
				{"bill_id":"1","result":"success","status":"deleted","pay_status":"notpayed"}
			]}
		}`)
	}))
	defer server.Close()

	client := testClient(t, server.URL, server.Client())
	defer client.Close()
	_, err := client.Delete(context.Background(), []string{"1", "2"})
	var ambiguous *AmbiguousMutationError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("error = %T %v", err, err)
	}
}

func TestClientRejectsMalformedSuccessAndClassifiesProviderError(t *testing.T) {
	responses := []struct {
		body string
		code string
	}{
		{body: `{"result":"success","answer":{}}`},
		{body: `{"result":"error","error_code":"ACCESS_DENIED_FROM_IP","error_text":"private text"}`, code: "ACCESS_DENIED_FROM_IP"},
	}
	for _, test := range responses {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, test.body)
		}))
		client := testClient(t, server.URL, server.Client())
		_, err := client.GetBalance(context.Background(), "RUR")
		client.Close()
		server.Close()
		if test.code == "" {
			var contractErr *ContractError
			if !errors.As(err, &contractErr) {
				t.Errorf("malformed success error = %T %v", err, err)
			}
			continue
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Code != test.code {
			t.Errorf("provider error = %#v", err)
		}
	}
}

type failingDoer struct{ calls int }

func (d *failingDoer) Do(*http.Request) (*http.Response, error) {
	d.calls++
	return nil, errors.New("fixture transport failure")
}

func testClient(t *testing.T, baseURL string, doer HTTPDoer) *Client {
	t.Helper()
	client, err := New(
		[]byte("fixture-user"),
		[]byte("fixture-password"),
		ClientOptions{BaseURL: baseURL + "/api/regru2", HTTPClient: doer, RequestTimeout: time.Second},
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}
