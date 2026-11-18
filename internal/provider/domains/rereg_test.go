package domains

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/adinvadim/reg-ru-cli/internal/cli"
	"github.com/adinvadim/reg-ru-cli/internal/profile"
	"github.com/adinvadim/reg-ru-cli/internal/provider/rereg"
)

const (
	reregLadder    = `[0,450,1950,5950,11950,17950,25500,49950,69900,99900,190900,350900,450000,675000,900000,1600000,2300000,3200000,4500000,7500000,10000000]`
	noBidsBody     = `{"result":"success","answer":{"domains":[],"rereg_bids":{"ru":` + reregLadder + `,"su":` + reregLadder + `,"рф":` + reregLadder + `}}}`
	reregBalance   = `{"result":"success","answer":{"currency":"RUR","prepay":"824.00"}}`
	reregBidOK     = `{"result":"success","answer":{"bill_id":"4321","payment":"450","pay_type":"prepay","pay_notes":"Amount successfully charged","domains":[{"dname":"moonbot.ru","result":"success","service_id":"999"}]}}`
	blitzBody      = `{"ru":{"cur_bid":"3200000","time_bid":1794995100,"max_bid":"10000000","min_bid":"450","time_left":63}}`
	reregListPage  = `<html><script>window.pDomainRereg = {"domainsArray":[]};</script></html>`
	reregProfileID = testProfileID
)

// Wednesday 2026-11-18 12:00 MSK: outside the guard window, the day before
// moonbot.ru is released.
var reregNoon = time.Date(2026, 11, 18, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))

func ownBidBody(user, top, next string, active int) string {
	return fmt.Sprintf(`{"result":"success","answer":{"domains":[{"dname":"moonbot.ru","tld":"ru","is_active":%d,"user_bid":"%s","max_bid":"%s","next_price":%s,"blitz_price":"10000000.00","delete_date":"2026-11-19","registrar":"REGRU"}],"rereg_bids":{"ru":%s}}}`,
		active, user, top, next, reregLadder)
}

func detailsFor(name, minPrice string) string {
	return `<html><script>
		window.pDomainReregDetails = {"domain":{"isDomainDeletionBlitz":"","zone":"ru","name":"` + name + `","minPrice":"` + minPrice + `","bids":["0","450","1950","5950","11950","17950","25500","49950","69900","99900","190900","350900","450000","675000","900000","1600000","2300000","3200000","4500000","7500000","10000000"],"maxPrice":"10000000.00"}};
	</script></html>`
}

type fakePublic struct {
	pages map[string]rereg.Response
	err   error
	opens int
	gets  []string
}

func (f *fakePublic) Open(_ context.Context, _ string) (rereg.Page, error) {
	f.opens++
	if f.err != nil {
		return nil, f.err
	}
	return fakePage{public: f}, nil
}

type fakePage struct{ public *fakePublic }

func (p fakePage) Get(_ context.Context, path string) (rereg.Response, error) {
	p.public.gets = append(p.public.gets, path)
	response, exists := p.public.pages[path]
	if !exists {
		return rereg.Response{}, &rereg.Failure{Reason: rereg.ReasonNetwork}
	}
	return response, nil
}

func (fakePage) Close() error { return nil }

func publicLot(name, minPrice string) *fakePublic {
	return &fakePublic{pages: map[string]rereg.Response{
		rereg.BlitzPath:                 {Status: 200, Body: blitzBody},
		rereg.DetailsPath("moonbot.ru"): {Status: 200, Body: detailsFor(name, minPrice)},
	}}
}

type reregFixture struct {
	executor *Executor
	journal  rereg.Journal
	store    rereg.Store
}

func newReregFixture(t *testing.T, script *regapiScript, public rereg.Opener, settings profile.Domain, now time.Time) reregFixture {
	t.Helper()
	profiles := profile.NewMemoryRepository(profile.Config{Accounts: map[string]profile.Account{
		"personal": {ID: reregProfileID, Provider: "reg.ru", Domain: settings},
	}})
	options := ExecutorOptions{
		Profiles: profiles, Registry: &fakeRegistry{}, ReconcileTimeout: 2 * time.Second,
		ReregPublic: public, CatalogDir: t.TempDir(), JournalDir: t.TempDir(),
		Now: func() time.Time { return now },
	}
	if script != nil {
		options.REGAPIBaseURL = script.server.URL + "/api/regru2"
		options.HTTPClient = script.server.Client()
	}
	return reregFixture{
		executor: NewExecutor(options, nil),
		journal:  rereg.Journal{Dir: options.JournalDir, ProfileID: reregProfileID},
		store:    rereg.Store{Dir: options.CatalogDir},
	}
}

