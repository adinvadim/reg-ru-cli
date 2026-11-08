package regapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
)

type scriptedServer struct {
	server   *httptest.Server
	requests atomic.Int32
	inputs   []map[string]any
	paths    []string
}

func newScriptedServer(t *testing.T, status int, body string) *scriptedServer {
	t.Helper()
	scripted := &scriptedServer{}
	scripted.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		scripted.requests.Add(1)
		if err := request.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		var input map[string]any
		if err := json.Unmarshal([]byte(request.PostForm.Get("input_data")), &input); err != nil {
			t.Errorf("input_data: %v", err)
		}
		scripted.inputs = append(scripted.inputs, input)
		scripted.paths = append(scripted.paths, request.URL.Path)
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(status)
		_, _ = io.WriteString(writer, body)
	}))
	t.Cleanup(scripted.server.Close)
	return scripted
}

func TestGetDomainPricesAcceptsNumbersAndStrings(t *testing.T) {
	scripted := newScriptedServer(t, 200, `{"result":"success","answer":{"currency":"RUR","price_group":"Retail","prices":{
		"ru":{"reg_price":169,"renew_price":"1199.00","retail_reg_price":226,"retail_renew_price":1199,"reg_min_period":1},
		"рф":{"reg_price":"169","renew_price":"1199"},
		"com":{"reg_price":1905}
	}}}`)
	client := testClient(t, scripted.server.URL, scripted.server.Client())
	defer client.Close()

	prices, err := client.GetDomainPrices(context.Background(), "RUR")
	if err != nil {
		t.Fatal(err)
	}
	if got := prices["ru"]; got.RegPrice != "169" || got.RenewPrice != "1199.00" || got.MinPeriod != "1" || got.Currency != "RUR" {
		t.Fatalf("ru = %#v", got)
	}
	if prices["рф"].RegPrice != "169" || prices["com"].RenewPrice != "" {
		t.Fatalf("prices = %#v", prices)
	}
	if scripted.paths[0] != "/api/regru2/domain/get_prices" || scripted.inputs[0]["show_renew_data"] != float64(1) {
		t.Fatalf("request = %v %v", scripted.paths, scripted.inputs)
	}
}

func TestCreateDomainSendsBalanceOnlyOrderOnce(t *testing.T) {
	scripted := newScriptedServer(t, 200, `{"result":"success","answer":{
		"bill_id":1234,"payment":"169.00","pay_type":"prepay","pay_notes":"Amount successfully charged",
		"domains":[{"dname":"xn--80ahqcjlq.xn--p1ai","result":"success","service_id":"777"}]}}`)
	client := testClient(t, scripted.server.URL, scripted.server.Client())
	defer client.Close()

	result, err := client.CreateDomain(context.Background(), DomainCreateRequest{
		Name: "xn--80ahqcjlq.xn--p1ai", ProfileType: "RU.PP", ProfileName: "owner",
		Nameservers: []string{"ns3-l2.nic.ru", "ns4-l2.nic.ru", "ns8-l2.nic.ru", "ns4-cloud.nic.ru", "ns8-cloud.nic.ru"},
		UserServID:  "0123456789abcdef0123456789abcdef",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ServiceID != "777" || result.BillID != "1234" || result.Payment != "169.00" {
		t.Fatalf("result = %#v", result)
	}
	input := scripted.inputs[0]
	want := map[string]any{
		"domain_name": "xn--80ahqcjlq.xn--p1ai", "period": float64(1), "profile_type": "RU.PP", "profile_name": "owner",
		"user_servid": "0123456789abcdef0123456789abcdef", "pay_type": "prepay",
		"nss": map[string]any{"ns0": "ns3-l2.nic.ru", "ns1": "ns4-l2.nic.ru", "ns2": "ns8-l2.nic.ru", "ns3": "ns4-cloud.nic.ru", "ns4": "ns8-cloud.nic.ru"},
	}
	if !reflect.DeepEqual(input, want) {
		t.Fatalf("input = %#v\nwant %#v", input, want)
	}
	if _, exists := input["ok_if_no_money"]; exists {
		t.Fatal("ok_if_no_money must never be sent")
	}
}

func TestCreateDomainOutcomes(t *testing.T) {
	request := DomainCreateRequest{
		Name: "domkray.ru", ProfileType: "RU.PP", ProfileName: "owner",
		Nameservers: []string{"ns1.example.com", "ns2.example.com"}, UserServID: "0123456789abcdef0123456789abcdef",
	}
	cases := []struct {
		name   string
		status int
		body   string
		check  func(error) bool
	}{
		{"not enough money is a recognised refusal", 200, `{"result":"error","error_code":"NOT_ENOUGH_MONEY","error_text":"x"}`, func(err error) bool {
			var apiErr *APIError
			return errors.As(err, &apiErr) && apiErr.Code == "NOT_ENOUGH_MONEY"
		}},
		{"per-item refusal", 200, `{"result":"success","answer":{"domains":[{"dname":"domkray.ru","result":"Unavailable domain name","error_code":"DOMAIN_ALREADY_EXISTS"}]}}`, func(err error) bool {
			var itemErr *DomainItemError
			return errors.As(err, &itemErr) && itemErr.Code == "DOMAIN_ALREADY_EXISTS"
		}},
		{"server error is ambiguous", 502, `bad gateway`, isAmbiguous},
		{"success without service id is ambiguous", 200, `{"result":"success","answer":{"bill_id":"1","payment":"169","domains":[{"dname":"domkray.ru","result":"success"}]}}`, isAmbiguous},
		{"answer for another name is ambiguous", 200, `{"result":"success","answer":{"domains":[{"dname":"other.ru","result":"success","service_id":"5"}]}}`, isAmbiguous},
		{"undecodable success is ambiguous", 200, `{"result":"success","answer":"nope"}`, isAmbiguous},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			scripted := newScriptedServer(t, testCase.status, testCase.body)
			client := testClient(t, scripted.server.URL, scripted.server.Client())
			defer client.Close()
			_, err := client.CreateDomain(context.Background(), request)
			if !testCase.check(err) {
				t.Fatalf("error = %#v", err)
			}
			if scripted.requests.Load() != 1 {
				t.Fatalf("requests = %d; domain/create must be sent exactly once", scripted.requests.Load())
			}
		})
	}
}

