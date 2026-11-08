package registry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
)

const rdapMediaType = "application/rdap+json"

// DefaultRDAPBase maps each RDAP-backed zone to its fixed Verisign base URL.
var DefaultRDAPBase = map[string]string{
	"com": "https://rdap.verisign.com/com/v1/",
	"net": "https://rdap.verisign.com/net/v1/",
}

type rdapOutcome struct {
	state       State
	reason      string
	record      *Record
	rateLimited bool
}

// ParseRDAP classifies one RDAP answer. Only 404 with the RDAP media type is a
// miss (the body may be empty); a matching domain object is taken regardless
// of status; everything else is unknown.
func ParseRDAP(query string, status int, contentType string, body []byte) (State, *Record, string) {
	outcome := parseRDAP(query, status, contentType, body)
	return outcome.state, outcome.record, outcome.reason
}

func parseRDAP(query string, status int, contentType string, body []byte) rdapOutcome {
	mediaType, _, _ := mime.ParseMediaType(contentType)
	switch {
	case status == http.StatusNotFound && mediaType == rdapMediaType:
		return rdapOutcome{state: StateLikelyFree}
	case status == http.StatusTooManyRequests:
		return rdapOutcome{state: StateUnknown, reason: ReasonRateLimited, rateLimited: true}
	case status != http.StatusOK || mediaType != rdapMediaType && mediaType != "application/json":
		return rdapOutcome{state: StateUnknown, reason: ReasonUnrecognised}
	}
	var object struct {
		ObjectClassName string   `json:"objectClassName"`
		LDHName         string   `json:"ldhName"`
		Status          []string `json:"status"`
	}
	if err := json.Unmarshal(body, &object); err != nil || object.ObjectClassName != "domain" {
		return rdapOutcome{state: StateUnknown, reason: ReasonUnrecognised}
	}
	if !strings.EqualFold(object.LDHName, query) {
		return rdapOutcome{state: StateUnknown, reason: ReasonMismatch}
	}
	record := &Record{Domain: strings.ToLower(object.LDHName), Statuses: object.Status}
	pending, restorable := false, false
	for _, value := range object.Status {
		switch strings.ToLower(strings.ReplaceAll(value, " ", "")) {
		case "pendingdelete":
			pending = true
		case "redemptionperiod", "pendingrestore":
			restorable = true
		}
	}
	record.PendingDelete = pending && !restorable
	return rdapOutcome{state: StateTaken, record: record}
}

func queryRDAP(ctx context.Context, client *http.Client, base, query string) rdapOutcome {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"domain/"+query, nil)
	if err != nil {
		return rdapOutcome{state: StateUnknown, reason: ReasonUnrecognised}
	}
	request.Header.Set("Accept", rdapMediaType)
	response, err := client.Do(request)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return rdapOutcome{state: StateUnknown, reason: ReasonCancelled}
		}
		var netErr net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &netErr) && netErr.Timeout() {
			return rdapOutcome{state: StateUnknown, reason: ReasonTimeout}
		}
		return rdapOutcome{state: StateUnknown, reason: ReasonConnection}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxReplyBytes+1))
	if err != nil || len(body) > maxReplyBytes {
		return rdapOutcome{state: StateUnknown, reason: ReasonUnrecognised}
	}
	return parseRDAP(query, response.StatusCode, response.Header.Get("Content-Type"), body)
}