func (f reregFixture) saveCatalog(t *testing.T, line string) {
	t.Helper()
	lines := []string{"Домен;Ставки;Текущ.блиц,руб.;Перв.регистрация;Дата удаления;Регистратор", line}
	for index := 0; index < 1000; index++ {
		lines = append(lines, fmt.Sprintf("filler%04d.ru;нет;10000000.00;;2026-11-20;BEGET", index))
	}
	if _, err := f.store.Save(strings.Join(lines, "\r\n"), "", reregNoon); err != nil {
		t.Fatal(err)
	}
}

func quoteOperation(account bool) cli.Operation {
	operation := cli.Operation{
		Capability: "domain.rereg.quote", Action: "domain.rereg.quote", Arguments: []string{"moonbot.ru"},
		RequestTimeout: time.Second,
	}
	if account {
		operation.Account, operation.ProfileID, operation.Credentials = "personal", reregProfileID, &fakeCredentials{}
	}
	return operation
}

// sharedQuote mirrors the dropcatch quote data contract the aggregator decodes.
type sharedQuote struct {
	Service     string  `json:"service"`
	Domain      string  `json:"domain"`
	ReleaseDate *string `json:"releaseDate"`
	State       string  `json:"state"`
	Steps       []struct {
		ID             string   `json:"id"`
		AmountKopecks  int64    `json:"amountKopecks"`
		Available      bool     `json:"available"`
		WinProbability *float64 `json:"winProbability"`
	} `json:"steps"`
	CurrentTopKopecks   *int64 `json:"currentTopKopecks"`
	NextBidKopecks      *int64 `json:"nextBidKopecks"`
	CompetingBidVisible bool   `json:"competingBidVisible"`
	MyOrder             *struct {
		ID            string `json:"id"`
		AmountKopecks int64  `json:"amountKopecks"`
		Status        string `json:"status"`
	} `json:"myOrder"`
	BidDeadline  *string `json:"bidDeadline"`
	AuctionUntil *string `json:"auctionUntil"`
	Balance      *struct {
		AvailableKopecks int64 `json:"availableKopecks"`
		Sufficient       *bool `json:"sufficient"`
	} `json:"balance"`
	Notes      []string  `json:"notes"`
	Currency   string    `json:"currency"`
	ObservedAt time.Time `json:"observedAt"`
}

func decodeShared(t *testing.T, data any) (sharedQuote, map[string]json.RawMessage) {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	var quote sharedQuote
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &quote); err != nil {
		t.Fatalf("quote does not decode as the shared contract: %v\n%s", err, raw)
	}
	for _, key := range []string{
		"service", "domain", "releaseDate", "state", "steps", "currentTopKopecks", "nextBidKopecks",
		"competingBidVisible", "myOrder", "bidDeadline", "auctionUntil", "balance", "notes",
	} {
		if _, present := keys[key]; !present {
			t.Errorf("shared quote key %q is missing (missing and null must stay distinct)", key)
		}
	}
	return quote, keys
}

