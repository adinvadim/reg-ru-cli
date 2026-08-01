package support

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/adinvadim/reg-ru-cli/internal/cli"
	"github.com/adinvadim/reg-ru-cli/internal/profile"
	"github.com/adinvadim/reg-ru-cli/internal/provider/portal/cdp"
	"github.com/adinvadim/reg-ru-cli/internal/provider/portal/session"
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
		message: "REG.RU exposes only a temporary composer upload and no captured standalone ticket-binding operation; no file was read or uploaded",
	},
	"support.ticket.close": {
		reason:  ReasonCloseContractUncaptured,
		message: "the provider close mutation and authoritative status mapping were not captured; the ticket was not changed",
	},
	"support.ticket.reopen": {
		reason:  ReasonReopenContractUncaptured,
		message: "the captured closed-ticket detail exposes no reopen transition; the ticket was not changed",
	},
}

type Executor struct {
	options  ExecutorOptions
	fallback cli.Executor
}

type ExecutorOptions struct {
	Profiles profile.Repository
	Portal   PortalControlPlane
}

func NewExecutor(fallback cli.Executor) *Executor {
	return NewExecutorWithOptions(ExecutorOptions{}, fallback)
}

func NewExecutorWithOptions(options ExecutorOptions, fallback cli.Executor) *Executor {
	if fallback == nil {
		fallback = cli.UnavailableExecutor{}
	}
	return &Executor{options: options, fallback: fallback}
}

func (e *Executor) Execute(ctx context.Context, operation cli.Operation) (cli.Result, error) {
	if !strings.HasPrefix(operation.Action, "support.ticket.") {
		return e.fallback.Execute(ctx, operation)
	}
	if operation.Action == "support.ticket.attach" || operation.Action == "support.ticket.reopen" {
		unavailable := unavailableCapabilities[operation.Action]
		return cli.Result{}, cli.CapabilityUnavailableWithReason(
			operation.Capability, string(unavailable.reason), unavailable.message,
		)
	}
	if e.options.Portal == nil || e.options.Profiles == nil {
		unavailable, exists := unavailableCapabilities[operation.Action]
		if !exists {
			return e.fallback.Execute(ctx, operation)
		}
		return cli.Result{}, cli.CapabilityUnavailableWithReason(
			operation.Capability, string(unavailable.reason), unavailable.message,
		)
	}
	if len(operation.Parameters["attachments"]) != 0 {
		unavailable := unavailableCapabilities["support.ticket.attach"]
		return cli.Result{}, cli.CapabilityUnavailableWithReason(
			"support.ticket.attachment", string(unavailable.reason), unavailable.message,
		)
	}
	account, err := e.account(operation)
	if err != nil {
		return cli.Result{}, err
	}
	var result cli.Result
	switch operation.Action {
	case "support.ticket.list":
		result, err = e.list(ctx, account, operation)
	case "support.ticket.get":
		result, err = e.get(ctx, account, operation)
	case "support.ticket.create", "support.ticket.reply":
		result, err = e.messageMutation(ctx, account, operation)
	case "support.ticket.close":
		result, err = e.close(ctx, account, operation)
	default:
		return e.fallback.Execute(ctx, operation)
	}
	if err != nil {
		return cli.Result{}, translatePortalError(operation, err)
	}
	return result, nil
}

func (e *Executor) account(operation cli.Operation) (profile.Account, error) {
	config, err := e.options.Profiles.Load()
	if err != nil {
		return profile.Account{}, cli.ConfigurationError("profile configuration is invalid")
	}
	account, ok := config.Accounts[operation.Account]
	if !ok || account.ID != operation.ProfileID {
		return profile.Account{}, cli.AccountNotFound(operation.Account)
	}
	return account, nil
}

func (e *Executor) list(ctx context.Context, account profile.Account, operation cli.Operation) (cli.Result, error) {
	limit, _ := strconv.Atoi(firstParameter(operation, "limit"))
	page, _ := strconv.Atoi(firstParameter(operation, "page"))
	result, err := e.options.Portal.List(ctx, account, ListRequest{
		Limit: limit, Page: page, Status: firstParameter(operation, "status"),
	})
	if err != nil {
		return cli.Result{}, err
	}
	lines := make([]string, 0, len(result.Tickets))
	plain := make([]string, 0, len(result.Tickets))
	for _, ticket := range result.Tickets {
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s", ticket.ID, ticket.Status, ticket.Preview))
		plain = append(plain, fmt.Sprintf("%s\t%s\t%s", ticket.ID, ticket.Status, ticket.Preview))
	}
	return cli.Result{
		Human: strings.Join(lines, "\n"), Plain: plain,
		Data:     map[string]any{"tickets": result.Tickets, "total": result.Total, "page": page, "limit": limit},
		Warnings: []cli.Warning{{Code: "experimental_private_portal", Message: "support data comes from a private, fail-closed REG.RU portal contract"}},
	}, nil
}

