package domains

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adinvadim/reg-ru-cli/internal/cli"
	"github.com/adinvadim/reg-ru-cli/internal/domain"
	"github.com/adinvadim/reg-ru-cli/internal/profile"
	"github.com/adinvadim/reg-ru-cli/internal/provider/registry"
)

const (
	testProfileID  = "p_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	testUserServID = "0123456789abcdef0123456789abcdef"
	pricesBody     = `{"result":"success","answer":{"currency":"RUR","prices":{"ru":{"reg_price":169,"renew_price":1199,"reg_min_period":1},"com":{"reg_price":1905,"renew_price":1905},"su":{"reg_price":669}}}}`
	balanceBody    = `{"result":"success","answer":{"currency":"RUR","prepay":"300.00"}}`
	nicNSSBody     = `{"result":"success","answer":{"domains":[{"dname":"domkray.ru","nss":[{"ns":"ns3-l2.nic.ru"},{"ns":"ns4-l2.nic.ru"},{"ns":"ns8-l2.nic.ru"},{"ns":"ns4-cloud.nic.ru"},{"ns":"ns8-cloud.nic.ru."}]}]}}`
	createBody     = `{"result":"success","answer":{"bill_id":"55","payment":"169","pay_type":"prepay","domains":[{"dname":"domkray.ru","result":"success","service_id":"777"}]}}`
)

type fakeRegistry struct {
	mu     sync.Mutex
	states []registry.State
	calls  int
}

func (f *fakeRegistry) Check(_ context.Context, names []domain.Name) []registry.Verdict {
	f.mu.Lock()
	defer f.mu.Unlock()
	verdicts := make([]registry.Verdict, len(names))
	for index, name := range names {
		state := registry.StateLikelyFree
		if f.calls < len(f.states) {
			state = f.states[f.calls]
		}
		f.calls++
		verdicts[index] = registry.Verdict{Name: name, State: state, Source: registry.SourceTCI, CheckedAt: time.Unix(0, 0)}
	}
	return verdicts
}

type fakeCredentials struct{ resolved int }

func (f *fakeCredentials) Resolve(_ context.Context, key string) ([]byte, error) {
	f.resolved++
	return []byte("secret-" + key), nil
}

type regapiScript struct {
	t         *testing.T
	mu        sync.Mutex
	responses map[string][]scripted
	calls     []string
	server    *httptest.Server
}

type scripted struct {
	status int
	body   string
	// onCall runs when the request arrives; hang then holds it until the client gives up.
	onCall func()
	hang   bool
}

func newREGAPI(t *testing.T, responses map[string][]scripted) *regapiScript {
	t.Helper()
	if _, exists := responses["domain/get_nss"]; !exists {
		responses["domain/get_nss"] = ok(nicNSSBody)
	}
	script := &regapiScript{t: t, responses: responses}
	script.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		operation := strings.TrimPrefix(request.URL.Path, "/api/regru2/")
		script.mu.Lock()
		script.calls = append(script.calls, operation)
		queue := script.responses[operation]
		if len(queue) == 0 {
			script.mu.Unlock()
			t.Errorf("unexpected REG.API call %s", operation)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		response := queue[0]
		if len(queue) > 1 {
			script.responses[operation] = queue[1:]
		}
		script.mu.Unlock()
		if response.onCall != nil {
			response.onCall()
		}
		if response.hang {
			_, _ = io.Copy(io.Discard, request.Body)
			<-request.Context().Done()
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(response.status)
		_, _ = io.WriteString(writer, response.body)
	}))
	t.Cleanup(script.server.Close)
	return script
}

func (s *regapiScript) count(operation string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := 0
	for _, call := range s.calls {
		if call == operation {
			total++
		}
	}
	return total
}

func ok(body string) []scripted { return []scripted{{status: 200, body: body}} }

func testExecutor(t *testing.T, script *regapiScript, reg Registry, settings profile.Domain) *Executor {
	t.Helper()
	profiles := profile.NewMemoryRepository(profile.Config{Accounts: map[string]profile.Account{
		"personal": {ID: testProfileID, Provider: "reg.ru", Domain: settings},
	}})
	options := ExecutorOptions{
		Profiles: profiles, Registry: reg,
		NewUserServID:    func() (string, error) { return testUserServID, nil },
		ReconcileTimeout: 2 * time.Second,
	}
	if script != nil {
		options.REGAPIBaseURL = script.server.URL + "/api/regru2"
		options.HTTPClient = script.server.Client()
	}
	return NewExecutor(options, nil)
}

