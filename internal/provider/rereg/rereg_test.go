package rereg

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adinvadim/reg-ru-cli/internal/domain"
)

// detailsPage mirrors the observed embedded JSON of rereg_details?dname=.
const detailsPage = `<html><script>
        window.pDomainReregDetails = {"domain":{"isDomainDeletionBlitz":"","zone":"ru","maxInstalment":400000,"name":"moonbot.ru","isInstallmentAllowed":"","minInstalment":450,"minPrice":"350900.00","bids":["0","450","1950","5950","11950","17950","25500","49950","69900","99900","190900","350900","450000","675000","900000","1600000","2300000","3200000","4500000","7500000","10000000"],"maxPrice":"10000000.00"}};
    </script><script src="/dist/domain-new-rereg-details.js"></script></html>`

// listPage is what REG.RU serves for a name that is not in the backorder.
const listPage = `<html><script>window.pDomainReregFilter = {}; window.pDomainRereg = {"domainsArray":[]};</script></html>`

func mustName(t *testing.T, value string) domain.Name {
	t.Helper()
	name, err := domain.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return name
}

func TestParseLot(t *testing.T) {
	lot, err := ParseLot(Response{Status: 200, Body: detailsPage}, mustName(t, "moonbot.ru"))
	if err != nil {
		t.Fatal(err)
	}
	if lot.State != LotListed || lot.MinKopecks != 35090000 || lot.MaxKopecks != 1000000000 || len(lot.Ladder) != 21 || lot.Ladder[1] != 45000 || lot.DeletionBlitz {
		t.Fatalf("lot = %#v", lot)
	}
	if lot, err := ParseLot(Response{Status: 200, Body: listPage}, mustName(t, "google.ru")); err != nil || lot.State != LotNotListed {
		t.Fatalf("unlisted = %#v, %v", lot, err)
	}
	for _, check := range []struct {
		body string
		name string
	}{
		{"<html>maintenance</html>", "moonbot.ru"},
		{strings.Replace(detailsPage, `"minPrice":"350900.00"`, `"minPrice":"soon"`, 1), "moonbot.ru"},
		{detailsPage, "other.ru"}, // the page answered for a different name
	} {
		var failure *Failure
		if _, err := ParseLot(Response{Status: 200, Body: check.body}, mustName(t, check.name)); !errors.As(err, &failure) || failure.Reason != ReasonDrift {
			t.Errorf("ParseLot(%.40q, %s) err = %v, want drift", check.body, check.name, err)
		}
	}
	var failure *Failure
	if _, err := ParseLot(Response{Status: 503}, mustName(t, "moonbot.ru")); !errors.As(err, &failure) || failure.Reason != ReasonNetwork {
		t.Fatalf("503 err = %v", err)
	}
}

func TestParseBlitz(t *testing.T) {
	blitz, err := ParseBlitz(Response{Status: 200, Body: `{"su":{"cur_bid":"3200000","time_bid":1794995100,"max_bid":"10000000","min_bid":"450","time_left":63},"рф":{"cur_bid":"3200000","min_bid":"450","max_bid":"10000000","time_bid":1794995100}}`})
	if err != nil {
		t.Fatal(err)
	}
	if blitz["su"].CurrentKopecks != 320000000 || blitz["рф"].MinKopecks != 45000 || blitz["su"].NextStepAt == nil || *blitz["su"].NextStepAt != 1794995100 {
		t.Fatalf("blitz = %#v", blitz)
	}
	if _, err := ParseBlitz(Response{Status: 200, Body: "<html>"}); err == nil {
		t.Fatal("HTML blitz answer was accepted")
	}
}

func catalogBody(rows int) string {
	lines := []string{"\ufeffДомен;Ставки;Текущ.блиц,руб.;Перв.регистрация;Дата удаления;Регистратор"}
	lines = append(lines,
		"moonbot.ru;есть;10000000.00;2012-03-31;2026-11-19;REGRU",
		"домкрай.рф;нет;3200000.00;;2026-11-18;RUCENTER",
		"академия.su;нет;10000000.00;2008-04-28;2026-11-25;REGRU", // Cyrillic .su is outside the CLI name rules
		"broken.ru;может;10000000.00;;2026-11-19;REGRU",
	)
	for index := 0; index < rows; index++ {
		lines = append(lines, fmt.Sprintf("name%04d.ru;нет;10000000.00;;2026-11-20;BEGET", index))
	}
	return strings.Join(lines, "\r\n") + "\r\n"
}