func (e *Executor) get(ctx context.Context, account profile.Account, operation cli.Operation) (cli.Result, error) {
	id := firstArgument(operation)
	if err := validateTicketID(id); err != nil {
		return cli.Result{}, cli.UsageError("support ticket ID must contain digits only")
	}
	ticket, err := e.options.Portal.Get(ctx, account, id)
	if err != nil {
		return cli.Result{}, err
	}
	lines := []string{fmt.Sprintf("Ticket %s: %s (%s)", ticket.ID, ticket.Title, ticket.Status)}
	plain := []string{fmt.Sprintf("%s\t%s\t%s", ticket.ID, ticket.Status, ticket.Title)}
	for _, message := range ticket.Messages {
		lines = append(lines, fmt.Sprintf("%s %s: %s", message.Created, message.Sender, message.Body))
		plain = append(plain, fmt.Sprintf("%s\t%s\t%s\t%s", message.Kind, message.Created, message.Sender, message.Body))
	}
	return cli.Result{Human: strings.Join(lines, "\n"), Plain: plain, Data: ticket,
		Warnings: []cli.Warning{{Code: "experimental_private_portal", Message: "support data comes from a private, fail-closed REG.RU portal contract"}}}, nil
}

func (e *Executor) messageMutation(ctx context.Context, account profile.Account, operation cli.Operation) (cli.Result, error) {
	if operation.Input == nil {
		return cli.Result{}, cli.UsageError("support message input is required")
	}
	id := ""
	if operation.Action == "support.ticket.reply" {
		id = firstArgument(operation)
		if err := validateTicketID(id); err != nil {
			return cli.Result{}, cli.UsageError("support ticket ID must contain digits only")
		}
	}
	body, err := operation.Input.Resolve(ctx)
	if err != nil {
		return cli.Result{}, err
	}
	action := "create"
	if id != "" {
		action = "reply"
	}
	if err := e.options.Portal.Mutate(ctx, account, MutationRequest{Action: action, ID: id, Message: string(body)}); err != nil {
		return cli.Result{}, err
	}
	return mutationResult(action, id), nil
}

func (e *Executor) close(ctx context.Context, account profile.Account, operation cli.Operation) (cli.Result, error) {
	id := firstArgument(operation)
	if err := validateTicketID(id); err != nil {
		return cli.Result{}, cli.UsageError("support ticket ID must contain digits only")
	}
	if err := e.options.Portal.Mutate(ctx, account, MutationRequest{Action: "close", ID: id}); err != nil {
		return cli.Result{}, err
	}
	return mutationResult("close", id), nil
}

func mutationResult(action, id string) cli.Result {
	human := "Support ticket " + action + " committed"
	if id != "" {
		human = "Support ticket " + id + " " + action + " committed"
	}
	return cli.Result{Human: human, Plain: []string{"committed"}, Data: map[string]any{
		"outcome": "committed", "action": action, "id": id,
	}, Warnings: []cli.Warning{{Code: "experimental_private_portal", Message: "support mutation used a private, fail-closed REG.RU portal contract"}}}
}

func firstArgument(operation cli.Operation) string {
	if len(operation.Arguments) == 0 {
		return ""
	}
	return operation.Arguments[0]
}

func firstParameter(operation cli.Operation, name string) string {
	if len(operation.Parameters[name]) == 0 {
		return ""
	}
	return operation.Parameters[name][0]
}

func translatePortalError(operation cli.Operation, err error) error {
	var cliErr *cli.CLIError
	if errors.As(err, &cliErr) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	switch {
	case session.IsCode(err, session.CodeAccountMismatch):
		return cli.AccountMismatch(operation.Account, "")
	case session.IsCode(err, session.CodeProfileBusy):
		return cli.PortalProfileBusy()
	case session.IsCode(err, session.CodeSessionLost), session.IsCode(err, session.CodeNotEstablished), isPortalKind(err, PortalUnauthorized):
		return cli.AuthenticationExpired()
	case session.IsCode(err, session.CodeContractDrift), isPortalKind(err, PortalContract):
		return cli.PrivateContractDrift(operation.Capability)
	case session.IsCode(err, session.CodeBrowser):
		if errors.Is(err, cdp.ErrBrowserNotFound) {
			return cli.MissingBrowser()
		}
		return cli.BrowserSessionInterrupted()
	case isPortalKind(err, PortalAmbiguous):
		return cli.OutcomeUnknown(operation.Capability)
	case isPortalKind(err, PortalNotFound):
		return cli.ProviderError("REG.RU support", "ticket_not_found", 0, false, "")
	case isPortalKind(err, PortalTransport):
		return cli.NetworkError("REG.RU support", false)
	default:
		return cli.ConfigurationError("support portal integration failed")
	}
}