func TestReregQuoteMatchesSharedContract(t *testing.T) {
	script := newREGAPI(t, map[string][]scripted{"domain/get_user_rereg_bids": ok(noBidsBody), "user/get_balance": ok(reregBalance)})
	public := publicLot("moonbot.ru", "1950.00")
	fixture := newReregFixture(t, script, public, profile.Domain{}, reregNoon)
	fixture.saveCatalog(t, "moonbot.ru;есть;10000000.00;2012-03-31;2026-11-19;REGRU")

	result, err := fixture.executor.Execute(context.Background(), quoteOperation(true))
	if err != nil {
		t.Fatal(err)
	}
	quote, keys := decodeShared(t, result.Data)
	if quote.Service != "regru" || quote.Domain != "moonbot.ru" || quote.State != "releasing" || quote.Currency != "RUB" || quote.ObservedAt.IsZero() {
		t.Fatalf("quote = %#v", quote)
	}
	if quote.ReleaseDate == nil || *quote.ReleaseDate != "2026-11-19" || quote.BidDeadline == nil || *quote.BidDeadline != "2026-11-19T16:45:00+03:00" || quote.AuctionUntil != nil {
		t.Fatalf("dates = %v %v %v", quote.ReleaseDate, quote.BidDeadline, quote.AuctionUntil)
	}
	if quote.NextBidKopecks == nil || *quote.NextBidKopecks != 195000 || quote.CurrentTopKopecks == nil || *quote.CurrentTopKopecks != 45000 || !quote.CompetingBidVisible {
		t.Fatalf("bids = next %v top %v competing %v", quote.NextBidKopecks, quote.CurrentTopKopecks, quote.CompetingBidVisible)
	}
	if string(keys["myOrder"]) != "null" || quote.Balance == nil || quote.Balance.AvailableKopecks != 82400 || quote.Balance.Sufficient == nil || *quote.Balance.Sufficient {
		t.Fatalf("myOrder = %s balance = %#v", keys["myOrder"], quote.Balance)
	}
	if len(quote.Steps) != 20 || quote.Steps[0].ID != "450" || quote.Steps[0].Available || !quote.Steps[1].Available ||
		quote.Steps[1].AmountKopecks != 195000 || quote.Steps[1].WinProbability != nil || !quote.Steps[19].Available {
		t.Fatalf("steps = %#v", quote.Steps)
	}
	if public.opens != 1 || script.count("domain/set_rereg_bids") != 0 {
		t.Fatalf("browser opens = %d calls = %v", public.opens, script.calls)
	}
}

func TestReregQuoteReportsOwnBid(t *testing.T) {
	script := newREGAPI(t, map[string][]scripted{
		"domain/get_user_rereg_bids": ok(ownBidBody("1950.00", "5950.00", `"11950.00"`, 1)), "user/get_balance": ok(reregBalance),
	})
	fixture := newReregFixture(t, script, publicLot("moonbot.ru", "11950.00"), profile.Domain{}, reregNoon)
	result, err := fixture.executor.Execute(context.Background(), quoteOperation(true))
	if err != nil {
		t.Fatal(err)
	}
	quote, _ := decodeShared(t, result.Data)
	if quote.State != "ordered_by_me" || quote.MyOrder == nil || quote.MyOrder.ID != "moonbot.ru" || quote.MyOrder.AmountKopecks != 195000 ||
		quote.MyOrder.Status != "outbid" || !quote.CompetingBidVisible || *quote.CurrentTopKopecks != 595000 || *quote.NextBidKopecks != 1195000 ||
		quote.ReleaseDate == nil || *quote.ReleaseDate != "2026-11-19" {
		t.Fatalf("quote = %#v myOrder = %#v", quote, quote.MyOrder)
	}
}

func TestReregQuoteWithoutAccountUsesPublicDataOnly(t *testing.T) {
	public := &fakePublic{pages: map[string]rereg.Response{
		rereg.BlitzPath:                 {Status: 200, Body: blitzBody},
		rereg.DetailsPath("moonbot.ru"): {Status: 200, Body: reregListPage},
	}}
	fixture := newReregFixture(t, nil, public, profile.Domain{}, reregNoon)
	result, err := fixture.executor.Execute(context.Background(), quoteOperation(false))
	if err != nil {
		t.Fatal(err)
	}
	quote, keys := decodeShared(t, result.Data)
	if quote.State != "not_listed" || string(keys["myOrder"]) != "null" || string(keys["balance"]) != "null" ||
		string(keys["nextBidKopecks"]) != "null" || quote.ReleaseDate != nil || len(quote.Steps) != 0 || string(keys["steps"]) != "[]" {
		t.Fatalf("quote = %s", mustJSON(t, result.Data))
	}
}

func TestReregQuoteFailsWhenNoSourceAnswers(t *testing.T) {
	fixture := newReregFixture(t, nil, &fakePublic{err: &rereg.Failure{Reason: rereg.ReasonMissingBrowser}}, profile.Domain{}, reregNoon)
	_, err := fixture.executor.Execute(context.Background(), quoteOperation(false))
	if cliErr := cliCode(t, err); cliErr.Code != cli.CodeMissingBrowser {
		t.Fatalf("error = %#v", cliErr)
	}
}

