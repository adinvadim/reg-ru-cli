package domains

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/adinvadim/reg-ru-cli/internal/cli"
	"github.com/adinvadim/reg-ru-cli/internal/domain"
	"github.com/adinvadim/reg-ru-cli/internal/provider/regapi"
	"github.com/adinvadim/reg-ru-cli/internal/provider/rereg"
)

const (
	reregService       = "regru"
	reregProvider      = "www.reg.ru"
	catalogStaleAfter  = 12 * time.Hour
	maxContactFields   = 40
	contactsCredential = "rereg.contacts"
)

var contactKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

func (e *Executor) now() time.Time {
	if e.options.Now != nil {
		return e.options.Now()
	}
	return time.Now()
}

func (e *Executor) catalogStore() rereg.Store { return rereg.Store{Dir: e.options.CatalogDir} }

// openPublic opens one anonymous public page, starting on the small blitz
// document so the anti-bot interstitial clears before any real read.
func (e *Executor) openPublic(ctx context.Context) (rereg.Page, error) {
	if e.options.ReregPublic == nil {
		return nil, &rereg.Failure{Reason: rereg.ReasonMissingBrowser}
	}
	return e.options.ReregPublic.Open(ctx, rereg.BlitzPath)
}

func publicError(capability string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var failure *rereg.Failure
	if !errors.As(err, &failure) {
		return cli.NetworkError(reregProvider, true)
	}
	switch failure.Reason {
	case rereg.ReasonMissingBrowser:
		return cli.MissingBrowser()
	case rereg.ReasonDrift:
		return &cli.CLIError{
			Code: cli.CodeContractDrift, ExitCode: cli.ExitContractDrift,
			Message: "the public REG.RU backorder page no longer matches the expected format; nothing was read",
			Details: map[string]any{"capability": capability, "provider": reregProvider},
		}
	default:
		networkErr := cli.NetworkError(reregProvider, true)
		networkErr.Details["reason"] = failure.Reason
		return networkErr
	}
}

func failureReason(err error) string {
	var failure *rereg.Failure
	switch {
	case errors.As(err, &failure):
		return failure.Reason
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	default:
		return rereg.ReasonNetwork
	}
}

// ---- catalog ----

func (e *Executor) reregCatalogFetch(ctx context.Context, operation cli.Operation) (cli.Result, error) {
	page, err := e.openPublic(ctx)
	if err != nil {
		return cli.Result{}, publicError(operation.Capability, err)
	}
	defer page.Close()
	response, err := page.Get(ctx, rereg.CatalogPath)
	if err != nil {
		return cli.Result{}, publicError(operation.Capability, err)
	}
	if response.Status != 200 || strings.Contains(strings.ToLower(response.ContentType), "text/html") {
		return cli.Result{}, publicError(operation.Capability, &rereg.Failure{Reason: rereg.ReasonDrift})
	}
	meta, err := e.catalogStore().Save(response.Body, response.LastModified, e.now())
	if err != nil {
		var failure *rereg.Failure
		if errors.As(err, &failure) {
			return cli.Result{}, publicError(operation.Capability, err)
		}
		return cli.Result{}, cli.ConfigurationError("could not store the backorder catalogue snapshot in the user cache directory")
	}
	return renderCatalogFetch(meta, e.catalogStore().Path()), nil
}

func renderCatalogFetch(meta rereg.Metadata, path string) cli.Result {
	dates := make([]string, 0, len(meta.ReleaseDates))
	for date := range meta.ReleaseDates {
		dates = append(dates, date)
	}
	sort.Strings(dates)
	human := []string{fmt.Sprintf("Stored %d releasing names (%d with bids, %d rows skipped) from %s", meta.Rows, meta.WithBids, meta.Skipped, orDash(meta.LastModified))}
	for index, date := range dates {
		if index == 5 {
			human = append(human, fmt.Sprintf("  … %d more release dates", len(dates)-5))
			break
		}
		human = append(human, fmt.Sprintf("  %s  %d", date, meta.ReleaseDates[date]))
	}
	warnings := []cli.Warning{}
	if meta.Skipped > 0 {
		warnings = append(warnings, cli.Warning{Code: "catalog_rows_skipped", Message: fmt.Sprintf("%d catalogue rows were not valid .ru/.рф/.su lines and were skipped", meta.Skipped)})
	}
	return cli.Result{
		Human:    strings.Join(human, "\n"),
		Plain:    []string{strings.Join([]string{"snapshot", strconv.Itoa(meta.Rows), strconv.Itoa(meta.WithBids), strconv.Itoa(meta.Skipped), meta.FetchedAt.Format(time.RFC3339), escape(meta.LastModified), escape(path)}, "\t")},
		Data:     map[string]any{"snapshot": meta, "path": path},
		Warnings: warnings,
	}
}

