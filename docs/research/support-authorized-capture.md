# Authorized REG.RU support capture

Capture date: 2026-08-01

## Boundary

The capture used the authorized `work` browser profile and retained only DOM,
route, field-name, state-class, and outcome shapes. Ticket locators, hidden
field values, cookies, CSRF values, message history, and response bodies were
not retained. Every provider mutation received separate human approval and was
dispatched at most once.

## Established contract

- Inventory is rendered at `/support/tickets/`. Each row has one numeric display
  ID, an opaque detail locator, an open/closed state class, and a preview.
- Detail is resolved from authenticated inventory by numeric display ID. It
  exposes a title, open/closed state, customer/agent messages, optional sender
  and date labels, and a separate customer-closed event.
- Generic create uses the no-service composer, `message`, and the private
  `send_universal_support_message` flow. Exact inventory reconciliation proves
  success without exposing the returned opaque token.
- Open detail exposes one POST-backed reply form with `message` plus opaque
  hidden fields. Exact read-after-write reconciliation proves the reply.
- Close is a two-step UI transition: the close button opens a confirmation and
  the visible primary `OK` action dispatches once. Closed state plus the
  customer-closed event proves the transition.

## Captured limitations

- Upload creates a temporary composer handle. No separate operation was found
  that binds an uploaded file to an existing ticket without also submitting a
  create/reply composer, so `ticket attach` remains unavailable.
- Closed detail exposes no reopen control or transition, so `ticket reopen`
  remains unavailable.
- Historical messages may omit sender or date elements; body and message kind
  remain required.

## Failure model

The adapter distinguishes build, route, principal, operation, and response
drift. Reads may be retried after a browser transition. Mutations get one
dispatch attempt; a result that cannot be proven by an exact independent read
is `outcome_unknown` and is never retried automatically.
