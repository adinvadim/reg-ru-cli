package registry

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adinvadim/reg-ru-cli/internal/domain"
)

const tciTaken = `% TCI Whois Service. Terms of use:
% https://tcinet.ru/documents/whois_ru_rf.pdf (in Russian)

domain:        DOMKRAY.RU
nserver:       ns1.reg.ru.
state:         REGISTERED, DELEGATED, VERIFIED
person:        Private Person
registrar:     REGRU-RU
created:       2026-11-07T18:36:52Z
paid-till:     2027-11-07T18:36:52Z
free-date:     2027-12-08
source:        TCI

Last updated on 2026-11-08T06:28:01Z
`

const tciMiss = "% TCI Whois Service. Terms of use:\r\n% https://tcinet.ru/documents/whois_ru_rf.pdf (in Russian)\r\n\r\nNo entries found for the selected source(s).\r\n\r\nLast updated on 2026-11-08T06:28:01Z\r\n"

func TestParseTCI(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		query      string
		reply      string
		state      State
		reason     string
		unexpected bool
	}{
		{"miss needs header and exact line", "free.ru", tciMiss, StateLikelyFree, "", false},
		{"matching record is taken", "domkray.ru", tciTaken, StateTaken, "", false},
		{"miss line without header is unknown", "free.ru", "No entries found for the selected source(s).\n", StateUnknown, ReasonUnrecognised, true},
		{"header without miss line is unknown", "free.ru", "% TCI Whois Service. Terms of use:\n\nLast updated on 2026\n", StateUnknown, ReasonUnrecognised, true},
		{"near-miss wording is unknown", "free.ru", "% TCI Whois Service.\nNo entries found.\n", StateUnknown, ReasonUnrecognised, true},
		{"mismatched record is unknown", "other.ru", tciTaken, StateUnknown, ReasonMismatch, true},
		{"garbage is unknown", "free.ru", "<html>blocked</html>", StateUnknown, ReasonUnrecognised, true},
		{"empty is unknown", "free.ru", "", StateUnknown, ReasonUnrecognised, true},
		{"rate-limit text is unknown", "free.ru", "% TCI Whois Service.\n%ERROR:201: access denied\n", StateUnknown, ReasonUnrecognised, true},
	}
	for _, testCase := range cases {
		state, record, reason, unexpected := ParseTCI(testCase.query, []byte(testCase.reply))
		if state != testCase.state || reason != testCase.reason || unexpected != testCase.unexpected {
			t.Errorf("%s: ParseTCI = %s/%q/%v, want %s/%q/%v", testCase.name, state, reason, unexpected, testCase.state, testCase.reason, testCase.unexpected)
		}
		if state == StateTaken && (record == nil || record.State != "REGISTERED, DELEGATED, VERIFIED" || record.Registrar != "REGRU-RU" ||
			record.PaidTill != "2027-11-07T18:36:52Z" || record.FreeDate != "2027-12-08" || record.PendingDelete) {
			t.Errorf("%s: record = %#v", testCase.name, record)
		}
	}

	pending := strings.Replace(tciTaken, "REGISTERED, DELEGATED, VERIFIED", "pendingDelete", 1)
	state, record, _, _ := ParseTCI("domkray.ru", []byte(pending))
	if state != StateTaken || record == nil || !record.PendingDelete {
		t.Fatalf("pendingDelete = %s %#v; a pending-delete record is still taken", state, record)
	}
	if strings.Contains(record.Domain+record.State+record.Registrar, "Private Person") {
		t.Fatal("contact data entered the record")
	}
}