func TestParseCatalog(t *testing.T) {
	rows, skipped, err := ParseCatalog(catalogBody(1))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || skipped != 2 {
		t.Fatalf("rows = %d skipped = %d", len(rows), skipped)
	}
	if !rows[0].HasBids || rows[0].BlitzKopecks != 1000000000 || rows[0].ReleaseDate != "2026-11-19" || rows[0].FirstRegistration != "2012-03-31" {
		t.Fatalf("row 0 = %#v", rows[0])
	}
	if rows[1].Name.ASCII != "xn--80ahqcjlq.xn--p1ai" || rows[1].HasBids || rows[1].Registrar != "RUCENTER" {
		t.Fatalf("row 1 = %#v", rows[1])
	}
	if _, _, err := ParseCatalog("Domain;Bids\r\nmoonbot.ru;yes\r\n"); err == nil {
		t.Fatal("a foreign header was accepted")
	}
}

func TestStoreSavesValidatesAndLooksUp(t *testing.T) {
	store := Store{Dir: filepath.Join(t.TempDir(), "rereg")}
	if _, _, err := store.Load(); !errors.Is(err, ErrNoSnapshot) {
		t.Fatalf("empty store err = %v", err)
	}
	if _, err := store.Save(catalogBody(10), "", time.Now()); err == nil {
		t.Fatal("a truncated catalogue was stored")
	}
	fetched := time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC)
	meta, err := store.Save(catalogBody(minCatalogRows), "Wed, 18 Nov 2026 06:32:36 GMT", fetched)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Rows != minCatalogRows+2 || meta.Skipped != 2 || meta.WithBids != 1 || meta.Zones["рф"] != 1 || meta.ReleaseDates["2026-11-20"] != minCatalogRows {
		t.Fatalf("meta = %#v", meta)
	}
	info, err := os.Stat(store.Path())
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("snapshot mode = %v, %v", info, err)
	}
	row, loaded, found, err := store.Lookup(mustName(t, "ДОМКРАЙ.рф"))
	if err != nil || !found || row.ReleaseDate != "2026-11-18" || !loaded.FetchedAt.Equal(fetched) {
		t.Fatalf("lookup = %#v %#v %v %v", row, loaded, found, err)
	}
	if _, _, found, err := store.Lookup(mustName(t, "absent.ru")); err != nil || found {
		t.Fatalf("absent lookup = %v %v", found, err)
	}
	// A snapshot that no longer matches its checksum is treated as missing.
	if err := os.WriteFile(store.Path(), []byte(catalogBody(minCatalogRows+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Load(); !errors.Is(err, ErrNoSnapshot) {
		t.Fatalf("tampered snapshot err = %v", err)
	}
}

func TestJournalDailySpendCountsCommittedAndUnknownBids(t *testing.T) {
	journal := Journal{Dir: t.TempDir(), ProfileID: "p_aaaaaaaaaaaaaaaaaaaaaaaaaa"}
	if spend, err := journal.DailySpend("2026-11-19"); err != nil || spend.AmountKopecks != 0 {
		t.Fatalf("empty spend = %#v, %v", spend, err)
	}
	record := func(id string, amount int64, day string, states ...string) {
		for _, state := range states {
			if err := journal.Append(Entry{ID: id, Domain: "a.ru", AmountKopecks: amount, State: state, Day: day, At: "2026-11-19T09:00:00Z"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	record("0000000000000001", 195000, "2026-11-19", StateIntent, StateConfirmed)
	record("0000000000000002", 45000, "2026-11-19", StateIntent, StateRejected)
	record("0000000000000003", 595000, "2026-11-19", StateIntent, StateUnknown)
	record("0000000000000004", 11950, "2026-11-19", StateIntent) // crashed before the outcome was written
	record("0000000000000005", 99900, "2026-11-18", StateIntent, StateConfirmed)
	spend, err := journal.DailySpend("2026-11-19")
	if err != nil {
		t.Fatal(err)
	}
	if spend.AmountKopecks != 195000+595000+11950 || spend.Bids != 3 {
		t.Fatalf("spend = %#v", spend)
	}
	if err := journal.Append(Entry{ID: "../evil", State: StateIntent, Day: "2026-11-19"}); err == nil {
		t.Fatal("an invalid journal ID was accepted")
	}
}

func TestJournalLockIsExclusive(t *testing.T) {
	journal := Journal{Dir: t.TempDir(), ProfileID: "p_aaaaaaaaaaaaaaaaaaaaaaaaaa"}
	first, err := journal.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := journal.Lock(); !errors.Is(err, ErrJournalBusy) {
		t.Fatalf("second lock err = %v, want busy", err)
	}
	first.Release()
	second, err := journal.Lock()
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	second.Release()
}
