# Billing and invoice commands

`regru billing` keeps REG.RU account billing and CloudVPS finance as separate
provider domains. It never treats a CloudVPS refill as an invoice, adds cash
and bonus balances, or correlates records by amount and date.

## Commands

```text
regru billing balance [--source all|regapi|cloudvps] [--currency RUR|USD|EUR|UAH]
regru billing history [--source cloudvps]
regru billing history --source regapi --start-date YYYY-MM-DD --end-date YYYY-MM-DD
regru billing invoice list [--limit 1..1024] [--offset N]
regru billing invoice show <id>
regru billing invoice status <id>
regru billing invoice delete <id> [id...]
regru billing invoice payment-method set <id> [id...] --type prepay|yamoney|bank --currency RUR|USD
regru billing invoice payment-method list <id>
regru billing invoice payment-link <id>
regru billing invoice create
```

`balance` requests both configured sources by default. Its structured result
contains source-discriminated balance entries plus a `sources` status array.
If one source succeeds, failure of the other is a partial success with
`complete: false` and a warning; values from one source never substitute for
the missing source. Use `--source` when an incomplete result must fail rather
than degrade.

CloudVPS `history` is refill history from `/v1/billing_history`, not resource
usage, invoices, or a complete account ledger. Provider order and local date
text are preserved. REG.API period history is explicitly partner-only and
requires date bounds; an ordinary account receives a structured capability
error rather than an empty result.

`invoice list` exposes the documented unpaid-invoice page. `invoice show`
returns full documented fields only when the invoice occurs in that unpaid
page. Otherwise it uses `bill/nop` and marks the result
`detailAvailable: false`; status-only data is never presented as full invoice
detail.

## Money and provider values

All monetary values are decimal strings. CloudVPS aggregate JSON numbers are
decoded without a `float64` conversion; CloudVPS currency is labelled `RUB`
with `currencySource: cloudvps_documentation` because the wire responses do
not contain a currency field. REG.API retains its provider enum `RUR`.

Invoice payment types and statuses are returned exactly as REG.API reports
them. List responses may contain `bank`, `pbank`, `prepay`, or `yacard`, while
the documented `bill/change_pay_type` mutation accepts only `prepay`,
`yamoney`, or `bank`. The CLI does not translate between these vocabularies.

## Mutation safety

Invoice deletion and payment-type changes require TTY confirmation or
`--force`, and support `--dry-run`. Selecting `prepay` may immediately pay an
invoice from the REG.RU account balance. REG.API publishes no idempotency
contract, so a mutation is sent at most once; an ambiguous post-dispatch
failure returns `outcome_unknown` and must be reconciled before retrying.

Bulk mutations inspect every returned item. If any item is rejected, the
command fails overall and includes the normalized per-item outcomes in the
structured error. Provider error text is not used for control flow or emitted
as stable output.

## Private portal enrichment and checkout

REG.API publishes no generic invoice-create method, ordinary-client paid
history, bill-specific available-method list, or payment URL. The current
cabinet contains private session-bound billing routes. An authorized redacted
capture now establishes the `userBills` request and response shape, empty and
non-empty pagination, normalized portal `id` equivalence to REG.API `bill_id`,
matching payment status, and stable opaque locators across a managed-browser
restart. A second authorized capture established two unpaid bill classes and
the bill-scoped `choose` handoff. The retained fixture contains no
account-specific field values, amounts, identifiers, or locators.

`invoice list` and detailed `invoice show` remain REG.API operations. When an
active portal session is available, the CLI may add a small
`portalEnrichment` record only after the browser principal, current frontend
semantics, identifier, amount, and payment-state invariants all agree. Missing
or incompatible portal data produces a warning and never replaces or disables
the public REG.API result.

`payment-link <id>` is intentionally a browser handoff rather than a URL. It
requires confirmation (or `--force`), finds exactly one payable portal row,
and opens the private `choose` route in the dedicated visible browser while
keeping `bill_sid` inside the browser process. Its result reports
`browser_opened`, `shareable: false`, and unknown expiry; it never prints or
returns the route. The CLI does not choose a method, enter payment data, or
submit payment.

The authorized chooser capture did not expose a stable bill-scoped method
list. Accordingly:

- `invoice create` explains that creation belongs to a service-specific order
  or renewal workflow;
- `payment-method list` returns `capability_unavailable` rather than
  substituting global saved bindings or a help-page catalog.

This gate is independent of public REG.API and CloudVPS reads: private portal
drift cannot silently disable or replace the documented provider contracts.