func TestParseRDAP(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		status      int
		contentType string
		body        string
		state       State
		reason      string
		pending     bool
	}{
		{"404 empty rdap body is a miss", 404, "application/rdap+json", "", StateLikelyFree, "", false},
		{"404 with charset parameter is a miss", 404, "application/rdap+json; charset=utf-8", "", StateLikelyFree, "", false},
		{"404 html is unknown", 404, "text/html", "<html>", StateUnknown, ReasonUnrecognised, false},
		{"match is taken", 200, "application/rdap+json", `{"objectClassName":"domain","ldhName":"GOOGLE.COM","status":["client transfer prohibited"]}`, StateTaken, "", false},
		{"pending delete", 200, "application/rdap+json", `{"objectClassName":"domain","ldhName":"google.com","status":["pending delete"]}`, StateTaken, "", true},
		{"redemption is not pending delete", 200, "application/rdap+json", `{"objectClassName":"domain","ldhName":"google.com","status":["pendingDelete","redemptionPeriod"]}`, StateTaken, "", false},
		{"mismatch is unknown", 200, "application/rdap+json", `{"objectClassName":"domain","ldhName":"other.com"}`, StateUnknown, ReasonMismatch, false},
		{"rate limit is unknown", 429, "application/rdap+json", "", StateUnknown, ReasonRateLimited, false},
		{"server error is unknown", 503, "application/rdap+json", "", StateUnknown, ReasonUnrecognised, false},
	}
	for _, testCase := range cases {
		state, record, reason := ParseRDAP("google.com", testCase.status, testCase.contentType, []byte(testCase.body))
		if state != testCase.state || reason != testCase.reason {
			t.Errorf("%s: ParseRDAP = %s/%q, want %s/%q", testCase.name, state, reason, testCase.state, testCase.reason)
		}
		if state == StateTaken && record.PendingDelete != testCase.pending {
			t.Errorf("%s: pendingDelete = %v", testCase.name, record.PendingDelete)
		}
	}
}

type whoisServer struct {
	listener  net.Listener
	active    atomic.Int32
	maxActive atomic.Int32
	queries   atomic.Int32
	reply     func(string) string
	delay     time.Duration
}

func startWhois(t *testing.T, delay time.Duration, reply func(string) string) *whoisServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &whoisServer{listener: listener, reply: reply, delay: delay}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go server.serve(conn)
		}
	}()
	t.Cleanup(func() { _ = listener.Close() })
	return server
}

func (s *whoisServer) serve(conn net.Conn) {
	defer conn.Close()
	current := s.active.Add(1)
	defer s.active.Add(-1)
	for {
		previous := s.maxActive.Load()
		if current <= previous || s.maxActive.CompareAndSwap(previous, current) {
			break
		}
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return
	}
	s.queries.Add(1)
	time.Sleep(s.delay)
	_, _ = conn.Write([]byte(s.reply(strings.TrimSpace(line))))
}

