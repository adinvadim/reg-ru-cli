// Package rereg holds the REG.RU backorder (освобождающиеся домены) data that
// comes from public www.reg.ru pages, the local catalogue snapshot, and the
// local bid journal used for daily spending caps.
package rereg

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"

	"github.com/adinvadim/reg-ru-cli/internal/domain"
	"github.com/adinvadim/reg-ru-cli/internal/provider/portal/cdp"
)

// Public paths on www.reg.ru.
const (
	BlitzPath   = "/domain/new/rereg/blitz_info"
	CatalogPath = "/static_files/rereg_extlist.csv"
	detailsPath = "/domain/new/rereg_details?dname="
)

// Response is one public GET answer read from inside a browser page.
type Response struct {
	Status       int
	ContentType  string
	LastModified string
	Body         string
}

// Page reads public www.reg.ru paths. Production uses an anonymous headless
// browser so the anti-bot interstitial clears the way it does for people.
type Page interface {
	Get(ctx context.Context, path string) (Response, error)
	Close() error
}

// Opener opens a Page; start is the first path loaded.
type Opener interface {
	Open(ctx context.Context, start string) (Page, error)
}

// Failure is a typed public-read failure with a stable reason.
type Failure struct {
	Reason string
	Cause  error
}

func (f *Failure) Error() string { return "public REG.RU read failed: " + f.Reason }
func (f *Failure) Unwrap() error { return f.Cause }

// Stable failure reasons.
const (
	ReasonMissingBrowser = "missing_browser"
	ReasonChallenge      = "anti_bot_challenge"
	ReasonNetwork        = "public_request_failed"
	ReasonDrift          = "public_contract_drift"
)

// BrowserOpener adapts cdp.PublicBrowser to Opener.
type BrowserOpener struct{ Browser *cdp.PublicBrowser }

// Open implements Opener.
func (o BrowserOpener) Open(ctx context.Context, start string) (Page, error) {
	if o.Browser == nil {
		return nil, &Failure{Reason: ReasonMissingBrowser}
	}
	page, err := o.Browser.Open(ctx, cdp.PublicOrigin+start)
	if err != nil {
		return nil, classify(ctx, err)
	}
	return browserPage{page: page}, nil
}

type browserPage struct{ page *cdp.PublicPage }

func (p browserPage) Get(ctx context.Context, path string) (Response, error) {
	response, err := p.page.Get(ctx, path)
	if err != nil {
		return Response{}, classify(ctx, err)
	}
	return Response(response), nil
}

func (p browserPage) Close() error { return p.page.Close() }

func classify(ctx context.Context, err error) error {
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, cdp.ErrBrowserNotFound):
		return &Failure{Reason: ReasonMissingBrowser, Cause: err}
	case errors.Is(err, cdp.ErrPublicChallenge):
		return &Failure{Reason: ReasonChallenge, Cause: err}
	default:
		return &Failure{Reason: ReasonNetwork, Cause: err}
	}
}

// Blitz is the current blitz ceiling of one zone from blitz_info. A bid at the
// ceiling closes the lot; on a release day the ceiling steps down between
// 12:10 and 16:45 MSK.
type Blitz struct {
	CurrentKopecks int64  `json:"currentKopecks"`
	MinKopecks     int64  `json:"minKopecks"`
	MaxKopecks     int64  `json:"maxKopecks"`
	NextStepAt     *int64 `json:"nextStepAt"`
}