func (e *Executor) reregCatalogSearch(_ context.Context, operation cli.Operation) (cli.Result, error) {
	rows, meta, err := e.catalogStore().Load()
	if errors.Is(err, rereg.ErrNoSnapshot) {
		return cli.Result{}, cli.ConfigurationError("no backorder catalogue snapshot; run regru domain rereg catalog fetch first")
	}
	if err != nil {
		return cli.Result{}, cli.ConfigurationError("the backorder catalogue snapshot could not be read; run regru domain rereg catalog fetch")
	}
	now := e.now()
	date := parameter(operation, "date")
	switch date {
	case "today":
		date = domain.MSKDate(now)
	case "tomorrow":
		date = domain.MSKDate(now.Add(24 * time.Hour))
	}
	zones := map[domain.Zone]bool{}
	if value := parameter(operation, "zones"); value != "" {
		parsed, err := parseZones(value)
		if err != nil {
			return cli.Result{}, cli.UsageError(err.Error())
		}
		for _, zone := range parsed {
			zones[zone] = true
		}
	}
	pattern := strings.ToLower(strings.TrimSpace(argument(operation, 0)))
	registrar := strings.ToUpper(strings.TrimSpace(parameter(operation, "registrar")))
	withBids := parameter(operation, "with-bids") == "true"
	limit, _ := strconv.Atoi(parameter(operation, "limit"))
	if limit <= 0 {
		limit = 50
	}
	matched := []rereg.Row{}
	for _, row := range rows {
		if date != "" && row.ReleaseDate != date ||
			len(zones) > 0 && !zones[row.Name.Zone] ||
			registrar != "" && strings.ToUpper(row.Registrar) != registrar ||
			withBids && !row.HasBids ||
			pattern != "" && !strings.Contains(row.Name.Unicode, pattern) && !strings.Contains(row.Name.ASCII, pattern) {
			continue
		}
		matched = append(matched, row)
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if matched[i].ReleaseDate != matched[j].ReleaseDate {
			return matched[i].ReleaseDate < matched[j].ReleaseDate
		}
		return matched[i].Name.ASCII < matched[j].Name.ASCII
	})
	total := len(matched)
	if len(matched) > limit {
		matched = matched[:limit]
	}
	warnings := []cli.Warning{}
	if now.Sub(meta.FetchedAt) > catalogStaleAfter {
		warnings = append(warnings, cli.Warning{Code: "catalog_stale", Message: "the catalogue snapshot is more than 12 hours old; run regru domain rereg catalog fetch"})
	}
	if total > len(matched) {
		warnings = append(warnings, cli.Warning{Code: "results_truncated", Message: fmt.Sprintf("%d of %d matches shown; raise --limit or narrow the search", len(matched), total)})
	}
	human := []string{fmt.Sprintf("%d match(es) in the snapshot fetched %s", total, meta.FetchedAt.In(domain.MSK).Format("2006-01-02 15:04 MSK"))}
	plain := make([]string, 0, len(matched))
	for _, row := range matched {
		bids := "no bids"
		if row.HasBids {
			bids = "has bids"
		}
		human = append(human, fmt.Sprintf("%s  release %s  %s  blitz %s RUB  %s", row.Name.Display(), row.ReleaseDate, bids, domain.Roubles(row.BlitzKopecks), row.Registrar))
		plain = append(plain, strings.Join([]string{
			escape(row.Name.Unicode), escape(row.Name.ASCII), row.ReleaseDate, strconv.FormatBool(row.HasBids),
			strconv.FormatInt(row.BlitzKopecks, 10), escape(row.Registrar), row.FirstRegistration,
		}, "\t"))
	}
	return cli.Result{
		Human: strings.Join(human, "\n"), Plain: plain, Warnings: warnings,
		Data: map[string]any{"results": matched, "total": total, "fetchedAt": meta.FetchedAt, "lastModified": meta.LastModified},
	}, nil
}

// ---- shared quote contract ----

// ReregQuote is the shared dropcatch quote object (all money in kopecks).
type ReregQuote struct {
	Service             string         `json:"service"`
	Domain              string         `json:"domain"`
	Name                domain.Name    `json:"name"`
	ReleaseDate         *string        `json:"releaseDate"`
	State               string         `json:"state"`
	Steps               []QuoteStep    `json:"steps"`
	CurrentTopKopecks   *int64         `json:"currentTopKopecks"`
	NextBidKopecks      *int64         `json:"nextBidKopecks"`
	CompetingBidVisible bool           `json:"competingBidVisible"`
	MyOrder             *MyOrder       `json:"myOrder"`
	BidDeadline         *string        `json:"bidDeadline"`
	AuctionUntil        *string        `json:"auctionUntil"`
	Balance             *QuoteBalance  `json:"balance"`
	Notes               []string       `json:"notes"`
	Currency            string         `json:"currency"`
	ObservedAt          string         `json:"observedAt"`
	BlitzKopecks        *int64         `json:"blitzKopecks"`
	Sources             map[string]any `json:"sources"`
}

// QuoteStep is one ladder price. REG.RU publishes no win probability.
type QuoteStep struct {
	ID             string   `json:"id"`
	AmountKopecks  int64    `json:"amountKopecks"`
	Available      bool     `json:"available"`
	WinProbability *float64 `json:"winProbability"`
}

// MyOrder is the account's own bid on the name.
type MyOrder struct {
	ID            string `json:"id"`
	AmountKopecks int64  `json:"amountKopecks"`
	Status        string `json:"status"`
}

// QuoteBalance compares the REG.RU prepaid balance with the next bid.
type QuoteBalance struct {
	AvailableKopecks int64 `json:"availableKopecks"`
	Sufficient       *bool `json:"sufficient"`
}

// Quote states of the shared contract.
const (
	stateReleasing   = "releasing"
	stateNotListed   = "not_listed"
	stateOrderedByMe = "ordered_by_me"
	stateUnknown     = "unknown"
)

// Bid statuses: an active lot is leading or outbid; a closed lot (blitz
// reached or bidding over) keeps whether the own bid was on top.
func bidStatus(bid regapi.ReregBid) string {
	user, _ := domain.Kopecks(bid.UserBid)
	top, topOK := domain.Kopecks(bid.MaxBid)
	leading := !topOK || user >= top
	switch {
	case bid.Active && leading:
		return "leading"
	case bid.Active:
		return "outbid"
	case leading:
		return "closed_leading"
	default:
		return "closed_outbid"
	}
}

func findBid(bids []regapi.ReregBid, name domain.Name) *regapi.ReregBid {
	for index := range bids {
		parsed, err := domain.Parse(bids[index].Name)
		if err == nil && parsed.ASCII == name.ASCII {
			return &bids[index]
		}
	}
	return nil
}

func ladderFor(ladders map[string][]string, zone domain.Zone) []int64 {
	for key, steps := range ladders {
		parsed, err := domain.ParseZone(key)
		if err != nil || parsed != zone {
			continue
		}
		result := make([]int64, 0, len(steps))
		for _, step := range steps {
			if kopecks, ok := domain.Kopecks(step); ok {
				result = append(result, kopecks)
			}
		}
		return result
	}
	return nil
}