var ownerSettings = profile.Domain{ContactProfileType: "RU.PP", ContactProfileName: "owner"}

func buyOperation(force bool, parameters map[string][]string) (cli.Operation, *fakeCredentials) {
	credentials := &fakeCredentials{}
	if parameters == nil {
		parameters = map[string][]string{}
	}
	if _, exists := parameters["ns"]; !exists {
		parameters["ns"] = []string{"nic"}
	}
	return cli.Operation{
		Capability: "domain.buy", Action: "domain.buy", Account: "personal", ProfileID: testProfileID,
		Arguments: []string{"domkray.ru"}, Parameters: parameters, Force: force,
		RequestTimeout: time.Second, Credentials: credentials,
	}, credentials
}

func cliCode(t *testing.T, err error) *cli.CLIError {
	t.Helper()
	var cliErr *cli.CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("error = %#v, want *cli.CLIError", err)
	}
	return cliErr
}

func TestBuyWithinCapsProceedsWithoutPromptAndSendsOnce(t *testing.T) {
	script := newREGAPI(t, map[string][]scripted{
		"domain/get_prices": ok(pricesBody), "user/get_balance": ok(balanceBody), "domain/create": ok(createBody),
	})
	executor := testExecutor(t, script, &fakeRegistry{}, ownerSettings)
	operation, _ := buyOperation(false, nil)

	result, err := executor.Execute(context.Background(), operation)
	if err != nil {
		t.Fatalf("buy error = %v", err)
	}
	data := result.Data.(map[string]any)
	if data["serviceId"] != "777" || data["outcome"] != "committed" || data["userServid"] != testUserServID || data["reconciled"] != false ||
		data["nameserversConfirmed"] != true || len(result.Warnings) != 0 {
		t.Fatalf("data = %#v", data)
	}
	if script.count("domain/create") != 1 {
		t.Fatalf("calls = %v", script.calls)
	}
}

func TestBuySpendingPolicy(t *testing.T) {
	cases := []struct {
		name       string
		prices     string
		settings   profile.Domain
		parameters map[string][]string
		force      bool
		code       string
		creates    int
	}{
		{"profile cap below price needs force", pricesBody, profile.Domain{ContactProfileType: "RU.PP", ContactProfileName: "owner", MaxPrice: "100"}, nil, false, cli.CodePriceCapExceeded, 0},
		{"flag overrides profile cap", pricesBody, profile.Domain{ContactProfileType: "RU.PP", ContactProfileName: "owner", MaxPrice: "100"}, map[string][]string{"max-price": {"200"}}, false, "", 1},
		{"renewal above default cap needs force", strings.Replace(pricesBody, `"renew_price":1199`, `"renew_price":3075`, 1), ownerSettings, nil, false, cli.CodePriceCapExceeded, 0},
		{"force buys above caps", strings.Replace(pricesBody, `"renew_price":1199`, `"renew_price":3075`, 1), ownerSettings, nil, true, "", 1},
		{"unknown renewal refuses even with force", strings.Replace(pricesBody, `,"renew_price":1199`, ``, 1), ownerSettings, nil, true, cli.CodeSpendingPolicyRefused, 0},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			responses := map[string][]scripted{"domain/get_prices": ok(testCase.prices), "user/get_balance": ok(balanceBody)}
			if testCase.creates > 0 {
				responses["domain/create"] = ok(createBody)
			}
			script := newREGAPI(t, responses)
			executor := testExecutor(t, script, &fakeRegistry{}, testCase.settings)
			operation, _ := buyOperation(testCase.force, testCase.parameters)
			_, err := executor.Execute(context.Background(), operation)
			if testCase.code == "" && err != nil {
				t.Fatalf("error = %v", err)
			}
			if testCase.code != "" && cliCode(t, err).Code != testCase.code {
				t.Fatalf("code = %s, want %s", cliCode(t, err).Code, testCase.code)
			}
			if got := script.count("domain/create"); got != testCase.creates {
				t.Fatalf("domain/create calls = %d, want %d", got, testCase.creates)
			}
		})
	}
}