// ParseBlitz decodes blitz_info into a zone-keyed map (zones in Unicode form).
func ParseBlitz(response Response) (map[string]Blitz, error) {
	if response.Status != 200 {
		return nil, &Failure{Reason: ReasonNetwork}
	}
	var wire map[string]struct {
		Current json.RawMessage `json:"cur_bid"`
		Min     json.RawMessage `json:"min_bid"`
		Max     json.RawMessage `json:"max_bid"`
		TimeBid json.RawMessage `json:"time_bid"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(response.Body)), &wire); err != nil || len(wire) == 0 {
		return nil, &Failure{Reason: ReasonDrift}
	}
	result := make(map[string]Blitz, len(wire))
	for zone, item := range wire {
		current, currentOK := jsonKopecks(item.Current)
		minimum, minOK := jsonKopecks(item.Min)
		maximum, maxOK := jsonKopecks(item.Max)
		if !currentOK || !minOK || !maxOK {
			return nil, &Failure{Reason: ReasonDrift}
		}
		blitz := Blitz{CurrentKopecks: current, MinKopecks: minimum, MaxKopecks: maximum}
		var next int64
		if err := json.Unmarshal(item.TimeBid, &next); err == nil && next > 0 {
			blitz.NextStepAt = &next
		}
		parsed, err := domain.ParseZone(zone)
		if err != nil {
			continue
		}
		result[string(parsed)] = blitz
	}
	return result, nil
}

// LotState says whether REG.RU lists a name in the backorder.
type LotState string

const (
	LotListed    LotState = "listed"
	LotNotListed LotState = "not_listed"
)

// Lot is the public per-name backorder view (rereg_details).
type Lot struct {
	State LotState
	// MinKopecks is the lowest bid REG.RU accepts now (the next ladder step
	// above the current top bid). MaxKopecks is the per-name blitz ceiling.
	MinKopecks int64
	MaxKopecks int64
	Ladder     []int64
	// DeletionBlitz is REG.RU's flag that the release-day blitz scaledown applies.
	DeletionBlitz bool
}

var (
	detailsPattern = regexp.MustCompile(`(?s)window\.pDomainReregDetails\s*=\s*(\{.*?\})\s*;\s*</script>`)
	listPattern    = regexp.MustCompile(`window\.pDomainRereg\s*=`)
)

// DetailsPath returns the public details path for an ASCII name.
func DetailsPath(ascii string) string { return detailsPath + url.QueryEscape(ascii) }

// ParseLot decodes the embedded pDomainReregDetails JSON. A name that is not
// listed gets the general backorder list page instead; anything else is drift.
func ParseLot(response Response, name domain.Name) (Lot, error) {
	if response.Status != 200 {
		return Lot{}, &Failure{Reason: ReasonNetwork}
	}
	match := detailsPattern.FindStringSubmatch(response.Body)
	if match == nil {
		if listPattern.MatchString(response.Body) {
			return Lot{State: LotNotListed}, nil
		}
		return Lot{}, &Failure{Reason: ReasonDrift}
	}
	var wire struct {
		Domain *struct {
			Name          string            `json:"name"`
			Zone          string            `json:"zone"`
			MinPrice      json.RawMessage   `json:"minPrice"`
			MaxPrice      json.RawMessage   `json:"maxPrice"`
			Bids          []json.RawMessage `json:"bids"`
			DeletionBlitz json.RawMessage   `json:"isDomainDeletionBlitz"`
		} `json:"domain"`
	}
	if err := json.Unmarshal([]byte(match[1]), &wire); err != nil || wire.Domain == nil {
		return Lot{}, &Failure{Reason: ReasonDrift}
	}
	listed, err := domain.Parse(wire.Domain.Name)
	if err != nil || listed.ASCII != name.ASCII {
		return Lot{}, &Failure{Reason: ReasonDrift}
	}
	minimum, minOK := jsonKopecks(wire.Domain.MinPrice)
	maximum, maxOK := jsonKopecks(wire.Domain.MaxPrice)
	if !minOK || !maxOK || len(wire.Domain.Bids) == 0 {
		return Lot{}, &Failure{Reason: ReasonDrift}
	}
	ladder := make([]int64, 0, len(wire.Domain.Bids))
	for _, raw := range wire.Domain.Bids {
		step, ok := jsonKopecks(raw)
		if !ok {
			return Lot{}, &Failure{Reason: ReasonDrift}
		}
		ladder = append(ladder, step)
	}
	deletion := strings.Trim(strings.TrimSpace(string(wire.Domain.DeletionBlitz)), `"`)
	return Lot{
		State: LotListed, MinKopecks: minimum, MaxKopecks: maximum, Ladder: ladder,
		DeletionBlitz: deletion != "" && deletion != "0" && deletion != "false" && deletion != "null",
	}, nil
}

func jsonKopecks(raw json.RawMessage) (int64, bool) {
	text := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	return domain.Kopecks(text)
}
