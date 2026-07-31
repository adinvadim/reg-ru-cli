package billing

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/adinvadim/reg-ru-cli/internal/profile"
	"github.com/adinvadim/reg-ru-cli/internal/provider/portal/session"
)

func TestPortalBillingHistoryAcceptsEmptyAndNonEmptyCapturedShapes(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		result string
		count  int
	}{
		{name: "empty", result: `{"state":"available","items":[]}`},
		{name: "non-empty", result: `{"state":"available","items":[{"id":"42","amount":"100","state":"notpaid","payStatus":"notpayed","freezed":false,"isPrepayment":true}]}`, count: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			broker := &fakeBillingSessionBroker{result: json.RawMessage(testCase.result)}
			control := NewPortalBillingControlPlane(broker)
			history, err := control.History(context.Background(), billingPortalAccount())
			if err != nil {
				t.Fatalf("History() error = %v", err)
			}
			if len(history.Invoices) != testCase.count {
				t.Fatalf("invoice count = %d, want %d", len(history.Invoices), testCase.count)
			}
			if broker.program != programBillingHistory || broker.handoffs != 0 || broker.reads != 1 {
				t.Fatalf("broker calls = %+v", broker)
			}
		})
	}
}

func TestPortalBillingHistoryFailsClosedOnDuplicateOrMalformedRows(t *testing.T) {
	for _, result := range []string{
		`{"state":"available","items":[{"id":"42","amount":"100","state":"notpaid","payStatus":"notpayed","freezed":false},{"id":"42","amount":"100","state":"notpaid","payStatus":"notpayed","freezed":false}]}`,
		`{"state":"available","items":[{"id":"opaque","amount":"100","state":"notpaid","payStatus":"notpayed","freezed":false}]}`,
		`{"state":"available","items":null}`,
	} {
		control := NewPortalBillingControlPlane(&fakeBillingSessionBroker{result: json.RawMessage(result)})
		_, err := control.History(context.Background(), billingPortalAccount())
		if !isBillingPortalKind(err, PortalContract) {
			t.Errorf("History() error = %v, want contract error", err)
		}
	}
}

func TestPortalBillingCheckoutUsesVisibleHandoffAndReturnsNoRoute(t *testing.T) {
	broker := &fakeBillingSessionBroker{result: json.RawMessage(`{"state":"browser-opened","shareable":false}`)}
	control := NewPortalBillingControlPlane(broker)
	handoff, err := control.OpenCheckout(context.Background(), billingPortalAccount(), "42")
	if err != nil {
		t.Fatalf("OpenCheckout() error = %v", err)
	}
	if broker.handoffs != 1 || broker.reads != 0 || broker.program != programBillingCheckout {
		t.Fatalf("broker calls = %+v", broker)
	}
	if handoff.Handoff != "browser_opened" || handoff.Destination != "reg-ru-checkout" || handoff.Shareable || handoff.ExpiresAt != nil {
		t.Fatalf("handoff = %#v", handoff)
	}
	encoded, err := json.Marshal(handoff)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, forbidden := range []string{"bill_sid", "/billing/", "https://"} {
		if json.Valid(encoded) && containsBytes(encoded, forbidden) {
			t.Errorf("handoff output contains %q: %s", forbidden, encoded)
		}
	}
}

func TestPortalBillingCheckoutClassifiesExpiredSessionAndNonPayableBill(t *testing.T) {
	expired := NewPortalBillingControlPlane(&fakeBillingSessionBroker{
		err: &session.Error{Code: session.CodeSessionLost, Err: errors.New("expired")},
	})
	_, err := expired.OpenCheckout(context.Background(), billingPortalAccount(), "42")
	if !session.IsCode(err, session.CodeSessionLost) {
		t.Fatalf("expired error = %v", err)
	}

	notPayable := NewPortalBillingControlPlane(&fakeBillingSessionBroker{
		result: json.RawMessage(`{"state":"not-payable"}`),
	})
	_, err = notPayable.OpenCheckout(context.Background(), billingPortalAccount(), "42")
	var portalErr *PortalError
	if !errors.As(err, &portalErr) || portalErr.Kind != PortalDomain || portalErr.Code != "not-payable" {
		t.Fatalf("not-payable error = %#v", err)
	}
}

func TestPortalBillingStateMappingFailsClosedForRouteDriftAndUnknownStates(t *testing.T) {
	for _, state := range []string{"drift", "unknown-future-state", ""} {
		err := billingPortalStateError(billingPortalEnvelope{State: state})
		if !isBillingPortalKind(err, PortalContract) {
			t.Errorf("state %q error = %v, want contract error", state, err)
		}
	}
}

func billingPortalAccount() profile.Account {
	return profile.Account{
		ID:     "p_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		Portal: profile.Portal{SessionRef: "s_aaaaaaaaaaaaaaaaaaaaaaaaaa"},
	}
}

type fakeBillingSessionBroker struct {
	result   json.RawMessage
	err      error
	program  session.ProgramID
	reads    int
	handoffs int
}

func (b *fakeBillingSessionBroker) WithSession(_ context.Context, _ session.Profile, use func(session.PageExecutor) error) error {
	b.reads++
	if b.err != nil {
		return b.err
	}
	return use((*fakeBillingPage)(b))
}

func (b *fakeBillingSessionBroker) Handoff(_ context.Context, _ session.Profile, use func(session.PageExecutor) error) error {
	b.handoffs++
	if b.err != nil {
		return b.err
	}
	return use((*fakeBillingPage)(b))
}

type fakeBillingPage fakeBillingSessionBroker

func (p *fakeBillingPage) RunJSON(_ context.Context, program session.ProgramID, _ json.RawMessage, result *json.RawMessage) error {
	p.program = program
	*result = append((*result)[:0], p.result...)
	return nil
}

func containsBytes(value []byte, substring string) bool {
	for index := 0; index+len(substring) <= len(value); index++ {
		if string(value[index:index+len(substring)]) == substring {
			return true
		}
	}
	return false
}
