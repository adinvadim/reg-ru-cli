package billing

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

func TestPortalUserBillsCaptureIsRedactedAndCompleteEnoughToRetain(t *testing.T) {
	data, err := os.ReadFile("testdata/portal-user-bills-redacted.json")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var capture struct {
		SchemaVersion string `json:"schemaVersion"`
		Request       struct {
			OperationName string `json:"operationName"`
		} `json:"request"`
		CSRF struct {
			CookieName string `json:"cookieName"`
			HeaderName string `json:"headerName"`
			Issuer     struct {
				Method string `json:"method"`
				Path   string `json:"path"`
			} `json:"issuer"`
			TokenValueRetained bool `json:"tokenValueRetained"`
		} `json:"csrf"`
		Response struct {
			Pagination struct {
				NonEmptyPageObserved bool `json:"nonEmptyPageObserved"`
				EmptyPageObserved    bool `json:"emptyPageObserved"`
				EmptyPageHasMore     bool `json:"emptyPageHasMore"`
			} `json:"pagination"`
			ItemFields map[string][]string `json:"itemFields"`
		} `json:"response"`
		Invariants struct {
			PrincipalMatchedSelectedProfile          bool `json:"principalMatchedSelectedProfile"`
			NormalizedIDMatchesBillID                bool `json:"normalizedIdMatchesBillId"`
			AllMatchedPayStatusesEqual               bool `json:"allMatchedPayStatusesEqual"`
			BillSIDStableAcrossManagedBrowserRestart bool `json:"billSidStableAcrossManagedBrowserRestart"`
			RequestedAmountMatchedPortalRow          bool `json:"requestedAmountMatchedPortalRow"`
			TwoUnpaidBillClassesObserved             bool `json:"twoUnpaidBillClassesObserved"`
		} `json:"invariants"`
		Checkout struct {
			RequestedRoute struct {
				Path      string   `json:"path"`
				QueryKeys []string `json:"queryKeys"`
			} `json:"requestedRoute"`
			BillClasses                  []json.RawMessage `json:"billClasses"`
			BillScopedMethodListObserved bool              `json:"billScopedMethodListObserved"`
			MethodSelected               bool              `json:"methodSelected"`
			PaymentSubmitted             bool              `json:"paymentSubmitted"`
			PrivateLocatorRetained       bool              `json:"privateLocatorRetained"`
		} `json:"checkout"`
	}
	if err := json.Unmarshal(data, &capture); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if capture.SchemaVersion != "regru.portal-billing-capture/v1" {
		t.Fatalf("schemaVersion = %q", capture.SchemaVersion)
	}
	if capture.Request.OperationName != "userBills" {
		t.Fatalf("operationName = %q", capture.Request.OperationName)
	}
	if capture.CSRF.CookieName != "acc-csrftoken" || capture.CSRF.HeaderName != "x-acc-csrftoken" || capture.CSRF.Issuer.Method != "GET" || capture.CSRF.Issuer.Path != "/account/issue_csrf_token" || capture.CSRF.TokenValueRetained {
		t.Fatalf("csrf = %#v", capture.CSRF)
	}
	if !capture.Response.Pagination.NonEmptyPageObserved || !capture.Response.Pagination.EmptyPageObserved || capture.Response.Pagination.EmptyPageHasMore {
		t.Fatalf("pagination = %#v", capture.Response.Pagination)
	}
	if !capture.Invariants.PrincipalMatchedSelectedProfile || !capture.Invariants.NormalizedIDMatchesBillID || !capture.Invariants.AllMatchedPayStatusesEqual || !capture.Invariants.BillSIDStableAcrossManagedBrowserRestart || !capture.Invariants.RequestedAmountMatchedPortalRow || !capture.Invariants.TwoUnpaidBillClassesObserved {
		t.Fatalf("invariants = %#v", capture.Invariants)
	}
	if capture.Checkout.RequestedRoute.Path != "/billing/payment/choose" || !slices.Equal(capture.Checkout.RequestedRoute.QueryKeys, []string{"bill_sid"}) {
		t.Fatalf("checkout route = %#v", capture.Checkout.RequestedRoute)
	}
	if len(capture.Checkout.BillClasses) != 2 || capture.Checkout.BillScopedMethodListObserved || capture.Checkout.MethodSelected || capture.Checkout.PaymentSubmitted || capture.Checkout.PrivateLocatorRetained {
		t.Fatalf("checkout capture = %#v", capture.Checkout)
	}

	fieldNames := make([]string, 0, len(capture.Response.ItemFields))
	for fieldName := range capture.Response.ItemFields {
		fieldNames = append(fieldNames, fieldName)
	}
	slices.Sort(fieldNames)
	wantFieldNames := []string{
		"amount", "bill_sid", "create_date", "description", "freezed", "id",
		"isDownloadGarantLetter", "is_prepayment", "pay_date", "pay_status",
		"pay_time", "pay_type", "pay_type_title", "state", "submode",
	}
	slices.Sort(wantFieldNames)
	if !slices.Equal(fieldNames, wantFieldNames) {
		t.Fatalf("item field names = %q, want %q", fieldNames, wantFieldNames)
	}
}
