package regapi

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

// observedLadder is the ladder REG.API returned live on 2026-11-18.
const observedLadder = `[0,450,1950,5950,11950,17950,25500,49950,69900,99900,190900,350900,450000,675000,900000,1600000,2300000,3200000,4500000,7500000,10000000]`

var nicServers = []string{"ns3-l2.nic.ru", "ns4-l2.nic.ru", "ns8-l2.nic.ru", "ns4-cloud.nic.ru", "ns8-cloud.nic.ru"}

func TestGetUserReregBidsDecodesBidsAndLadder(t *testing.T) {
	scripted := newScriptedServer(t, 200, `{"result":"success","answer":{
		"domains":[
			{"dname":"test.su","tld":"su","is_active":0,"user_bid":"5000.00","max_bid":5000,"next_price":null,"blitz_price":"5000.00","delete_date":"2099-01-01","registrar":"REGRU"},
			{"dname":"Test.RU","tld":"ru","is_active":"1","user_bid":225.00,"max_bid":"590.00","next_price":"1950.00","blitz_price":8000,"delete_date":"2099-12-31","registrar":"REGRU"}
		],
		"rereg_bids":{"ru":`+observedLadder+`,"рф":["0","450"]}}}`)
	client := testClient(t, scripted.server.URL, scripted.server.Client())
	defer client.Close()

	bids, err := client.GetUserReregBids(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(bids.Bids) != 2 || bids.Bids[0].Name != "test.ru" || !bids.Bids[0].Active || bids.Bids[0].UserBid != "225.00" ||
		bids.Bids[0].NextPrice != "1950.00" || bids.Bids[1].Active || bids.Bids[1].NextPrice != "" {
		t.Fatalf("bids = %#v", bids.Bids)
	}
	if len(bids.Ladders["ru"]) != 21 || bids.Ladders["ru"][1] != "450" || bids.Ladders["рф"][1] != "450" {
		t.Fatalf("ladders = %#v", bids.Ladders)
	}
	if scripted.paths[0] != "/api/regru2/domain/get_user_rereg_bids" {
		t.Fatalf("path = %v", scripted.paths)
	}
}

func TestGetUserReregBidsRejectsIncompleteRows(t *testing.T) {
	for _, body := range []string{
		`{"result":"success","answer":{"rereg_bids":{}}}`,
		`{"result":"success","answer":{"domains":[{"dname":"a.ru","is_active":"maybe","user_bid":"1","max_bid":"1"}]}}`,
		`{"result":"success","answer":{"domains":[{"dname":"a.ru","is_active":1,"max_bid":"1"}]}}`,
		`{"result":"success","answer":{"domains":[],"rereg_bids":{"ru":["free"]}}}`,
	} {
		scripted := newScriptedServer(t, 200, body)
		client := testClient(t, scripted.server.URL, scripted.server.Client())
		_, err := client.GetUserReregBids(context.Background())
		client.Close()
		var contract *ContractError
		if !errors.As(err, &contract) {
			t.Errorf("%s: err = %v, want contract drift", body, err)
		}
	}
}

func TestSetReregBidSendsOneBidWithProfileOrContacts(t *testing.T) {
	success := `{"result":"success","answer":{"bill_id":"1234","payment":"1950","pay_type":"prepay","pay_notes":"Amount successfully charged",
		"domains":[{"dname":"moonbot.ru","result":"success","service_id":"12345"}]}}`
	for _, request := range []ReregBidRequest{
		{Name: "moonbot.ru", Price: "1950", Nameservers: nicServers, ProfileType: "RU.PP", ProfileName: "owner"},
		{Name: "moonbot.ru", Price: "1950.00", Nameservers: nicServers, Contacts: map[string]string{"person": "Ivan I Ivanov", "country": "RU"}},
	} {
		scripted := newScriptedServer(t, 200, success)
		client := testClient(t, scripted.server.URL, scripted.server.Client())
		result, err := client.SetReregBid(context.Background(), request)
		client.Close()
		if err != nil {
			t.Fatal(err)
		}
		if result.BillID != "1234" || result.ServiceID != "12345" || result.Payment != "1950" || scripted.requests.Load() != 1 {
			t.Fatalf("result = %#v requests = %d", result, scripted.requests.Load())
		}
		input := scripted.inputs[0]
		domains, _ := input["domains"].([]any)
		item, _ := domains[0].(map[string]any)
		if scripted.paths[0] != "/api/regru2/domain/set_rereg_bids" || len(domains) != 1 || item["dname"] != "moonbot.ru" || item["price"] != float64(1950) {
			t.Fatalf("input = %#v", input)
		}
		if nss, _ := input["nss"].(map[string]any); nss["ns0"] != "ns3-l2.nic.ru" || nss["ns4"] != "ns8-cloud.nic.ru" {
			t.Fatalf("nss = %#v", input["nss"])
		}
		if request.Contacts != nil {
			if contacts, _ := input["contacts"].(map[string]any); contacts["person"] != "Ivan I Ivanov" || input["profile_type"] != nil {
				t.Fatalf("contacts input = %#v", input)
			}
		} else if input["profile_type"] != "RU.PP" || input["profile_name"] != "owner" || input["contacts"] != nil {
			t.Fatalf("profile input = %#v", input)
		}
		for _, forbidden := range []string{"instalment", "autopay"} {
			if _, present := item[forbidden]; present {
				t.Fatalf("partner-only %s was sent", forbidden)
			}
		}
	}
}

func TestSetReregBidRejectsIncompleteRequestsBeforeSending(t *testing.T) {
	scripted := newScriptedServer(t, 200, `{}`)
	client := testClient(t, scripted.server.URL, scripted.server.Client())
	defer client.Close()
	for _, request := range []ReregBidRequest{
		{Name: "a.ru", Price: "0", Nameservers: nicServers, ProfileType: "RU.PP", ProfileName: "x"},
		{Name: "a.ru", Price: "450", Nameservers: nicServers},
		{Name: "a.ru", Price: "450", Nameservers: nicServers, ProfileType: "RU.PP", ProfileName: "x", Contacts: map[string]string{"person": "x"}},
		{Name: "a.ru", Price: "450", Nameservers: nicServers[:1], ProfileType: "RU.PP", ProfileName: "x"},
	} {
		if _, err := client.SetReregBid(context.Background(), request); err == nil {
			t.Errorf("request %#v was accepted", request)
		}
	}
	if scripted.requests.Load() != 0 {
		t.Fatalf("requests = %d, want 0", scripted.requests.Load())
	}
}

func TestSetReregBidOutcomes(t *testing.T) {
	request := ReregBidRequest{Name: "moonbot.ru", Price: "450", Nameservers: nicServers, ProfileType: "RU.PP", ProfileName: "owner"}
	for _, check := range []struct {
		name      string
		status    int
		body      string
		ambiguous bool
		item      string
		api       string
	}{
		{name: "per-item refusal", status: 200, body: `{"result":"success","answer":{"bill_id":"1","payment":"0","domains":[{"dname":"moonbot.ru","result":"Invalid bid"}]}}`, item: "Invalid bid"},
		{name: "own bid already higher", status: 200, body: `{"result":"error","error_code":"YOUR_SAME_OR_MORE_BID_FOUND"}`, api: "YOUR_SAME_OR_MORE_BID_FOUND"},
		{name: "billing lock", status: 200, body: `{"result":"error","error_code":"BILLING_LOCK"}`, api: "BILLING_LOCK"},
		{name: "no bill id", status: 200, body: `{"result":"success","answer":{"payment":"450","domains":[{"dname":"moonbot.ru","result":"success"}]}}`, ambiguous: true},
		{name: "another name", status: 200, body: `{"result":"success","answer":{"bill_id":"1","payment":"450","domains":[{"dname":"other.ru","result":"success"}]}}`, ambiguous: true},
		{name: "server error", status: http.StatusBadGateway, body: `<html>`, ambiguous: true},
		{name: "undecodable success", status: 200, body: `{"result":"success","answer":"ok"}`, ambiguous: true},
	} {
		t.Run(check.name, func(t *testing.T) {
			scripted := newScriptedServer(t, check.status, check.body)
			client := testClient(t, scripted.server.URL, scripted.server.Client())
			defer client.Close()
			_, err := client.SetReregBid(context.Background(), request)
			var ambiguous *AmbiguousMutationError
			var item *DomainItemError
			var apiErr *APIError
			switch {
			case check.ambiguous && !errors.As(err, &ambiguous):
				t.Fatalf("err = %v, want ambiguous", err)
			case check.item != "" && (!errors.As(err, &item) || item.Code != check.item):
				t.Fatalf("err = %v, want item %s", err, check.item)
			case check.api != "" && (!errors.As(err, &apiErr) || apiErr.Code != check.api || errors.As(err, &ambiguous)):
				t.Fatalf("err = %v, want definitive %s", err, check.api)
			}
			if scripted.requests.Load() != 1 {
				t.Fatalf("requests = %d, want exactly 1", scripted.requests.Load())
			}
		})
	}
}