func mustNames(t *testing.T, values ...string) []domain.Name {
	t.Helper()
	names := make([]domain.Name, 0, len(values))
	for _, value := range values {
		name, err := domain.Parse(value)
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	return names
}

type recordingDialer struct {
	mu     sync.Mutex
	starts []time.Time
	dialer net.Dialer
}

func (d *recordingDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	d.mu.Lock()
	d.starts = append(d.starts, time.Now())
	d.mu.Unlock()
	return d.dialer.DialContext(ctx, network, address)
}

func TestCheckerRespectsTCIConcurrencyAndSpacing(t *testing.T) {
	t.Parallel()

	server := startWhois(t, 40*time.Millisecond, func(query string) string {
		if query == "domkray.ru" {
			return tciTaken
		}
		return tciMiss
	})
	spacing := 30 * time.Millisecond
	dialer := &recordingDialer{}
	checker := NewChecker(Options{TCIAddress: server.listener.Addr().String(), Dialer: dialer, TCISpacing: spacing, TCIConcurrency: 5})
	names := mustNames(t, "domkray.ru", "aaa1.ru", "aaa2.ru", "домкрай.рф", "aaa3.su", "aaa4.ru")
	verdicts := checker.Check(context.Background(), names)

	if verdicts[0].State != StateTaken || verdicts[0].Source != SourceTCI {
		t.Fatalf("verdict[0] = %#v", verdicts[0])
	}
	for _, verdict := range verdicts[1:] {
		if verdict.State != StateLikelyFree {
			t.Fatalf("verdict = %#v", verdict)
		}
	}
	if got := server.maxActive.Load(); got > 2 {
		t.Fatalf("max concurrent TCI connections = %d, want <= 2", got)
	}
	dialer.mu.Lock()
	defer dialer.mu.Unlock()
	if len(dialer.starts) != len(names) {
		t.Fatalf("dials = %d, want %d", len(dialer.starts), len(names))
	}
	for index := 1; index < len(dialer.starts); index++ {
		if gap := dialer.starts[index].Sub(dialer.starts[index-1]); gap < spacing {
			t.Fatalf("connection %d started %s after the previous one, want >= %s", index, gap, spacing)
		}
	}
}

func TestCheckerStopsTCIAfterUnexpectedReply(t *testing.T) {
	t.Parallel()

	server := startWhois(t, 0, func(string) string { return "Too many requests\n" })
	checker := NewChecker(Options{TCIAddress: server.listener.Addr().String(), TCISpacing: 20 * time.Millisecond})
	verdicts := checker.Check(context.Background(), mustNames(t, "one1.ru", "two2.ru", "three3.ru", "four4.ru", "five5.ru"))

	stopped := 0
	for _, verdict := range verdicts {
		if verdict.State != StateUnknown {
			t.Fatalf("verdict = %#v; garbage must never be free", verdict)
		}
		if verdict.Reason == ReasonTCIStopped {
			stopped++
		}
	}
	if queries := server.queries.Load(); queries > 2 || stopped < 3 {
		t.Fatalf("queries = %d, stopped = %d; TCI work must stop after the first unexpected reply", queries, stopped)
	}
}

func TestCheckerTimesOutTCI(t *testing.T) {
	t.Parallel()

	server := startWhois(t, 300*time.Millisecond, func(string) string { return tciMiss })
	checker := NewChecker(Options{TCIAddress: server.listener.Addr().String(), Timeout: 50 * time.Millisecond})
	verdicts := checker.Check(context.Background(), mustNames(t, "slow1.ru"))
	if verdicts[0].State != StateUnknown || verdicts[0].Reason != ReasonTimeout {
		t.Fatalf("verdict = %#v", verdicts[0])
	}
}

func TestCheckerRDAP(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Header.Get("Accept") != "application/rdap+json" {
			t.Errorf("Accept = %q", request.Header.Get("Accept"))
		}
		switch request.URL.Path {
		case "/com/v1/domain/google.com":
			writer.Header().Set("Content-Type", "application/rdap+json")
			_, _ = writer.Write([]byte(`{"objectClassName":"domain","ldhName":"GOOGLE.COM","status":["active"]}`))
		case "/com/v1/domain/free-name.com", "/net/v1/domain/free-name.net":
			writer.Header().Set("Content-Type", "application/rdap+json")
			writer.WriteHeader(http.StatusNotFound)
		case "/com/v1/domain/moved.com":
			http.Redirect(writer, request, "/elsewhere", http.StatusFound)
		default:
			writer.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(server.Close)
	checker := NewChecker(Options{RDAPBase: map[string]string{"com": server.URL + "/com/v1/", "net": server.URL + "/net/v1/"}})
	verdicts := checker.Check(context.Background(), mustNames(t, "google.com", "free-name.com", "free-name.net", "moved.com"))
	want := []State{StateTaken, StateLikelyFree, StateLikelyFree, StateUnknown}
	for index, verdict := range verdicts {
		if verdict.State != want[index] || verdict.Source != SourceVerisign {
			t.Fatalf("verdict[%d] = %#v, want %s", index, verdict, want[index])
		}
	}
	if requests.Load() != 4 {
		t.Fatalf("requests = %d; redirects must not be followed", requests.Load())
	}
}

func TestCheckerStopsRDAPAfterRateLimit(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		time.Sleep(20 * time.Millisecond)
		writer.Header().Set("Retry-After", "60")
		writer.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(server.Close)
	checker := NewChecker(Options{RDAPBase: map[string]string{"com": server.URL + "/"}})
	names := mustNames(t, "a1a.com", "a2a.com", "a3a.com", "a4a.com", "a5a.com", "a6a.com", "a7a.com", "a8a.com", "a9a.com", "a10a.com")
	verdicts := checker.Check(context.Background(), names)
	for _, verdict := range verdicts {
		if verdict.State != StateUnknown {
			t.Fatalf("verdict = %#v", verdict)
		}
	}
	if requests.Load() >= int32(len(names)) {
		t.Fatalf("requests = %d; RDAP must stop after a rate limit", requests.Load())
	}
}
