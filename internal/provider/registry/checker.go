package registry

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/adinvadim/reg-ru-cli/internal/domain"
)

const (
	// DefaultTimeout bounds one registry request.
	DefaultTimeout = 5 * time.Second
	// DefaultTCISpacing keeps TCI near 100 queries/minute, under its 120/minute/IP limit.
	DefaultTCISpacing     = 600 * time.Millisecond
	defaultTCIConcurrency = 2
	defaultRDAPWorkers    = 4
)

// Options configures a Checker. Zero values select the production endpoints
// and budgets; tests point them at local servers.
type Options struct {
	TCIAddress     string
	RDAPBase       map[string]string
	Dialer         Dialer
	HTTPClient     *http.Client
	Timeout        time.Duration
	TCISpacing     time.Duration
	TCIConcurrency int
	Now            func() time.Time
}

// Checker performs registry availability lookups within the documented budgets.
type Checker struct {
	options Options
}

// NewChecker returns a Checker with production defaults for unset options.
func NewChecker(options Options) *Checker {
	if options.TCIAddress == "" {
		options.TCIAddress = DefaultTCIAddress
	}
	if options.RDAPBase == nil {
		options.RDAPBase = DefaultRDAPBase
	}
	if options.Timeout <= 0 {
		options.Timeout = DefaultTimeout
	}
	if options.TCISpacing < 0 {
		options.TCISpacing = 0
	}
	if options.TCIConcurrency <= 0 || options.TCIConcurrency > defaultTCIConcurrency {
		options.TCIConcurrency = defaultTCIConcurrency
	}
	if options.Dialer == nil {
		options.Dialer = &net.Dialer{}
	}
	if options.HTTPClient == nil {
		options.HTTPClient = &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Checker{options: options}
}

// Check returns one verdict per name, in input order. Names are looked up
// concurrently per registry; TCI work stops for the run after any unexpected
// reply, and RDAP work stops after a rate limit.
func (c *Checker) Check(ctx context.Context, names []domain.Name) []Verdict {
	verdicts := make([]Verdict, len(names))
	tciIndexes := []int{}
	rdapIndexes := []int{}
	for index, name := range names {
		switch name.Zone {
		case domain.ZoneRU, domain.ZoneRF, domain.ZoneSU:
			tciIndexes = append(tciIndexes, index)
		case domain.ZoneCOM, domain.ZoneNET:
			rdapIndexes = append(rdapIndexes, index)
		default:
			verdicts[index] = Verdict{Name: name, State: StateUnknown, Reason: ReasonUnsupportedZone, CheckedAt: c.options.Now().UTC()}
		}
	}
	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		c.checkTCI(ctx, names, tciIndexes, verdicts)
	}()
	go func() {
		defer group.Done()
		c.checkRDAP(ctx, names, rdapIndexes, verdicts)
	}()
	group.Wait()
	return verdicts
}

func (c *Checker) checkTCI(ctx context.Context, names []domain.Name, indexes []int, verdicts []Verdict) {
	gate := newTCIGate(c.options.TCIConcurrency, c.options.TCISpacing)
	work := make(chan int)
	var workers sync.WaitGroup
	for range c.options.TCIConcurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range work {
				verdicts[index] = c.tciVerdict(ctx, gate, names[index])
			}
		}()
	}
	for _, index := range indexes {
		work <- index
	}
	close(work)
	workers.Wait()
}

func (c *Checker) tciVerdict(ctx context.Context, gate *tciGate, name domain.Name) Verdict {
	verdict := Verdict{Name: name, Source: SourceTCI}
	if gate.isStopped() {
		verdict.State, verdict.Reason, verdict.CheckedAt = StateUnknown, ReasonTCIStopped, c.options.Now().UTC()
		return verdict
	}
	if err := gate.acquire(ctx); err != nil {
		verdict.State, verdict.Reason, verdict.CheckedAt = StateUnknown, ReasonCancelled, c.options.Now().UTC()
		return verdict
	}
	defer gate.release()
	if gate.isStopped() {
		verdict.State, verdict.Reason, verdict.CheckedAt = StateUnknown, ReasonTCIStopped, c.options.Now().UTC()
		return verdict
	}
	outcome := queryTCI(ctx, c.options.Dialer, c.options.TCIAddress, name.ASCII, c.options.Timeout)
	if outcome.unexpected {
		gate.stop()
	}
	verdict.State, verdict.Reason, verdict.Record = outcome.state, outcome.reason, outcome.record
	verdict.CheckedAt = c.options.Now().UTC()
	return verdict
}

func (c *Checker) checkRDAP(ctx context.Context, names []domain.Name, indexes []int, verdicts []Verdict) {
	var stopped sync.Once
	stop := make(chan struct{})
	work := make(chan int)
	var workers sync.WaitGroup
	for range defaultRDAPWorkers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range work {
				name := names[index]
				verdict := Verdict{Name: name, Source: SourceVerisign}
				select {
				case <-stop:
					verdict.State, verdict.Reason = StateUnknown, ReasonRDAPStopped
					verdict.CheckedAt = c.options.Now().UTC()
					verdicts[index] = verdict
					continue
				default:
				}
				base, exists := c.options.RDAPBase[string(name.Zone)]
				if !exists {
					verdict.State, verdict.Reason = StateUnknown, ReasonUnsupportedZone
					verdict.CheckedAt = c.options.Now().UTC()
					verdicts[index] = verdict
					continue
				}
				requestCtx, cancel := context.WithTimeout(ctx, c.options.Timeout)
				outcome := queryRDAP(requestCtx, c.options.HTTPClient, base, name.ASCII)
				cancel()
				if outcome.reason == ReasonTimeout && errors.Is(ctx.Err(), context.Canceled) {
					outcome.reason = ReasonCancelled
				}
				if outcome.rateLimited {
					stopped.Do(func() { close(stop) })
				}
				verdict.State, verdict.Reason, verdict.Record = outcome.state, outcome.reason, outcome.record
				verdict.CheckedAt = c.options.Now().UTC()
				verdicts[index] = verdict
			}
		}()
	}
	for _, index := range indexes {
		work <- index
	}
	close(work)
	workers.Wait()
}
