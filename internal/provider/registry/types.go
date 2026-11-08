// Package registry answers "does the authoritative registry hold a record for
// this name" through TCI WHOIS (.ru/.рф/.su) and Verisign RDAP (.com/.net).
// A registry miss is only ever likely_free; failures are unknown, never free.
package registry

import (
	"time"

	"github.com/adinvadim/reg-ru-cli/internal/domain"
)

// State is the availability verdict for one name.
type State string

const (
	StateTaken      State = "taken"
	StateLikelyFree State = "likely_free"
	StateFree       State = "free"
	StateUnknown    State = "unknown"
)

const (
	SourceTCI       = "whois.tcinet.ru:43"
	SourceVerisign  = "rdap.verisign.com"
	SourceRegistrar = "REG.API domain/check"
)

// Reasons for an unknown verdict.
const (
	ReasonTimeout         = "timeout"
	ReasonRateLimited     = "rate_limited"
	ReasonMismatch        = "mismatch"
	ReasonUnrecognised    = "unrecognised_response"
	ReasonConnection      = "connection_failed"
	ReasonTCIStopped      = "tci_stopped_after_unexpected_reply"
	ReasonRDAPStopped     = "rdap_stopped_after_rate_limit"
	ReasonCancelled       = "cancelled"
	ReasonUnsupportedZone = "no_registry_source"
)

// Record is the non-personal evidence kept from a registry answer.
type Record struct {
	Domain        string   `json:"domain,omitempty"`
	State         string   `json:"state,omitempty"`
	Statuses      []string `json:"statuses,omitempty"`
	Registrar     string   `json:"registrar,omitempty"`
	PaidTill      string   `json:"paidTill,omitempty"`
	FreeDate      string   `json:"freeDate,omitempty"`
	PendingDelete bool     `json:"pendingDelete,omitempty"`
}

// Verdict is the availability answer for one name with its evidence.
type Verdict struct {
	Name      domain.Name `json:"name"`
	State     State       `json:"state"`
	Source    string      `json:"source"`
	Reason    string      `json:"reason,omitempty"`
	CheckedAt time.Time   `json:"checkedAt"`
	Record    *Record     `json:"record,omitempty"`
	Registrar *Registrar  `json:"registrar,omitempty"`
}

// Registrar carries the optional REG.API domain/check confirmation.
type Registrar struct {
	Checked    bool   `json:"checked"`
	Result     string `json:"result,omitempty"`
	ErrorCode  string `json:"errorCode,omitempty"`
	Premium    bool   `json:"premium,omitempty"`
	Price      string `json:"price,omitempty"`
	RenewPrice string `json:"renewPrice,omitempty"`
	Currency   string `json:"currency,omitempty"`
	Reason     string `json:"reason,omitempty"`
}