func isAmbiguous(err error) bool {
	var ambiguous *AmbiguousMutationError
	return errors.As(err, &ambiguous)
}

func TestServiceByUserServID(t *testing.T) {
	found := newScriptedServer(t, 200, `{"result":"success","answer":{"services":[{"service_id":777,"dname":"domkray.ru","servtype":"domain","state":"N"}]}}`)
	client := testClient(t, found.server.URL, found.server.Client())
	info, ok, err := client.ServiceByUserServID(context.Background(), "0123456789abcdef0123456789abcdef")
	client.Close()
	if err != nil || !ok || info.ServiceID != "777" || info.State != "N" {
		t.Fatalf("found = %#v %v %v", info, ok, err)
	}
	if found.inputs[0]["user_servid"] != "0123456789abcdef0123456789abcdef" || found.paths[0] != "/api/regru2/service/get_info" {
		t.Fatalf("request = %v %v", found.paths, found.inputs)
	}

	missing := newScriptedServer(t, 200, `{"result":"error","error_code":"SERVICE_NOT_FOUND"}`)
	client = testClient(t, missing.server.URL, missing.server.Client())
	_, ok, err = client.ServiceByUserServID(context.Background(), "0123456789abcdef0123456789abcdef")
	client.Close()
	if err != nil || ok {
		t.Fatalf("missing = %v %v", ok, err)
	}

	unknownCode := newScriptedServer(t, 200, `{"result":"error","error_code":"SOMETHING_ELSE"}`)
	client = testClient(t, unknownCode.server.URL, unknownCode.server.Client())
	_, _, err = client.ServiceByUserServID(context.Background(), "0123456789abcdef0123456789abcdef")
	client.Close()
	if err == nil {
		t.Fatal("an unrecognised lookup error must not read as not-found")
	}
}

func TestCheckDomainsAndListAndNameservers(t *testing.T) {
	check := newScriptedServer(t, 200, `{"result":"success","answer":{"domains":[
		{"dname":"free.ru","result":"Available"},
		{"dname":"taken.ru","result":"Domain already exists","error_code":"DOMAIN_ALREADY_EXISTS"},
		{"dname":"prem.com","result":"Available","is_premium":1,"price":"9000","renew_price":"9000","currency":"RUR"}]}}`)
	client := testClient(t, check.server.URL, check.server.Client())
	checks, err := client.CheckDomains(context.Background(), []string{"free.ru", "taken.ru", "prem.com"})
	client.Close()
	if err != nil || len(checks) != 3 || !checks[0].Available || checks[1].Available || checks[1].ErrorCode != "DOMAIN_ALREADY_EXISTS" || !checks[2].Premium || checks[2].Price != "9000" {
		t.Fatalf("checks = %#v, %v", checks, err)
	}

	list := newScriptedServer(t, 200, `{"result":"success","answer":{"services":[{"service_id":"1","dname":"domkray.ru","servtype":"domain","state":"A","expiration_date":"2027-11-07"}]}}`)
	client = testClient(t, list.server.URL, list.server.Client())
	services, err := client.ListDomains(context.Background())
	client.Close()
	if err != nil || len(services) != 1 || services[0].Name != "domkray.ru" || list.inputs[0]["servtype"] != "domain" {
		t.Fatalf("services = %#v, %v", services, err)
	}

	esia := newScriptedServer(t, 200, `{"result":"success","answer":{"domains":[{"dname":"domkray.ru","result":"error","error_code":"ESIA_IDENTIFICATION_REQUIRED"}]}}`)
	client = testClient(t, esia.server.URL, esia.server.Client())
	_, err = client.UpdateNameservers(context.Background(), "domkray.ru", []string{"ns1.example.com", "ns2.example.com"})
	client.Close()
	var itemErr *DomainItemError
	if !errors.As(err, &itemErr) || itemErr.Code != "ESIA_IDENTIFICATION_REQUIRED" || esia.requests.Load() != 1 {
		t.Fatalf("update_nss error = %#v", err)
	}
}
