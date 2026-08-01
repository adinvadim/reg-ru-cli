package support

import (
	"context"
	"strings"

	"github.com/adinvadim/reg-ru-cli/internal/cli"
)

// Reason is a stable, non-secret explanation for a private support capability
// that has not been captured safely enough to execute.
type Reason string

const (
	ReasonInventoryContractUncaptured  Reason = "authenticated_inventory_contract_uncaptured"
	ReasonDetailContractUncaptured     Reason = "authenticated_detail_contract_uncaptured"
	ReasonCreateContractUnverified     Reason = "authenticated_create_contract_unverified"
	ReasonReplyContractUncaptured      Reason = "authenticated_reply_contract_uncaptured"
	ReasonAttachmentContractUncaptured Reason = "attachment_contract_uncaptured"
	ReasonCloseContractUncaptured      Reason = "close_contract_uncaptured"
	ReasonReopenContractUncaptured     Reason = "reopen_contract_uncaptured"
)

type unavailableCapability struct {
	reason  Reason
	message string
}

var unavailableCapabilities = map[string]unavailableCapability{
	"support.ticket.list": {
		reason:  ReasonInventoryContractUncaptured,
		message: "the authenticated ticket-inventory transport was not captured without reading private ticket data; no provider request was made",
	},
	"support.ticket.get": {
		reason:  ReasonDetailContractUncaptured,
		message: "the authenticated ticket-detail, conversation, unread-state, pagination, and download contract was not captured without reading private ticket data; no provider request was made",
	},
	"support.ticket.create": {
		reason:  ReasonCreateContractUnverified,
		message: "the public support form is observable, but its authenticated mutation response and outcome contract are unverified; no ticket was submitted",
	},
	"support.ticket.reply": {
		reason:  ReasonReplyContractUncaptured,
		message: "the authenticated reply mutation and an independent reconciliation read were not captured; no reply was submitted",
	},
	"support.ticket.attach": {
		reason:  ReasonAttachmentContractUncaptured,
		message: "attachment limits, upload-handle lifetime, ticket binding, and download authorization were not captured; no file was read or uploaded",
	},
	"support.ticket.close": {
		reason:  ReasonCloseContractUncaptured,
		message: "the provider close mutation and authoritative status mapping were not captured; the ticket was not changed",
	},
	"support.ticket.reopen": {
		reason:  ReasonReopenContractUncaptured,
		message: "the provider reopen mutation and authoritative status mapping were not captured; the ticket was not changed",
	},
}

type Executor struct {
	fallback cli.Executor
}

func NewExecutor(fallback cli.Executor) *Executor {
	if fallback == nil {
		fallback = cli.UnavailableExecutor{}
	}
	return &Executor{fallback: fallback}
}

func (e *Executor) Execute(ctx context.Context, operation cli.Operation) (cli.Result, error) {
	if !strings.HasPrefix(operation.Action, "support.ticket.") {
		return e.fallback.Execute(ctx, operation)
	}
	unavailable, exists := unavailableCapabilities[operation.Action]
	if !exists {
		return e.fallback.Execute(ctx, operation)
	}
	return cli.Result{}, cli.CapabilityUnavailableWithReason(
		operation.Capability,
		string(unavailable.reason),
		unavailable.message,
	)
}
