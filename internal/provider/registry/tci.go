package registry

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultTCIAddress is the documented TCI port-43 WHOIS service.
	DefaultTCIAddress = "whois.tcinet.ru:43"
	// TCIHeader starts every TCI WHOIS answer.
	TCIHeader = "% TCI Whois Service."
	// TCINoEntries is the exact miss line; with the header it means likely_free.
	TCINoEntries  = "No entries found for the selected source(s)."
	maxReplyBytes = 128 << 10
)

// Dialer opens one TCP connection per WHOIS query.
type Dialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

// tciOutcome is the classification of one TCI reply.
type tciOutcome struct {
	state      State
	reason     string
	record     *Record
	unexpected bool
}

// ParseTCI classifies a TCI WHOIS reply for the ASCII query. Only the header
// plus the exact miss line is a miss; any matching domain record is taken;
// anything else is unknown and marked unexpected so the run stops querying TCI.
func ParseTCI(query string, reply []byte) (State, *Record, string, bool) {
	outcome := parseTCI(query, reply)
	return outcome.state, outcome.record, outcome.reason, outcome.unexpected
}

func parseTCI(query string, reply []byte) tciOutcome {
	header := false
	noEntries := false
	record := Record{}
	domains := 0
	scanner := bufio.NewScanner(bytes.NewReader(reply))
	scanner.Buffer(make([]byte, 0, 4096), maxReplyBytes)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), " \t\r")
		if strings.HasPrefix(line, TCIHeader) {
			header = true
			continue
		}
		if strings.TrimSpace(line) == TCINoEntries {
			noEntries = true
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if !found || strings.HasPrefix(line, "%") {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(strings.ToLower(key)) {
		case "domain":
			domains++
			if !strings.EqualFold(value, query) {
				return tciOutcome{state: StateUnknown, reason: ReasonMismatch, unexpected: true}
			}
			record.Domain = strings.ToLower(value)
		case "state":
			record.State = value
		case "registrar":
			record.Registrar = value
		case "paid-till":
			record.PaidTill = value
		case "free-date":
			record.FreeDate = value
		}
	}
	if scanner.Err() != nil || !header {
		return tciOutcome{state: StateUnknown, reason: ReasonUnrecognised, unexpected: true}
	}
	if domains > 0 {
		if noEntries {
			return tciOutcome{state: StateUnknown, reason: ReasonUnrecognised, unexpected: true}
		}
		record.PendingDelete = strings.Contains(strings.ToLower(record.State), "pendingdelete")
		return tciOutcome{state: StateTaken, record: &record}
	}
	if noEntries {
		return tciOutcome{state: StateLikelyFree}
	}
	return tciOutcome{state: StateUnknown, reason: ReasonUnrecognised, unexpected: true}
}

// tciGate enforces the TCI budget for one lookup run: at most N open
// connections and real spacing between consecutive connection starts.
type tciGate struct {
	slots   chan struct{}
	starts  chan struct{}
	next    time.Time
	spacing time.Duration
	mu      sync.Mutex
	stopped bool
}

func newTCIGate(concurrency int, spacing time.Duration) *tciGate {
	if concurrency < 1 {
		concurrency = 1
	}
	return &tciGate{slots: make(chan struct{}, concurrency), starts: make(chan struct{}, 1), spacing: spacing}
}

func (g *tciGate) acquire(ctx context.Context) error {
	select {
	case g.slots <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case g.starts <- struct{}{}:
	case <-ctx.Done():
		<-g.slots
		return ctx.Err()
	}
	defer func() { <-g.starts }()
	if wait := time.Until(g.next); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			<-g.slots
			return ctx.Err()
		}
	}
	g.next = time.Now().Add(g.spacing)
	return nil
}

func (g *tciGate) release() { <-g.slots }

func (g *tciGate) stop() {
	g.mu.Lock()
	g.stopped = true
	g.mu.Unlock()
}

func (g *tciGate) isStopped() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stopped
}

func queryTCI(ctx context.Context, dialer Dialer, address, query string, timeout time.Duration) tciOutcome {
	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := dialer.DialContext(queryCtx, "tcp", address)
	if err != nil {
		return transportOutcome(ctx, queryCtx, err)
	}
	defer conn.Close()
	deadline, _ := queryCtx.Deadline()
	_ = conn.SetDeadline(deadline)
	stopClose := context.AfterFunc(queryCtx, func() { _ = conn.Close() })
	defer stopClose()
	if _, err := io.WriteString(conn, query+"\r\n"); err != nil {
		return transportOutcome(ctx, queryCtx, err)
	}
	reply, err := io.ReadAll(io.LimitReader(conn, maxReplyBytes+1))
	if err != nil {
		return transportOutcome(ctx, queryCtx, err)
	}
	if len(reply) > maxReplyBytes || len(bytes.TrimSpace(reply)) == 0 {
		return tciOutcome{state: StateUnknown, reason: ReasonUnrecognised, unexpected: true}
	}
	return parseTCI(query, reply)
}

func transportOutcome(parent, query context.Context, err error) tciOutcome {
	if parent.Err() != nil {
		return tciOutcome{state: StateUnknown, reason: ReasonCancelled}
	}
	var netErr net.Error
	if query.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.As(err, &netErr) && netErr.Timeout() {
		return tciOutcome{state: StateUnknown, reason: ReasonTimeout, unexpected: true}
	}
	return tciOutcome{state: StateUnknown, reason: ReasonConnection, unexpected: true}
}
