# Experimental support adapter

The support command tree uses an experimental, browser-session-bound adapter.
REG.RU exposes a human support workflow and a private web implementation, not a
published support API contract, so every operation is guarded by route,
principal, rendered-structure, operation, and response probes. Read operations
wait for the rendered inventory within a bounded deadline; frontend bundle
filenames are not treated as provider API versions.

An authorized value-redacted capture on 2026-08-01 established the rendered
inventory, numeric display IDs, detail conversations, generic no-service create
flow, reply form, and two-step close confirmation. Opaque ticket locators remain
inside the managed browser and are resolved from a numeric display ID for every
operation.

## Commands and capability reasons

Five commands are enabled. Two remain fail-closed because the captured website
does not provide the contract implied by their CLI shape.

| Command | Capability | Current reason |
| --- | --- | --- |
| `ticket list` | `support.ticket.list` | enabled |
| `ticket get` (`ticket show`) | `support.ticket.show` | enabled |
| `ticket create` | `support.ticket.create` | enabled |
| `ticket reply` | `support.ticket.reply` | enabled |
| `ticket attach` | `support.ticket.attachment` | `attachment_contract_uncaptured` |
| `ticket close` | `support.ticket.close` | enabled |
| `ticket reopen` | `support.ticket.reopen` | `reopen_contract_uncaptured` |

`ticket attach` remains unavailable because REG.RU only exposes a temporary
upload handle inside a create/reply composer; there is no captured standalone
operation that binds a file to an existing ticket. `ticket reopen` remains
unavailable because closed-ticket detail exposes no reopen transition.

An expired or missing session returns `authentication_expired` and requires
`regru auth login`; the adapter never falls back to an anonymous request.

`ticket list` validates `--limit` (1–100), one-based `--page`, and
`--status all|open|closed`. Filtering and bounded pagination are applied to the
captured authenticated inventory.

## Message and attachment input

`ticket create` and `ticket reply` accept exactly one message source:

```text
--body TEXT
--file PATH
stdin when neither flag is present
```

`--attachment PATH` and `ticket attach <id> <path>` remain fail-closed and do
not read the file. Prefer `--file` or stdin for sensitive text;
`--body` is necessarily visible in the process argument list. Message input is
UTF-8, non-empty, and limited to 256 KiB. It is represented by a deferred
resolver so an unavailable or drifted adapter never reads the file or stdin.
Attachment files are likewise not opened while their contract is unavailable.
Provider attachment size/count limits and download semantics remain unknown and
are part of `attachment_contract_uncaptured`; the CLI does not invent defaults.

Mutations require terminal confirmation or `--force`. Piped stdin cannot also
serve as the confirmation terminal, so unattended stdin use requires
`--force`. A dry run performs no executor, browser, or provider call and does
not read message or attachment files; its output contains only the account,
action, and positional-argument count.

## Mutation outcome contract

Each enabled mutation gets one dispatch attempt. A proven pre-dispatch failure
is `not-sent`; a recognized success is
`committed`; a recognized provider refusal is `rejected`. Timeout, disconnect,
malformed response, `429`, `5xx`, or any other ambiguous result after possible
dispatch is `outcome_unknown` and is never retried automatically.

The same normalized intent remains blocked after `outcome_unknown` unless an
independent read proves a safe postcondition. Confirmation, `--force`, or an
acknowledgement of duplicate risk is not such proof. Support mutations are
serialized per account, while local intent fingerprints are only duplicate
guards and never provider idempotency.

Opaque ticket locators, upload handles, download URLs, cookies, CSRF values,
ticket content, and attachments must remain inside the selected browser/session
boundary. They are never normal config or log fields.
The CLI never synthesizes a ticket URL from a numeric ticket ID. It resolves the
current opaque locator from authenticated inventory inside the browser for each
detail, reply, or close operation.

The evidence and risk analysis live in
[the authorized value-redacted capture](research/support-authorized-capture.md),
[the support capability report](research/support-portal-capability.md),
[the accepted boundary](research/support-boundary-contract-refresh.md), and
[the failure analysis](research/support-boundary-failure-risk.md).