func TestBuyInsufficientFunds(t *testing.T) {
	t.Run("balance precheck sends nothing", func(t *testing.T) {
		script := newREGAPI(t, map[string][]scripted{
			"domain/get_prices": ok(pricesBody), "user/get_balance": ok(`{"result":"success","answer":{"currency":"RUR","prepay":"131.50"}}`),
		})
		operation, _ := buyOperation(false, nil)
		_, err := testExecutor(t, script, &fakeRegistry{}, ownerSettings).Execute(context.Background(), operation)
		cliErr := cliCode(t, err)
		if cliErr.Code != cli.CodeInsufficientFunds || cliErr.Details["missing_amount"] != "37.50" || cliErr.Details["dispatched"] != false || cliErr.ExitCode != cli.ExitInteractionRequired {
			t.Fatalf("error = %#v", cliErr)
		}
		if script.count("domain/create") != 0 {
			t.Fatal("domain/create was sent despite a short balance")
		}
	})
	t.Run("NOT_ENOUGH_MONEY maps to a top-up instruction", func(t *testing.T) {
		script := newREGAPI(t, map[string][]scripted{
			"domain/get_prices": ok(pricesBody), "user/get_balance": ok(balanceBody),
			"domain/create": ok(`{"result":"error","error_code":"NOT_ENOUGH_MONEY","error_text":"Not enough money"}`),
		})
		operation, _ := buyOperation(false, nil)
		_, err := testExecutor(t, script, &fakeRegistry{}, ownerSettings).Execute(context.Background(), operation)
		cliErr := cliCode(t, err)
		if cliErr.Code != cli.CodeInsufficientFunds || cliErr.Details["order_created"] != false || !strings.Contains(cliErr.Message, "top up") {
			t.Fatalf("error = %#v", cliErr)
		}
		if script.count("domain/create") != 1 || script.count("service/get_info") != 0 {
			t.Fatalf("calls = %v", script.calls)
		}
	})
}

func TestBuyReconciliationNeverRetries(t *testing.T) {
	cases := []struct {
		name      string
		create    scripted
		info      scripted
		committed bool
		lookup    string
	}{
		{"ambiguous then found", scripted{status: 502, body: "bad gateway"}, scripted{status: 200, body: `{"result":"success","answer":{"services":[{"service_id":"777","dname":"domkray.ru","servtype":"domain","state":"N"}]}}`}, true, "found"},
		{"duplicate user_servid proves dispatch", scripted{status: 200, body: `{"result":"error","error_code":"USER_SERVID_IS_NOT_UNIQUE"}`}, scripted{status: 200, body: `{"result":"success","answer":{"services":[{"service_id":"777","dname":"domkray.ru","servtype":"domain","state":"A"}]}}`}, true, "found"},
		{"ambiguous and not found stays unknown", scripted{status: 502, body: "bad gateway"}, scripted{status: 200, body: `{"result":"error","error_code":"SERVICE_NOT_FOUND"}`}, false, "not_found"},
		{"ambiguous and lookup fails stays unknown", scripted{status: 502, body: "bad gateway"}, scripted{status: 500, body: `oops`}, false, "failed"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			script := newREGAPI(t, map[string][]scripted{
				"domain/get_prices": ok(pricesBody),
				"user/get_balance":  {{status: 200, body: balanceBody}, {status: 200, body: `{"result":"success","answer":{"currency":"RUR","prepay":"131.00"}}`}},
				"domain/create":     {testCase.create}, "service/get_info": {testCase.info},
			})
			fake := &fakeRegistry{}
			operation, _ := buyOperation(false, nil)
			result, err := testExecutor(t, script, fake, ownerSettings).Execute(context.Background(), operation)
			if script.count("domain/create") != 1 {
				t.Fatalf("domain/create calls = %d; an ambiguous order must never be resent", script.count("domain/create"))
			}
			if testCase.committed {
				if err != nil {
					t.Fatalf("error = %v", err)
				}
				data := result.Data.(map[string]any)
				if data["reconciled"] != true || data["serviceId"] != "777" || data["userServid"] != testUserServID || data["nameserversConfirmed"] != true {
					t.Fatalf("data = %#v", data)
				}
				return
			}
			cliErr := cliCode(t, err)
			evidence, _ := cliErr.Details["evidence"].(Evidence)
			if cliErr.Code != cli.CodeOutcomeUnknown || cliErr.ExitCode != cli.ExitOutcomeUnknown || evidence.ServiceLookup != testCase.lookup ||
				evidence.UserServID != testUserServID || evidence.BalanceChanged == nil || !*evidence.BalanceChanged || fake.calls != 2 {
				t.Fatalf("error = %#v evidence = %#v registry calls = %d", cliErr, evidence, fake.calls)
			}
		})
	}
}