func TestReregBidsListIsAnArrayForTheAggregator(t *testing.T) {
	script := newREGAPI(t, map[string][]scripted{"domain/get_user_rereg_bids": ok(ownBidBody("5950.00", "5950.00", `"11950.00"`, 1))})
	fixture := newReregFixture(t, script, nil, profile.Domain{}, reregNoon)
	operation := quoteOperation(true)
	operation.Capability, operation.Action, operation.Arguments = "domain.rereg.bids", "domain.rereg.bids", nil
	result, err := fixture.executor.Execute(context.Background(), operation)
	if err != nil {
		t.Fatal(err)
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(mustJSON(t, result.Data)), &items); err != nil || len(items) != 1 {
		t.Fatalf("data = %s (%v)", mustJSON(t, result.Data), err)
	}
	item := items[0]
	if item["service"] != "regru" || item["domain"] != "moonbot.ru" || item["id"] != "moonbot.ru" || item["amountKopecks"] != float64(595000) ||
		item["status"] != "leading" || item["bidDeadline"] != "2026-11-19T16:45:00+03:00" {
		t.Fatalf("item = %#v", item)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func bidOperation(price string, extra map[string][]string) cli.Operation {
	parameters := map[string][]string{
		"price": {price}, "max-price": {"5000"}, "contact": {"profile"}, "ns": {"nic"},
		"over-limit": {"false"}, "allow-release-window": {"false"},
	}
	for key, value := range extra {
		parameters[key] = value
	}
	return cli.Operation{
		Capability: "domain.rereg.bid", Action: "domain.rereg.bid", Account: "personal", ProfileID: reregProfileID,
		Arguments: []string{"moonbot.ru"}, Parameters: parameters, Force: true,
		RequestTimeout: time.Second, Credentials: &fakeCredentials{},
	}
}

func spentToday(t *testing.T, journal rereg.Journal) int64 {
	t.Helper()
	spend, err := journal.DailySpend("2026-11-18")
	if err != nil {
		t.Fatal(err)
	}
	return spend.AmountKopecks
}

func TestReregBidSendsOnceAndJournals(t *testing.T) {
	script := newREGAPI(t, map[string][]scripted{
		"domain/get_user_rereg_bids": ok(noBidsBody), "user/get_balance": ok(reregBalance), "domain/set_rereg_bids": ok(reregBidOK),
	})
	fixture := newReregFixture(t, script, publicLot("moonbot.ru", "450.00"), ownerSettings, reregNoon)
	fixture.saveCatalog(t, "moonbot.ru;нет;10000000.00;;2026-11-19;REGRU")
	result, err := fixture.executor.Execute(context.Background(), bidOperation("450", nil))
	if err != nil {
		t.Fatal(err)
	}
	data := result.Data.(map[string]any)
	if data["outcome"] != "committed" || data["billId"] != "4321" || data["priceKopecks"] != int64(45000) || data["reconciled"] != false ||
		data["bidDeadline"] != "2026-11-19T16:45:00+03:00" {
		t.Fatalf("data = %#v", data)
	}
	if script.count("domain/set_rereg_bids") != 1 || spentToday(t, fixture.journal) != 45000 {
		t.Fatalf("calls = %v spent = %d", script.calls, spentToday(t, fixture.journal))
	}
}

func TestReregBidCredentialContacts(t *testing.T) {
	for _, check := range []struct {
		name     string
		contacts string
		sent     bool
	}{
		{"valid contacts are sent", `{"person":"Ivan I Ivanov","country":"RU"}`, true},
		{"malformed contacts stop before REG.API", `person=Ivan`, false},
		{"empty values stop before REG.API", `{"person":""}`, false},
	} {
		t.Run(check.name, func(t *testing.T) {
			responses := map[string][]scripted{}
			if check.sent {
				responses = map[string][]scripted{
					"domain/get_user_rereg_bids": ok(noBidsBody), "user/get_balance": ok(reregBalance), "domain/set_rereg_bids": ok(reregBidOK),
				}
			}
			script := newREGAPI(t, responses)
			fixture := newReregFixture(t, script, publicLot("moonbot.ru", "450.00"), profile.Domain{}, reregNoon)
			operation := bidOperation("450", map[string][]string{"contact": {"credential"}})
			operation.Credentials = contactCredentials{contacts: check.contacts}
			_, err := fixture.executor.Execute(context.Background(), operation)
			if check.sent {
				if err != nil || script.count("domain/set_rereg_bids") != 1 {
					t.Fatalf("err = %v calls = %v", err, script.calls)
				}
				return
			}
			if cliErr := cliCode(t, err); cliErr.Code != cli.CodeConfiguration || len(script.calls) != 0 || strings.Contains(cliErr.Message, "Ivan") {
				t.Fatalf("error = %#v calls = %v", cliErr, script.calls)
			}
		})
	}
}

type contactCredentials struct{ contacts string }

func (c contactCredentials) Resolve(_ context.Context, key string) ([]byte, error) {
	if key == "rereg.contacts" {
		return []byte(c.contacts), nil
	}
	return []byte("secret-" + key), nil
}

func TestReregBidPolicyRefusesBeforeSending(t *testing.T) {
	guard := time.Date(2026, 11, 18, 17, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	cases := []struct {
		name     string
		price    string
		extra    map[string][]string
		now      time.Time
		priorDay int64
		bids     string
		reason   string
		code     string
		minimum  string
	}{
		{name: "guard window", price: "450", now: guard, reason: "guard_window"},
		{name: "per-bid cap", price: "5950", extra: map[string][]string{"max-price": {"6000"}}, reason: "spend_limit_exceeded"},
		{name: "daily cap", price: "1950", priorDay: 900000, reason: "spend_limit_exceeded"},
		{name: "own equal bid", price: "1950", bids: ownBidBody("1950.00", "1950.00", `"5950.00"`, 1), reason: "existing_bid"},
		{name: "deadline passed", price: "450", now: time.Date(2026, 11, 19, 16, 50, 0, 0, time.FixedZone("MSK", 3*3600)),
			bids: ownBidBody("0.00", "0.00", `"450.00"`, 1), reason: "deadline_passed"},
		{name: "not a ladder step", price: "500", code: cli.CodeInvalidUsage},
		{name: "below the public minimum", price: "450", minimum: "1950.00", code: cli.CodeInvalidUsage},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			now := testCase.now
			if now.IsZero() {
				now = reregNoon
			}
			bids := testCase.bids
			if bids == "" {
				bids = noBidsBody
			}
			script := newREGAPI(t, map[string][]scripted{"domain/get_user_rereg_bids": ok(bids)})
			minimum := testCase.minimum
			if minimum == "" {
				minimum = "450.00"
			}
			fixture := newReregFixture(t, script, publicLot("moonbot.ru", minimum), ownerSettings, now)
			if testCase.priorDay > 0 {
				if err := fixture.journal.Append(rereg.Entry{ID: "00000000000000aa", Domain: "x.ru", AmountKopecks: testCase.priorDay, State: rereg.StateConfirmed, Day: "2026-11-18"}); err != nil {
					t.Fatal(err)
				}
			}
			_, err := fixture.executor.Execute(context.Background(), bidOperation(testCase.price, testCase.extra))
			cliErr := cliCode(t, err)
			switch {
			case testCase.reason != "" && (cliErr.Code != cli.CodePolicyRefused || cliErr.ExitCode != cli.ExitPolicy || cliErr.Details["reason"] != testCase.reason):
				t.Fatalf("error = %#v", cliErr)
			case testCase.code != "" && cliErr.Code != testCase.code:
				t.Fatalf("error = %#v", cliErr)
			}
			if script.count("domain/set_rereg_bids") != 0 || script.count("user/get_balance") != 0 {
				t.Fatalf("calls = %v; nothing may be sent after a refusal", script.calls)
			}
		})
	}
}

func TestReregBidOverridesLiftCapsAndWindow(t *testing.T) {
	guard := time.Date(2026, 11, 18, 17, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	script := newREGAPI(t, map[string][]scripted{
		"domain/get_user_rereg_bids": ok(noBidsBody),
		"user/get_balance":           ok(`{"result":"success","answer":{"currency":"RUR","prepay":"9000.00"}}`),
		"domain/set_rereg_bids":      ok(strings.Replace(reregBidOK, `"payment":"450"`, `"payment":"5950"`, 1)),
	})
	fixture := newReregFixture(t, script, publicLot("moonbot.ru", "450.00"), ownerSettings, guard)
	result, err := fixture.executor.Execute(context.Background(), bidOperation("5950", map[string][]string{
		"over-limit": {"true"}, "allow-release-window": {"true"}, "max-price": {"6000"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !hasWarning(result.Warnings, "spending_cap_overridden") || script.count("domain/set_rereg_bids") != 1 {
		t.Fatalf("warnings = %#v calls = %v", result.Warnings, script.calls)
	}
}

func hasWarning(warnings []cli.Warning, code string) bool {
	for _, warning := range warnings {
		if warning.Code == code {
			return true
		}
	}
	return false
}

func TestReregBidInsufficientBalanceStopsBeforeDispatch(t *testing.T) {
	script := newREGAPI(t, map[string][]scripted{"domain/get_user_rereg_bids": ok(noBidsBody), "user/get_balance": ok(reregBalance)})
	fixture := newReregFixture(t, script, publicLot("moonbot.ru", "450.00"), ownerSettings, reregNoon)
	_, err := fixture.executor.Execute(context.Background(), bidOperation("1950", nil))
	if cliErr := cliCode(t, err); cliErr.Code != cli.CodeInsufficientFunds || cliErr.Details["dispatched"] != false {
		t.Fatalf("error = %#v", cliErr)
	}
	if script.count("domain/set_rereg_bids") != 0 || spentToday(t, fixture.journal) != 0 {
		t.Fatalf("calls = %v", script.calls)
	}
}

func TestReregBidDefinitiveRejectionIsNotCounted(t *testing.T) {
	script := newREGAPI(t, map[string][]scripted{
		"domain/get_user_rereg_bids": ok(noBidsBody), "user/get_balance": ok(reregBalance),
		"domain/set_rereg_bids": ok(`{"result":"error","error_code":"SAME_OR_MORE_BID_FOUND","error_text":"The same or big rate is found"}`),
	})
	fixture := newReregFixture(t, script, publicLot("moonbot.ru", "450.00"), ownerSettings, reregNoon)
	_, err := fixture.executor.Execute(context.Background(), bidOperation("450", nil))
	cliErr := cliCode(t, err)
	if cliErr.Code != cli.CodeNetwork || cliErr.Details["provider_code"] != "SAME_OR_MORE_BID_FOUND" || cliErr.Details["bid_created"] != false {
		t.Fatalf("error = %#v", cliErr)
	}
	if script.count("domain/set_rereg_bids") != 1 || spentToday(t, fixture.journal) != 0 {
		t.Fatalf("calls = %v spent = %d", script.calls, spentToday(t, fixture.journal))
	}
}

func TestReregBidAmbiguousDispatchIsReconciledNeverResent(t *testing.T) {
	cases := []struct {
		name      string
		dispatch  scripted
		after     scripted
		committed bool
		lookup    string
	}{
		{"502 then own bid found", scripted{status: 502, body: "bad gateway"}, scripted{status: 200, body: ownBidBody("450.00", "450.00", `"1950.00"`, 1)}, true, ""},
		{"502 and no own bid", scripted{status: 502, body: "bad gateway"}, scripted{status: 200, body: noBidsBody}, false, "not_found"},
		{"timeout and lookup fails", scripted{hang: true}, scripted{status: 500, body: "oops"}, false, "failed"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			script := newREGAPI(t, map[string][]scripted{
				"domain/get_user_rereg_bids": {{status: 200, body: noBidsBody}, testCase.after},
				"user/get_balance":           {{status: 200, body: reregBalance}, {status: 200, body: `{"result":"success","answer":{"currency":"RUR","prepay":"374.00"}}`}},
				"domain/set_rereg_bids":      {testCase.dispatch},
			})
			fixture := newReregFixture(t, script, publicLot("moonbot.ru", "450.00"), ownerSettings, reregNoon)
			ctx := context.Background()
			if testCase.dispatch.hang {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 1500*time.Millisecond)
				defer cancel()
			}
			result, err := fixture.executor.Execute(ctx, bidOperation("450", nil))
			if script.count("domain/set_rereg_bids") != 1 {
				t.Fatalf("set_rereg_bids calls = %d; an ambiguous bid must never be resent", script.count("domain/set_rereg_bids"))
			}
			if spentToday(t, fixture.journal) != 45000 {
				t.Fatalf("spent = %d; committed and unknown bids count toward the daily cap", spentToday(t, fixture.journal))
			}
			if testCase.committed {
				if err != nil {
					t.Fatal(err)
				}
				if data := result.Data.(map[string]any); data["reconciled"] != true || !hasWarning(result.Warnings, "reconciled") {
					t.Fatalf("data = %#v", data)
				}
				return
			}
			cliErr := cliCode(t, err)
			evidence, _ := cliErr.Details["evidence"].(ReregEvidence)
			if cliErr.Code != cli.CodeOutcomeUnknown || cliErr.ExitCode != cli.ExitOutcomeUnknown || evidence.BidLookup != testCase.lookup ||
				cliErr.Details["journalId"] == "" || evidence.BalanceChanged == nil || !*evidence.BalanceChanged || evidence.BalanceBefore != 82400 {
				t.Fatalf("error = %#v evidence = %#v", cliErr, evidence)
			}
		})
	}
}

func catalogCSV(extra ...string) string {
	lines := append([]string{"Домен;Ставки;Текущ.блиц,руб.;Перв.регистрация;Дата удаления;Регистратор"}, extra...)
	for index := 0; index < 1000; index++ {
		lines = append(lines, fmt.Sprintf("filler%04d.ru;нет;10000000.00;;2026-11-25;BEGET", index))
	}
	return strings.Join(lines, "\r\n")
}

func TestReregCatalogFetchStoresAndSearchFilters(t *testing.T) {
	public := &fakePublic{pages: map[string]rereg.Response{
		rereg.CatalogPath: {Status: 200, ContentType: "application/octet-stream", LastModified: "Wed, 18 Nov 2026 06:32:36 GMT", Body: catalogCSV(
			"moonbot.ru;есть;10000000.00;2012-03-31;2026-11-19;REGRU",
			"kubanbot.ru;нет;10000000.00;;2026-11-19;RUCENTER",
			"кубань.рф;есть;10000000.00;;2026-11-20;REGRU",
		)},
	}}
	fixture := newReregFixture(t, nil, public, profile.Domain{}, reregNoon)
	fetch := cli.Operation{Capability: "domain.rereg.catalog", Action: "domain.rereg.catalog.fetch", RequestTimeout: time.Second}
	result, err := fixture.executor.Execute(context.Background(), fetch)
	if err != nil {
		t.Fatal(err)
	}
	if meta := result.Data.(map[string]any)["snapshot"].(rereg.Metadata); meta.Rows != 1003 || meta.WithBids != 2 || meta.LastModified == "" {
		t.Fatalf("snapshot = %#v", meta)
	}
	search := func(arguments []string, parameters map[string][]string) []string {
		t.Helper()
		operation := cli.Operation{Capability: "domain.rereg.catalog", Action: "domain.rereg.catalog.search", Arguments: arguments, Parameters: parameters}
		result, err := fixture.executor.Execute(context.Background(), operation)
		if err != nil {
			t.Fatal(err)
		}
		names := []string{}
		for _, row := range result.Data.(map[string]any)["results"].([]rereg.Row) {
			names = append(names, row.Name.Unicode)
		}
		return names
	}
	for _, check := range []struct {
		arguments  []string
		parameters map[string][]string
		want       string
	}{
		{nil, map[string][]string{"date": {"tomorrow"}}, "kubanbot.ru,moonbot.ru"},
		{[]string{"BOT"}, map[string][]string{"with-bids": {"true"}}, "moonbot.ru"},
		{[]string{"кубань"}, nil, "кубань.рф"},
		{nil, map[string][]string{"zones": {"rf"}}, "кубань.рф"},
		{nil, map[string][]string{"registrar": {"rucenter"}}, "kubanbot.ru"},
		{nil, map[string][]string{"date": {"2026-11-19"}, "limit": {"1"}}, "kubanbot.ru"},
	} {
		if got := strings.Join(search(check.arguments, check.parameters), ","); got != check.want {
			t.Errorf("search %v %v = %q, want %q", check.arguments, check.parameters, got, check.want)
		}
	}
}

func TestReregCatalogFetchRejectsAnHTMLAnswer(t *testing.T) {
	public := &fakePublic{pages: map[string]rereg.Response{
		rereg.CatalogPath: {Status: 200, ContentType: "text/html; charset=utf-8", Body: "<html>maintenance</html>"},
	}}
	fixture := newReregFixture(t, nil, public, profile.Domain{}, reregNoon)
	_, err := fixture.executor.Execute(context.Background(), cli.Operation{Capability: "domain.rereg.catalog", Action: "domain.rereg.catalog.fetch"})
	if cliErr := cliCode(t, err); cliErr.Code != cli.CodeContractDrift || cliErr.ExitCode != cli.ExitContractDrift {
		t.Fatalf("error = %#v", cliErr)
	}
	if _, _, err := fixture.store.Load(); err == nil {
		t.Fatal("an HTML answer was stored as the catalogue")
	}
}