func kopecksPointer(value string) *int64 {
	if value == "" {
		return nil
	}
	kopecks, ok := domain.Kopecks(value)
	if !ok {
		return nil
	}
	return &kopecks
}

func stringPointer(value string) *string { return &value }

type reregView struct {
	name        domain.Name
	lot         *rereg.Lot
	lotErr      error
	blitz       map[string]rereg.Blitz
	bids        *regapi.ReregBids
	bidsErr     error
	balance     *int64
	balanceErr  error
	catalogRow  *rereg.Row
	catalogMeta *rereg.Metadata
	catalogErr  error
	account     bool
}

// readPublicLot reads the zone blitz and the lot page in one browser.
func (e *Executor) readPublicLot(ctx context.Context, name domain.Name) (*rereg.Lot, map[string]rereg.Blitz, error) {
	page, err := e.openPublic(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer page.Close()
	var blitz map[string]rereg.Blitz
	if response, err := page.Get(ctx, rereg.BlitzPath); err == nil {
		blitz, _ = rereg.ParseBlitz(response)
	}
	response, err := page.Get(ctx, rereg.DetailsPath(name.ASCII))
	if err != nil {
		return nil, blitz, err
	}
	lot, err := rereg.ParseLot(response, name)
	if err != nil {
		return nil, blitz, err
	}
	return &lot, blitz, nil
}

func (e *Executor) reregQuote(ctx context.Context, operation cli.Operation) (cli.Result, error) {
	name, err := domain.Parse(argument(operation, 0))
	if err != nil || !domain.ReregZone(name.Zone) {
		return cli.Result{}, cli.UsageError("REG.RU backorders cover only .ru, .рф and .su names")
	}
	view := reregView{name: name}
	view.lot, view.blitz, view.lotErr = e.readPublicLot(ctx, name)
	if err := ctx.Err(); err != nil {
		return cli.Result{}, err
	}
	if operation.Account != "" && operation.Credentials != nil {
		view.account = true
		client, err := e.regapiClient(ctx, operation)
		if err != nil {
			translated := translateError(operation, err)
			if view.lotErr != nil {
				return cli.Result{}, translated
			}
			view.bidsErr, view.balanceErr = translated, translated
		} else {
			defer client.Close()
			if bids, err := client.GetUserReregBids(ctx); err == nil {
				view.bids = &bids
			} else {
				view.bidsErr = translateError(operation, err)
			}
			if balance, err := client.GetBalance(ctx, "RUR"); err == nil {
				view.balance = kopecksPointer(balance.Prepay)
			} else {
				view.balanceErr = translateError(operation, err)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return cli.Result{}, err
	}
	if view.lotErr != nil && (!view.account || view.bidsErr != nil) {
		return cli.Result{}, publicError(operation.Capability, view.lotErr)
	}
	if row, meta, found, err := e.catalogStore().Lookup(name); err == nil {
		view.catalogMeta = &meta
		if found {
			view.catalogRow = &row
		}
	} else {
		view.catalogErr = err
	}
	quote, warnings := buildQuote(view, e.now())
	return renderReregQuote(quote, warnings), nil
}

func buildQuote(view reregView, now time.Time) (ReregQuote, []cli.Warning) {
	name := view.name
	quote := ReregQuote{
		Service: reregService, Domain: name.Unicode, Name: name, State: stateUnknown,
		Steps: []QuoteStep{}, Notes: []string{}, Currency: "RUB",
		ObservedAt: now.UTC().Format(time.RFC3339), Sources: map[string]any{},
	}
	warnings := []cli.Warning{}
	var own *regapi.ReregBid
	var ladder []int64

	switch {
	case view.lot != nil:
		quote.Sources["public"] = "ok"
		ladder = view.lot.Ladder
	case view.lotErr != nil:
		quote.Sources["public"] = map[string]string{"state": "failed", "reason": failureReason(view.lotErr)}
		warnings = append(warnings, cli.Warning{Code: "public_lot_unavailable", Message: "the public lot page could not be read; next bid and blitz come from REG.API only"})
	}
	switch {
	case !view.account:
		quote.Sources["regapi"] = "no_account"
		quote.Notes = append(quote.Notes, "no account selected: own bid and balance are not included")
	case view.bids != nil:
		quote.Sources["regapi"] = "ok"
		own = findBid(view.bids.Bids, name)
		if ladder == nil {
			ladder = ladderFor(view.bids.Ladders, name.Zone)
		}
	default:
		quote.Sources["regapi"] = "failed"
		warnings = append(warnings, cli.Warning{Code: "own_bids_unavailable", Message: "REG.API domain/get_user_rereg_bids failed; an own bid on this name may be missing from the quote"})
	}

	// Release date: the own bid is authoritative; the catalogue snapshot otherwise.
	switch {
	case own != nil && own.DeleteDate != "":
		quote.ReleaseDate = stringPointer(own.DeleteDate)
		quote.Sources["releaseDate"] = "regapi"
	case view.catalogRow != nil:
		quote.ReleaseDate = stringPointer(view.catalogRow.ReleaseDate)
		quote.Sources["releaseDate"] = "catalog"
		if now.Sub(view.catalogMeta.FetchedAt) > catalogStaleAfter {
			warnings = append(warnings, cli.Warning{Code: "catalog_stale", Message: "the release date comes from a catalogue snapshot more than 12 hours old"})
		}
	case view.catalogMeta != nil:
		quote.Sources["releaseDate"] = "not_in_catalog"
	default:
		quote.Sources["releaseDate"] = "no_catalog"
		quote.Notes = append(quote.Notes, "release date unknown: run regru domain rereg catalog fetch to read it from the public catalogue")
	}
	releaseToday := quote.ReleaseDate != nil && *quote.ReleaseDate == domain.MSKDate(now)
	if quote.ReleaseDate != nil {
		if deadline, ok := domain.ReregBidDeadline(*quote.ReleaseDate); ok {
			quote.BidDeadline = stringPointer(deadline.Format(time.RFC3339))
			if !now.Before(deadline) {
				quote.Notes = append(quote.Notes, "the 16:45 MSK last-payment deadline has passed; REG.RU may treat later orders as invalid")
			}
		}
	}

	// Ceiling: the per-name blitz, lowered by the zone blitz on the release day.
	var ceiling *int64
	if view.lot != nil && view.lot.State == rereg.LotListed {
		ceiling = &view.lot.MaxKopecks
	} else if own != nil {
		ceiling = kopecksPointer(own.BlitzPrice)
	}
	if zoneBlitz, ok := view.blitz[string(name.Zone)]; ok && releaseToday && ceiling != nil && zoneBlitz.CurrentKopecks < *ceiling {
		lowered := zoneBlitz.CurrentKopecks
		ceiling = &lowered
	}
	quote.BlitzKopecks = ceiling

	// State, next bid, and current top.
	listed := view.lot != nil && view.lot.State == rereg.LotListed
	switch {
	case own != nil:
		quote.State = stateOrderedByMe
		amount, _ := domain.Kopecks(own.UserBid)
		quote.MyOrder = &MyOrder{ID: name.ASCII, AmountKopecks: amount, Status: bidStatus(*own)}
		quote.CurrentTopKopecks = kopecksPointer(own.MaxBid)
		if own.Active {
			quote.NextBidKopecks = kopecksPointer(own.NextPrice)
		}
		if quote.CurrentTopKopecks != nil && amount < *quote.CurrentTopKopecks {
			quote.CompetingBidVisible = true
		}
		quote.Notes = append(quote.Notes, "raising replaces the own bid; whether REG.RU charges the full new price or the difference is undocumented, so balance sufficiency uses the full next bid")
	case listed:
		quote.State = stateReleasing
		next := view.lot.MinKopecks
		quote.NextBidKopecks = &next
		if top, ok := stepBelow(view.lot.Ladder, next); ok {
			quote.CurrentTopKopecks = &top
			quote.CompetingBidVisible = true
			quote.Notes = append(quote.Notes, "currentTopKopecks is inferred as the ladder step below the minimum next bid; REG.RU does not publish bid amounts")
		}
	case view.lot != nil:
		quote.State = stateNotListed
		quote.Notes = append(quote.Notes, "REG.RU does not list this name in the backorder (not releasing, renewed, or already at the blitz price)")
	}
	if view.catalogRow != nil && view.catalogRow.HasBids && own == nil && !quote.CompetingBidVisible && listed {
		quote.Notes = append(quote.Notes, "the catalogue snapshot marks this name as having bids")
	}

	// Steps.
	floor := int64(-1)
	if quote.NextBidKopecks != nil {
		floor = *quote.NextBidKopecks
	}
	open := quote.State == stateReleasing || quote.MyOrder != nil && (quote.MyOrder.Status == "leading" || quote.MyOrder.Status == "outbid")
	for _, step := range ladder {
		if step <= 0 {
			continue
		}
		available := open && floor >= 0 && step >= floor && (ceiling == nil || step <= *ceiling)
		quote.Steps = append(quote.Steps, QuoteStep{ID: domain.Roubles(step), AmountKopecks: step, Available: available})
	}
	if ceiling != nil && open {
		quote.Notes = append(quote.Notes, fmt.Sprintf("a bid at the blitz price (%s RUB) closes the lot at once", domain.Roubles(*ceiling)))
	}

	// Balance.
	switch {
	case view.balance != nil:
		quote.Balance = &QuoteBalance{AvailableKopecks: *view.balance}
		if quote.NextBidKopecks != nil {
			sufficient := *view.balance >= *quote.NextBidKopecks
			quote.Balance.Sufficient = &sufficient
		}
	case view.account && view.balanceErr != nil:
		warnings = append(warnings, cli.Warning{Code: "balance_unavailable", Message: "REG.API user/get_balance failed; balance sufficiency is unknown"})
	}
	if open {
		quote.Notes = append(quote.Notes, "a paid bid cannot be cancelled; an outbid bid is refunded to the balance; REG.RU does not guarantee registration")
	}
	return quote, warnings
}

// stepBelow returns the highest positive ladder step strictly below next,
// when next is above the lowest positive step (meaning a bid exists).
func stepBelow(ladder []int64, next int64) (int64, bool) {
	positive := []int64{}
	for _, step := range ladder {
		if step > 0 {
			positive = append(positive, step)
		}
	}
	sort.Slice(positive, func(i, j int) bool { return positive[i] < positive[j] })
	if len(positive) == 0 || next <= positive[0] {
		return 0, false
	}
	best, found := int64(0), false
	for _, step := range positive {
		if step < next {
			best, found = step, true
		}
	}
	return best, found
}

func renderReregQuote(quote ReregQuote, warnings []cli.Warning) cli.Result {
	money := func(value *int64) string {
		if value == nil {
			return "-"
		}
		return domain.Roubles(*value) + " RUB"
	}
	lines := []string{fmt.Sprintf("%s: %s", quote.Name.Display(), quote.State)}
	lines = append(lines, fmt.Sprintf("  next bid %s, current top %s, blitz %s", money(quote.NextBidKopecks), money(quote.CurrentTopKopecks), money(quote.BlitzKopecks)))
	if quote.ReleaseDate != nil {
		deadline := "unknown"
		if parsed, err := time.Parse(time.RFC3339, deref(quote.BidDeadline)); err == nil {
			deadline = parsed.In(domain.MSK).Format("2006-01-02 15:04 MSK")
		}
		lines = append(lines, fmt.Sprintf("  release %s, last payment %s", *quote.ReleaseDate, deadline))
	}
	if quote.MyOrder != nil {
		lines = append(lines, fmt.Sprintf("  own bid %s RUB (%s)", domain.Roubles(quote.MyOrder.AmountKopecks), quote.MyOrder.Status))
	}
	if quote.Balance != nil {
		sufficient := "unknown"
		if quote.Balance.Sufficient != nil {
			sufficient = strconv.FormatBool(*quote.Balance.Sufficient)
		}
		lines = append(lines, fmt.Sprintf("  balance %s RUB, enough for next bid: %s", domain.Roubles(quote.Balance.AvailableKopecks), sufficient))
	}
	for _, note := range quote.Notes {
		lines = append(lines, "  note: "+note)
	}
	optional := func(value *int64) string {
		if value == nil {
			return ""
		}
		return strconv.FormatInt(*value, 10)
	}
	myAmount, myStatus := "", ""
	if quote.MyOrder != nil {
		myAmount, myStatus = strconv.FormatInt(quote.MyOrder.AmountKopecks, 10), quote.MyOrder.Status
	}
	balance := ""
	if quote.Balance != nil {
		balance = strconv.FormatInt(quote.Balance.AvailableKopecks, 10)
	}
	return cli.Result{
		Human: strings.Join(lines, "\n"),
		Plain: []string{strings.Join([]string{
			escape(quote.Name.Unicode), escape(quote.Name.ASCII), quote.State, optional(quote.NextBidKopecks),
			optional(quote.CurrentTopKopecks), optional(quote.BlitzKopecks), deref(quote.ReleaseDate), deref(quote.BidDeadline),
			myAmount, myStatus, balance,
		}, "\t")},
		Data:     quote,
		Warnings: warnings,
	}
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func orDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

// ---- bids ----

// ReregBidItem is one element of `domain rereg bids` data.
type ReregBidItem struct {
	Service        string      `json:"service"`
	Domain         string      `json:"domain"`
	Name           domain.Name `json:"name"`
	ID             string      `json:"id"`
	AmountKopecks  int64       `json:"amountKopecks"`
	Status         string      `json:"status"`
	Active         bool        `json:"active"`
	MaxBidKopecks  *int64      `json:"maxBidKopecks"`
	NextBidKopecks *int64      `json:"nextBidKopecks"`
	BlitzKopecks   *int64      `json:"blitzKopecks"`
	ReleaseDate    *string     `json:"releaseDate"`
	BidDeadline    *string     `json:"bidDeadline"`
	Registrar      string      `json:"registrar,omitempty"`
}

func (e *Executor) reregBids(ctx context.Context, operation cli.Operation) (cli.Result, error) {
	client, err := e.regapiClient(ctx, operation)
	if err != nil {
		return cli.Result{}, translateError(operation, err)
	}
	defer client.Close()
	bids, err := client.GetUserReregBids(ctx)
	if err != nil {
		return cli.Result{}, translateError(operation, err)
	}
	items := make([]ReregBidItem, 0, len(bids.Bids))
	human := []string{fmt.Sprintf("%d backorder bid(s)", len(bids.Bids))}
	plain := make([]string, 0, len(bids.Bids))
	for _, bid := range bids.Bids {
		name, err := domain.Parse(bid.Name)
		if err != nil {
			return cli.Result{}, cli.ProviderContractDrift("REG.API")
		}
		amount, _ := domain.Kopecks(bid.UserBid)
		item := ReregBidItem{
			Service: reregService, Domain: name.Unicode, Name: name, ID: name.ASCII,
			AmountKopecks: amount, Status: bidStatus(bid), Active: bid.Active,
			MaxBidKopecks: kopecksPointer(bid.MaxBid), NextBidKopecks: kopecksPointer(bid.NextPrice),
			BlitzKopecks: kopecksPointer(bid.BlitzPrice), Registrar: bid.Registrar,
		}
		if bid.DeleteDate != "" {
			item.ReleaseDate = stringPointer(bid.DeleteDate)
			if deadline, ok := domain.ReregBidDeadline(bid.DeleteDate); ok {
				item.BidDeadline = stringPointer(deadline.Format(time.RFC3339))
			}
		}
		items = append(items, item)
		human = append(human, fmt.Sprintf("%s  %s  own %s RUB, top %s, release %s", name.Display(), item.Status,
			domain.Roubles(amount), orDash(bid.MaxBid), orDash(bid.DeleteDate)))
		plain = append(plain, strings.Join([]string{
			escape(name.Unicode), escape(name.ASCII), item.Status, strconv.FormatInt(amount, 10),
			optionalKopecks(item.MaxBidKopecks), optionalKopecks(item.NextBidKopecks), bid.DeleteDate,
		}, "\t"))
	}
	return cli.Result{Human: strings.Join(human, "\n"), Plain: plain, Data: items}, nil
}

func optionalKopecks(value *int64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatInt(*value, 10)
}

// ---- bid ----

type reregCaps struct {
	MaxBidKopecks   int64 `json:"maxBidKopecks"`
	MaxDailyKopecks int64 `json:"maxDailyKopecks"`
	SpentToday      int64 `json:"spentTodayKopecks"`
	OverLimit       bool  `json:"overLimit"`
}

type bidPlan struct {
	name        domain.Name
	price       int64
	maxPrice    int64
	nameservers []string
	contact     string
	caps        reregCaps
	journalID   string
	balance     int64
	releaseDate string
	priorBid    int64
}

func amountSetting(value, fallback string) int64 {
	if value == "" {
		value = fallback
	}
	kopecks, _ := domain.Kopecks(value)
	return kopecks
}

func (e *Executor) reregBid(ctx context.Context, operation cli.Operation) (cli.Result, error) {
	name, err := domain.Parse(argument(operation, 0))
	if err != nil || !domain.ReregZone(name.Zone) {
		return cli.Result{}, cli.UsageError("REG.RU backorders cover only .ru, .рф and .su names")
	}
	price, priceOK := domain.Kopecks(parameter(operation, "price"))
	maxPrice, maxOK := domain.Kopecks(parameter(operation, "max-price"))
	if !priceOK || !maxOK || price <= 0 || price > maxPrice {
		return cli.Result{}, cli.UsageError("--price must be a positive amount no higher than --max-price")
	}
	nameservers, err := domain.ParseNameservers(parameter(operation, "ns"))
	if err != nil {
		return cli.Result{}, cli.UsageError(err.Error())
	}
	overLimit := parameter(operation, "over-limit") == "true"
	allowWindow := parameter(operation, "allow-release-window") == "true"
	now := e.now()
	if start, end, inside := domain.ReregGuardWindow(now); inside && !allowWindow {
		return cli.Result{}, cli.PolicyRefused("guard_window", fmt.Sprintf(
			"backorder bids are refused between %s and %s MSK; rerun later or pass --allow-release-window", start.Format("15:04"), end.Format("15:04"),
		), map[string]any{"override": "--allow-release-window"})
	}
	account, err := e.account(operation)
	if err != nil {
		return cli.Result{}, err
	}
	plan := bidPlan{
		name: name, price: price, maxPrice: maxPrice, nameservers: nameservers, contact: parameter(operation, "contact"),
		caps: reregCaps{
			MaxBidKopecks:   amountSetting(account.Domain.ReregMaxBid, domain.DefaultReregMaxBid),
			MaxDailyKopecks: amountSetting(account.Domain.ReregMaxDaily, domain.DefaultReregMaxDaily),
			OverLimit:       overLimit,
		},
	}
	request := regapi.ReregBidRequest{Name: name.ASCII, Price: domain.Roubles(price), Nameservers: nameservers}
	switch plan.contact {
	case "profile":
		if account.Domain.ContactProfileType == "" || account.Domain.ContactProfileName == "" {
			return cli.Result{}, cli.ConfigurationError("--contact profile needs contact_profile_type and contact_profile_name under [accounts." + operation.Account + ".domain]")
		}
		request.ProfileType, request.ProfileName = account.Domain.ContactProfileType, account.Domain.ContactProfileName
	case "credential":
		contacts, err := e.reregContacts(ctx, operation)
		if err != nil {
			return cli.Result{}, err
		}
		request.Contacts = contacts
	default:
		return cli.Result{}, cli.UsageError("--contact must be profile or credential")
	}
	if price > plan.caps.MaxBidKopecks && !overLimit {
		return cli.Result{}, cli.PolicyRefused("spend_limit_exceeded", fmt.Sprintf(
			"the bid %s RUB is above the per-bid cap %s RUB (rereg_max_bid); pass --over-limit after approval",
			domain.Roubles(price), domain.Roubles(plan.caps.MaxBidKopecks),
		), map[string]any{"limit": "per_bid", "override": "--over-limit", "priceKopecks": price, "capKopecks": plan.caps.MaxBidKopecks})
	}

	journal := rereg.Journal{Dir: e.options.JournalDir, ProfileID: operation.ProfileID}
	lock, err := journal.Lock()
	if errors.Is(err, rereg.ErrJournalBusy) {
		refused := cli.PolicyRefused("bid_in_progress", "another backorder bid is in progress for this account; wait for it to finish", nil)
		refused.Retryable = true
		return cli.Result{}, refused
	}
	if err != nil {
		return cli.Result{}, cli.ConfigurationError("the backorder bid journal is unavailable in the user config directory")
	}
	defer lock.Release()
	spend, err := journal.DailySpend(rereg.Today(now))
	if err != nil {
		return cli.Result{}, cli.ConfigurationError("the backorder bid journal could not be read; daily spending cannot be evaluated")
	}
	plan.caps.SpentToday = spend.AmountKopecks
	if spend.AmountKopecks+price > plan.caps.MaxDailyKopecks && !overLimit {
		return cli.Result{}, cli.PolicyRefused("spend_limit_exceeded", fmt.Sprintf(
			"the bid would bring today's backorder spending to %s RUB, above the daily cap %s RUB (rereg_max_daily); pass --over-limit after approval",
			domain.Roubles(spend.AmountKopecks+price), domain.Roubles(plan.caps.MaxDailyKopecks),
		), map[string]any{"limit": "daily", "override": "--over-limit", "spentTodayKopecks": spend.AmountKopecks, "priceKopecks": price, "capKopecks": plan.caps.MaxDailyKopecks})
	}

	client, err := e.regapiClient(ctx, operation)
	if err != nil {
		return cli.Result{}, translateError(operation, err)
	}
	defer client.Close()
	bids, err := client.GetUserReregBids(ctx)
	if err != nil {
		return cli.Result{}, translateError(operation, err)
	}
	own := findBid(bids.Bids, name)
	if own != nil {
		plan.priorBid, _ = domain.Kopecks(own.UserBid)
		if plan.priorBid >= price {
			return cli.Result{}, cli.PolicyRefused("existing_bid", fmt.Sprintf(
				"the account already bids %s RUB on %s; nothing was sent", domain.Roubles(plan.priorBid), name.Display(),
			), map[string]any{"domain": name.Display(), "ownBidKopecks": plan.priorBid, "status": bidStatus(*own)})
		}
		if !own.Active {
			return cli.Result{}, cli.PolicyRefused("lot_closed", "REG.API reports the lot as closed for bidding; nothing was sent", map[string]any{"domain": name.Display()})
		}
	}
	ladder := ladderFor(bids.Ladders, name.Zone)
	if !containsStep(ladder, price) {
		return cli.Result{}, cli.UsageError(fmt.Sprintf("--price %s RUB is not a REG.RU ladder step; nearby steps: %s", domain.Roubles(price), nearbySteps(ladder, price)))
	}
	warnings := []cli.Warning{}
	lot, _, lotErr := e.readPublicLot(ctx, name)
	if err := ctx.Err(); err != nil {
		return cli.Result{}, err
	}
	switch {
	case lotErr != nil:
		warnings = append(warnings, cli.Warning{Code: "public_lot_unverified", Message: "the public lot page could not be read, so the minimum next bid was not checked locally"})
	case lot.State == rereg.LotNotListed && own == nil:
		return cli.Result{}, cli.PolicyRefused("not_listed", "REG.RU does not list this name in the backorder; nothing was sent", map[string]any{"domain": name.Display()})
	case lot.State == rereg.LotListed && price < lot.MinKopecks:
		return cli.Result{}, cli.UsageError(fmt.Sprintf("--price %s RUB is below the minimum next bid %s RUB", domain.Roubles(price), domain.Roubles(lot.MinKopecks)))
	case lot.State == rereg.LotListed && price > lot.MaxKopecks:
		return cli.Result{}, cli.UsageError(fmt.Sprintf("--price %s RUB is above the blitz price %s RUB", domain.Roubles(price), domain.Roubles(lot.MaxKopecks)))
	}
	if own != nil && own.NextPrice != "" {
		if next, ok := domain.Kopecks(own.NextPrice); ok && price < next {
			return cli.Result{}, cli.UsageError(fmt.Sprintf("--price %s RUB is below the minimum next bid %s RUB", domain.Roubles(price), domain.Roubles(next)))
		}
	}
	switch {
	case own != nil && own.DeleteDate != "":
		plan.releaseDate = own.DeleteDate
	default:
		if row, _, found, err := e.catalogStore().Lookup(name); err == nil && found {
			plan.releaseDate = row.ReleaseDate
		}
	}
	if plan.releaseDate == "" {
		warnings = append(warnings, cli.Warning{Code: "release_date_unknown", Message: "the release date is unknown (no own bid, no catalogue snapshot entry), so the 16:45 MSK deadline was not checked"})
	} else if deadline, ok := domain.ReregBidDeadline(plan.releaseDate); ok && !now.Before(deadline) && !allowWindow {
		return cli.Result{}, cli.PolicyRefused("deadline_passed", fmt.Sprintf(
			"the last payment for %s was due at %s; REG.RU may treat later orders as invalid", name.Display(), deadline.Format("2006-01-02 15:04 MSK"),
		), map[string]any{"override": "--allow-release-window", "bidDeadline": deadline.Format(time.RFC3339)})
	}

	balance, err := client.GetBalance(ctx, "RUR")
	if err != nil {
		return cli.Result{}, translateError(operation, err)
	}
	available, ok := domain.Kopecks(balance.Prepay)
	if !ok {
		return cli.Result{}, cli.ProviderContractDrift("REG.API")
	}
	plan.balance = available
	if available < price {
		return cli.Result{}, cli.InsufficientFunds(domain.Roubles(price), balance.Prepay, domain.Roubles(price-available), "RUR", false)
	}
	if err := ctx.Err(); err != nil {
		return cli.Result{}, err
	}

	plan.journalID, err = rereg.NewID()
	if err != nil {
		return cli.Result{}, cli.ConfigurationError("could not create a bid journal entry")
	}
	day := rereg.Today(now)
	entry := rereg.Entry{ID: plan.journalID, Domain: name.ASCII, AmountKopecks: price, State: rereg.StateIntent, Day: day, At: now.UTC().Format(time.RFC3339)}
	if err := journal.Append(entry); err != nil {
		return cli.Result{}, cli.ConfigurationError("the bid journal could not be written; nothing was sent")
	}
	record := func(state, billID string) {
		entry.State, entry.BillID, entry.At = state, billID, e.now().UTC().Format(time.RFC3339)
		_ = journal.Append(entry)
	}

	result, err := client.SetReregBid(ctx, request)
	if err == nil {
		record(rereg.StateConfirmed, result.BillID)
		return renderReregBid(plan, result, false, warnings), nil
	}
	return e.reregBidFailure(ctx, operation, client, plan, err, record, warnings)
}

func (e *Executor) reregContacts(ctx context.Context, operation cli.Operation) (map[string]string, error) {
	if operation.Credentials == nil {
		return nil, cli.ConfigurationError("--contact credential needs a credential_process that returns rereg.contacts")
	}
	raw, err := operation.Credentials.Resolve(ctx, contactsCredential)
	if err != nil {
		return nil, err
	}
	defer wipe(raw)
	var contacts map[string]string
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	if err := decoder.Decode(&contacts); err != nil || len(contacts) == 0 || len(contacts) > maxContactFields {
		return nil, cli.ConfigurationError("the credential-process field rereg.contacts must be a JSON object of REG.API contact fields with string values")
	}
	for key, value := range contacts {
		if !contactKeyPattern.MatchString(key) || strings.TrimSpace(value) == "" {
			return nil, cli.ConfigurationError("the credential-process field rereg.contacts must be a JSON object of REG.API contact fields with string values")
		}
	}
	return contacts, nil
}

func containsStep(ladder []int64, price int64) bool {
	for _, step := range ladder {
		if step == price && step > 0 {
			return true
		}
	}
	return false
}

func nearbySteps(ladder []int64, price int64) string {
	sorted := []int64{}
	for _, step := range ladder {
		if step > 0 {
			sorted = append(sorted, step)
		}
	}
	if len(sorted) == 0 {
		return "none (REG.API returned no ladder for the zone)"
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	index := sort.Search(len(sorted), func(i int) bool { return sorted[i] >= price })
	parts := []string{}
	for _, candidate := range []int{index - 1, index} {
		if candidate >= 0 && candidate < len(sorted) {
			parts = append(parts, domain.Roubles(sorted[candidate]))
		}
	}
	return strings.Join(parts, ", ")
}

// ReregEvidence is collected after an ambiguous bid; it never triggers a resend.
type ReregEvidence struct {
	Trigger        string `json:"trigger"`
	JournalID      string `json:"journalId"`
	BidLookup      string `json:"bidLookup"`
	OwnBidKopecks  *int64 `json:"ownBidKopecks,omitempty"`
	BalanceBefore  int64  `json:"balanceBeforeKopecks"`
	BalanceAfter   *int64 `json:"balanceAfterKopecks,omitempty"`
	BalanceChanged *bool  `json:"balanceChanged,omitempty"`
}

func (e *Executor) reregBidFailure(
	ctx context.Context,
	operation cli.Operation,
	client *regapi.Client,
	plan bidPlan,
	err error,
	record func(state, billID string),
	warnings []cli.Warning,
) (cli.Result, error) {
	var ambiguous *regapi.AmbiguousMutationError
	if errors.As(err, &ambiguous) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return e.reconcileReregBid(ctx, operation, client, plan, record, warnings)
	}
	record(rereg.StateRejected, "")
	var apiErr *regapi.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.Code == "NOT_ENOUGH_MONEY":
			return cli.Result{}, cli.InsufficientFunds(domain.Roubles(plan.price), domain.Roubles(plan.balance), domain.Roubles(max(plan.price-plan.balance, 0)), "RUR", true)
		case identificationCode(apiErr.Code):
			return cli.Result{}, cli.IdentificationRequired(apiErr.Code)
		}
	}
	var itemErr *regapi.DomainItemError
	if errors.As(err, &itemErr) {
		rejected := cli.ProviderError("REG.API", itemErr.Code, 200, false, "")
		rejected.Message = "REG.API refused the bid; nothing was charged"
		rejected.Details["bid_created"] = false
		return cli.Result{}, rejected
	}
	translated := translateError(operation, err)
	var cliErr *cli.CLIError
	if errors.As(translated, &cliErr) && cliErr.Details != nil {
		cliErr.Details["bid_created"] = false
	}
	return cli.Result{}, translated
}

func (e *Executor) reconcileReregBid(
	ctx context.Context,
	operation cli.Operation,
	client *regapi.Client,
	plan bidPlan,
	record func(state, billID string),
	warnings []cli.Warning,
) (cli.Result, error) {
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), e.options.ReconcileTimeout)
	defer cancel()
	evidence := ReregEvidence{Trigger: "dispatch_outcome_unknown", JournalID: plan.journalID, BalanceBefore: plan.balance}
	bids, err := client.GetUserReregBids(reconcileCtx)
	switch {
	case err != nil:
		evidence.BidLookup = "failed"
	default:
		own := findBid(bids.Bids, plan.name)
		if own == nil {
			evidence.BidLookup = "not_found"
			break
		}
		amount, _ := domain.Kopecks(own.UserBid)
		evidence.OwnBidKopecks = &amount
		if amount >= plan.price {
			record(rereg.StateConfirmed, "")
			warnings = append(warnings, cli.Warning{Code: "reconciled", Message: "the bid was confirmed by domain/get_user_rereg_bids after an ambiguous dispatch"})
			result := renderReregBid(plan, regapi.ReregBidResult{Name: plan.name.ASCII}, true, warnings)
			if data, ok := result.Data.(map[string]any); ok {
				data["evidence"] = evidence
			}
			return result, nil
		}
		evidence.BidLookup = "found_lower"
	}
	if balance, err := client.GetBalance(reconcileCtx, "RUR"); err == nil {
		if after, ok := domain.Kopecks(balance.Prepay); ok {
			changed := after != plan.balance
			evidence.BalanceAfter, evidence.BalanceChanged = &after, &changed
		}
	}
	record(rereg.StateUnknown, "")
	return cli.Result{}, cli.OutcomeUnknownWithDetails(
		operation.Capability,
		"the bid may have been charged but could not be confirmed; do not resend: check regru domain rereg bids and the balance first",
		map[string]any{"domain": plan.name.Display(), "priceKopecks": plan.price, "journalId": plan.journalID, "evidence": evidence,
			"reconcileWith": "regru domain rereg bids"},
	)
}