func TestBuyWithSuppliedUserServID(t *testing.T) {
	const reused = "fedcba9876543210fedcba9876543210"
	found := func(name string) []scripted {
		return ok(`{"result":"success","answer":{"services":[{"service_id":"777","dname":"` + name + `","servtype":"domain","state":"N"}]}}`)
	}
	t.Run("landed order is reported before registry and balance checks", func(t *testing.T) {
		script := newREGAPI(t, map[string][]scripted{"service/get_info": found("domkray.ru")})
		fake := &fakeRegistry{states: []registry.State{registry.StateTaken}}
		operation, _ := buyOperation(false, map[string][]string{"user-servid": {reused}})
		result, err := testExecutor(t, script, fake, ownerSettings).Execute(context.Background(), operation)
		if err != nil {
			t.Fatal(err)
		}
		data := result.Data.(map[string]any)
		if data["reconciled"] != true || data["serviceId"] != "777" || data["nameserversConfirmed"] != true || fake.calls != 0 || script.count("domain/create") != 0 {
			t.Fatalf("data = %#v registry = %d calls = %v", data, fake.calls, script.calls)
		}
	})
	t.Run("service for another name is refused", func(t *testing.T) {
		script := newREGAPI(t, map[string][]scripted{"service/get_info": found("other.ru")})
		operation, _ := buyOperation(false, map[string][]string{"user-servid": {reused}})
		_, err := testExecutor(t, script, &fakeRegistry{}, ownerSettings).Execute(context.Background(), operation)
		if cliErr := cliCode(t, err); cliErr.Details["reason"] != "user_servid_conflict" || script.count("domain/create") != 0 {
			t.Fatalf("error = %#v calls = %v", cliErr, script.calls)
		}
	})
	t.Run("unused id proceeds with a normal purchase", func(t *testing.T) {
		script := newREGAPI(t, map[string][]scripted{
			"service/get_info":  ok(`{"result":"error","error_code":"SERVICE_NOT_FOUND"}`),
			"domain/get_prices": ok(pricesBody), "user/get_balance": ok(balanceBody), "domain/create": ok(createBody),
		})
		operation, _ := buyOperation(false, map[string][]string{"user-servid": {reused}})
		result, err := testExecutor(t, script, &fakeRegistry{}, ownerSettings).Execute(context.Background(), operation)
		if err != nil || result.Data.(map[string]any)["userServid"] != reused || script.count("domain/create") != 1 {
			t.Fatalf("err = %v calls = %v", err, script.calls)
		}
	})
	nameless := ok(`{"result":"success","answer":{"services":[{"service_id":"777","servtype":"domain","state":"N"}]}}`)
	for _, testCase := range []struct {
		name   string
		nop    []scripted
		code   string
		reason string
	}{
		{"service of another type conflicts", nil, cli.CodeSpendingPolicyRefused, "user_servid_conflict"},
		{"nameless service resolved by domain/nop", ok(`{"result":"success","answer":{"dname":"domkray.ru","service_id":"777"}}`), "", ""},
		{"nameless service with another id conflicts", ok(`{"result":"success","answer":{"dname":"domkray.ru","service_id":"888"}}`), cli.CodeSpendingPolicyRefused, "user_servid_conflict"},
		{"nameless service with no domain/nop match stays unknown", ok(`{"result":"error","error_code":"DOMAIN_NOT_FOUND"}`), cli.CodeOutcomeUnknown, ""},
		{"nameless service that cannot be resolved stays unknown", []scripted{{status: 500, body: "oops"}}, cli.CodeOutcomeUnknown, ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			info := nameless
			if testCase.nop == nil {
				info = ok(`{"result":"success","answer":{"services":[{"service_id":"777","dname":"domkray.ru","servtype":"srv_hosting_ispmgr","state":"A"}]}}`)
			}
			script := newREGAPI(t, map[string][]scripted{"service/get_info": info, "domain/nop": testCase.nop})
			operation, _ := buyOperation(false, map[string][]string{"user-servid": {reused}})
			result, err := testExecutor(t, script, &fakeRegistry{}, ownerSettings).Execute(context.Background(), operation)
			if script.count("domain/create") != 0 {
				t.Fatalf("calls = %v", script.calls)
			}
			if testCase.code == "" {
				if err != nil || result.Data.(map[string]any)["reconciled"] != true {
					t.Fatalf("err = %v", err)
				}
				return
			}
			cliErr := cliCode(t, err)
			if cliErr.Code != testCase.code || testCase.reason != "" && cliErr.Details["reason"] != testCase.reason {
				t.Fatalf("error = %#v", cliErr)
			}
		})
	}
	t.Run("duplicate id for another name after dispatch is not a success", func(t *testing.T) {
		script := newREGAPI(t, map[string][]scripted{
			"domain/get_prices": ok(pricesBody), "user/get_balance": ok(balanceBody),
			"domain/create":    ok(`{"result":"error","error_code":"USER_SERVID_IS_NOT_UNIQUE"}`),
			"service/get_info": found("other.ru"),
		})
		operation, _ := buyOperation(false, nil)
		_, err := testExecutor(t, script, &fakeRegistry{}, ownerSettings).Execute(context.Background(), operation)
		if cliErr := cliCode(t, err); cliErr.Details["reason"] != "user_servid_conflict" || script.count("domain/create") != 1 {
			t.Fatalf("error = %#v", cliErr)
		}
	})
}