func renderReregBid(plan bidPlan, result regapi.ReregBidResult, reconciled bool, warnings []cli.Warning) cli.Result {
	data := map[string]any{
		"service": reregService, "domain": plan.name.Unicode, "name": plan.name, "outcome": "committed",
		"priceKopecks": plan.price, "maxPriceKopecks": plan.maxPrice, "billId": result.BillID, "payment": result.Payment,
		"serviceId": result.ServiceID, "journalId": plan.journalID, "reconciled": reconciled,
		"previousBidKopecks": nil, "caps": plan.caps, "nameservers": plan.nameservers, "contact": plan.contact,
		"releaseDate": nil, "bidDeadline": nil,
		"next": "check regru domain rereg bids; an outbid bid is refunded to the balance",
	}
	if plan.priorBid > 0 {
		data["previousBidKopecks"] = plan.priorBid
	}
	if plan.releaseDate != "" {
		data["releaseDate"] = plan.releaseDate
		if deadline, ok := domain.ReregBidDeadline(plan.releaseDate); ok {
			data["bidDeadline"] = deadline.Format(time.RFC3339)
		}
	}
	if plan.caps.OverLimit && (plan.price > plan.caps.MaxBidKopecks || plan.caps.SpentToday+plan.price > plan.caps.MaxDailyKopecks) {
		warnings = append(warnings, cli.Warning{Code: "spending_cap_overridden", Message: "--over-limit placed a bid above the configured backorder caps"})
	}
	return cli.Result{
		Human: fmt.Sprintf("Bid %s RUB on %s, bill %s; check regru domain rereg bids", domain.Roubles(plan.price), plan.name.Display(), orDash(result.BillID)),
		Plain: []string{strings.Join([]string{
			"committed", escape(plan.name.Unicode), escape(plan.name.ASCII), strconv.FormatInt(plan.price, 10),
			escape(result.BillID), plan.journalID,
		}, "\t")},
		Data:     data,
		Warnings: warnings,
	}
}