func TestBuyInterruptedDuringDispatchReconcilesOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	script := newREGAPI(t, map[string][]scripted{
		"domain/get_prices": ok(pricesBody), "user/get_balance": ok(balanceBody),
		"domain/create":    {{hang: true, onCall: cancel}},
		"service/get_info": ok(`{"result":"success","answer":{"services":[{"service_id":"777","dname":"domkray.ru","servtype":"domain","state":"N"}]}}`),
	})
	operation, _ := buyOperation(false, nil)
	result, err := testExecutor(t, script, &fakeRegistry{}, ownerSettings).Execute(ctx, operation)
	if err != nil {
		t.Fatalf("error = %v; reconciliation must outlive the interrupt", err)
	}
	if result.Data.(map[string]any)["reconciled"] != true || script.count("domain/create") != 1 {
		t.Fatalf("data = %#v calls = %v", result.Data, script.calls)
	}
}

func TestNameserverAmbiguityIsOutcomeUnknownAndNotResent(t *testing.T) {
	script := newREGAPI(t, map[string][]scripted{"domain/update_nss": {{status: 502, body: "bad gateway"}}})
	_, err := testExecutor(t, script, &fakeRegistry{}, ownerSettings).Execute(context.Background(), cli.Operation{
		Capability: "domain.ns", Action: "domain.ns.set", Account: "personal", ProfileID: testProfileID,
		Arguments: []string{"domkray.ru"}, Parameters: map[string][]string{"ns": {"nic"}},
		RequestTimeout: time.Second, Credentials: &fakeCredentials{},
	})
	if cliErr := cliCode(t, err); cliErr.Code != cli.CodeOutcomeUnknown || script.count("domain/update_nss") != 1 {
		t.Fatalf("error = %#v calls = %v", cliErr, script.calls)
	}
}

func TestBuyPreconditionsStopBeforeREGAPI(t *testing.T) {
	t.Run("taken in registry", func(t *testing.T) {
		script := newREGAPI(t, map[string][]scripted{})
		operation, credentials := buyOperation(false, nil)
		_, err := testExecutor(t, script, &fakeRegistry{states: []registry.State{registry.StateTaken}}, ownerSettings).Execute(context.Background(), operation)
		if cliCode(t, err).Code != cli.CodeDomainUnavailable || len(script.calls) != 0 || credentials.resolved != 0 {
			t.Fatalf("err = %v calls = %v credentials = %d", err, script.calls, credentials.resolved)
		}
	})
	t.Run("unknown in registry", func(t *testing.T) {
		script := newREGAPI(t, map[string][]scripted{})
		operation, _ := buyOperation(false, nil)
		_, err := testExecutor(t, script, &fakeRegistry{states: []registry.State{registry.StateUnknown}}, ownerSettings).Execute(context.Background(), operation)
		if cliCode(t, err).Code != cli.CodeAvailabilityUnknown || len(script.calls) != 0 {
			t.Fatalf("err = %v calls = %v", err, script.calls)
		}
	})
	t.Run("missing contact profile", func(t *testing.T) {
		operation, credentials := buyOperation(false, nil)
		_, err := testExecutor(t, nil, &fakeRegistry{}, profile.Domain{}).Execute(context.Background(), operation)
		if cliCode(t, err).Code != cli.CodeConfiguration || credentials.resolved != 0 {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestBuyAndNameserverIdentificationErrorsAreTyped(t *testing.T) {
	script := newREGAPI(t, map[string][]scripted{
		"domain/get_prices": ok(pricesBody), "user/get_balance": ok(balanceBody),
		"domain/create":     ok(`{"result":"error","error_code":"ESIA_IDENTIFICATION_REQUIRED"}`),
		"domain/update_nss": ok(`{"result":"success","answer":{"domains":[{"dname":"domkray.ru","result":"error","error_code":"ESIA_IDENTIFICATION_REQUIRED"}]}}`),
	})
	executor := testExecutor(t, script, &fakeRegistry{}, ownerSettings)
	operation, _ := buyOperation(false, nil)
	_, err := executor.Execute(context.Background(), operation)
	if cliErr := cliCode(t, err); cliErr.Code != cli.CodeIdentificationRequired || cliErr.Details["url"] != cli.IdentificationURL {
		t.Fatalf("buy error = %#v", cliErr)
	}

	mismatch := newREGAPI(t, map[string][]scripted{
		"domain/update_nss": ok(`{"result":"success","answer":{"domains":[{"dname":"domkray.ru","result":"success","service_id":"777"}]}}`),
		"domain/get_nss":    ok(`{"result":"success","answer":{"domains":[{"dname":"domkray.ru","nss":[{"ns":"ns3-l2.nic.ru"},{"ns":"ns4-l2.nic.ru"},{"ns":"ns8-l2.nic.ru"},{"ns":"ns4-cloud.nic.ru"}]}]}}`),
	})
	nsResult, err := testExecutor(t, mismatch, &fakeRegistry{}, ownerSettings).Execute(context.Background(), cli.Operation{
		Capability: "domain.ns", Action: "domain.ns.set", Account: "personal", ProfileID: testProfileID,
		Arguments: []string{"domkray.ru"}, Parameters: map[string][]string{"ns": {"nic"}},
		RequestTimeout: time.Second, Credentials: &fakeCredentials{},
	})
	if err != nil || nsResult.Data.(map[string]any)["nameserversConfirmed"] != false || len(nsResult.Warnings) != 1 || nsResult.Warnings[0].Code != "nameservers_mismatch" {
		t.Fatalf("truncated nameserver set was not reported: %#v %v", nsResult, err)
	}

	nsOperation := cli.Operation{
		Capability: "domain.ns", Action: "domain.ns.set", Account: "personal", ProfileID: testProfileID,
		Arguments: []string{"domkray.ru"}, Parameters: map[string][]string{"ns": {"nic"}},
		RequestTimeout: time.Second, Credentials: &fakeCredentials{},
	}
	_, err = executor.Execute(context.Background(), nsOperation)
	if cliErr := cliCode(t, err); cliErr.Code != cli.CodeIdentificationRequired || cliErr.ExitCode != cli.ExitInteractionRequired {
		t.Fatalf("ns error = %#v", cliErr)
	}
	if script.count("domain/update_nss") != 1 {
		t.Fatalf("calls = %v", script.calls)
	}
}

func TestCheckRegistrarConfirmation(t *testing.T) {
	checkOperation := func(credentials cli.CredentialResolver, account string) cli.Operation {
		return cli.Operation{
			Capability: "domain.search", Action: "domain.check", Account: account, ProfileID: testProfileID,
			Arguments: []string{"free-one.ru", "taken-one.ru", "домкрай.рф"}, RequestTimeout: time.Second, Credentials: credentials,
		}
	}
	registryStates := func() *fakeRegistry {
		return &fakeRegistry{states: []registry.State{registry.StateLikelyFree, registry.StateTaken, registry.StateLikelyFree}}
	}

	t.Run("no account stays likely free without credentials", func(t *testing.T) {
		result, err := testExecutor(t, nil, registryStates(), ownerSettings).Execute(context.Background(), checkOperation(nil, ""))
		if err != nil {
			t.Fatal(err)
		}
		data := result.Data.(map[string]any)
		results := data["results"].([]registry.Verdict)
		if results[0].State != registry.StateLikelyFree || results[0].Registrar.Reason != "no_account_selected" || data["complete"] != true {
			t.Fatalf("data = %#v", data)
		}
		if results[2].Name.ASCII != "xn--80ahqcjlq.xn--p1ai" || results[2].Name.Unicode != "домкрай.рф" {
			t.Fatalf("IDN forms = %#v", results[2].Name)
		}
	})

	t.Run("disabled registrar check never resolves credentials", func(t *testing.T) {
		credentials := &fakeCredentials{}
		_, err := testExecutor(t, nil, registryStates(), ownerSettings).Execute(context.Background(), checkOperation(credentials, "personal"))
		if err != nil || credentials.resolved != 0 {
			t.Fatalf("err = %v resolved = %d", err, credentials.resolved)
		}
	})

	t.Run("partner-only refusal keeps likely free with the reason", func(t *testing.T) {
		script := newREGAPI(t, map[string][]scripted{"domain/check": ok(`{"result":"error","error_code":"RESELLER_AUTH_FAILED"}`)})
		settings := ownerSettings
		settings.RegistrarCheck = true
		result, err := testExecutor(t, script, registryStates(), settings).Execute(context.Background(), checkOperation(&fakeCredentials{}, "personal"))
		if err != nil {
			t.Fatal(err)
		}
		results := result.Data.(map[string]any)["results"].([]registry.Verdict)
		if results[0].State != registry.StateLikelyFree || results[0].Registrar.Reason != "registrar_check_partner_only" || script.count("domain/check") != 1 {
			t.Fatalf("results = %#v", results)
		}
	})

	t.Run("registrar confirmation upgrades to free in one batch", func(t *testing.T) {
		script := newREGAPI(t, map[string][]scripted{"domain/check": ok(`{"result":"success","answer":{"domains":[
			{"dname":"free-one.ru","result":"Available"},
			{"dname":"xn--80ahqcjlq.xn--p1ai","result":"Domain already exists","error_code":"DOMAIN_ALREADY_EXISTS"}]}}`)})
		settings := ownerSettings
		settings.RegistrarCheck = true
		result, err := testExecutor(t, script, registryStates(), settings).Execute(context.Background(), checkOperation(&fakeCredentials{}, "personal"))
		if err != nil {
			t.Fatal(err)
		}
		results := result.Data.(map[string]any)["results"].([]registry.Verdict)
		if results[0].State != registry.StateFree || results[2].State != registry.StateTaken || script.count("domain/check") != 1 {
			t.Fatalf("results = %#v", results)
		}
	})

	t.Run("all unknown is an error, never free", func(t *testing.T) {
		fake := &fakeRegistry{states: []registry.State{registry.StateUnknown, registry.StateUnknown, registry.StateUnknown}}
		_, err := testExecutor(t, nil, fake, ownerSettings).Execute(context.Background(), checkOperation(nil, ""))
		if cliErr := cliCode(t, err); cliErr.Code != cli.CodeAvailabilityUnknown || cliErr.ExitCode != cli.ExitNetwork {
			t.Fatalf("error = %#v", cliErr)
		}
	})
}

func TestQuoteReportsPricesAndPolicy(t *testing.T) {
	script := newREGAPI(t, map[string][]scripted{"domain/get_prices": ok(pricesBody)})
	operation := cli.Operation{
		Capability: "domain.quote", Action: "domain.quote", Account: "personal", ProfileID: testProfileID,
		Arguments: []string{"example.com"}, RequestTimeout: time.Second, Credentials: &fakeCredentials{},
	}
	result, err := testExecutor(t, script, &fakeRegistry{}, ownerSettings).Execute(context.Background(), operation)
	if err != nil {
		t.Fatal(err)
	}
	data := result.Data.(map[string]any)
	policy := data["policy"].(domain.Evaluation)
	if data["firstYear"] != "1905" || data["renewal"] != "1905" || data["currency"] != "RUR" || policy.Decision != domain.DecisionNeedsForce {
		t.Fatalf("data = %#v", data)
	}
}
