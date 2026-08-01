# Experimental support adapter

The support command tree is deliberately visible, but every operation currently
fails closed before reading private input, opening the managed browser profile,
or making a provider request. REG.RU exposes a human support workflow and a
private web implementation, not a published support API contract.

As of 2026-08-01, the current first-party support bundle establishes the public
form shell and helper routes for service selection, temporary uploads, request
creation, phone data, alerts, and notification toggling. It does not establish
the authenticated contracts needed for ticket inventory, conversation history,
replies, lifecycle status, pagination, or downloads. Capturing those operations
would require reading real private tickets; capturing a mutation response would
require sending a real ticket or reply. Neither action was performed merely to
make the adapter appear complete.

## Commands and capability reasons

The commands preserve the intended CLI boundary while returning a structured
`capability_unavailable` error with exit code 7 and a stable `reason` detail.

| Command | Capability | Current reason |
| --- | --- | --- |
| `ticket list` | `support.ticket.list` | `authenticated_inventory_contract_uncaptured` |
| `ticket get` (`ticket show`) | `support.ticket.show` | `authenticated_detail_contract_uncaptured` |
| `ticket create` | `support.ticket.create` | `authenticated_create_contract_unverified` |
| `ticket reply` | `support.ticket.reply` | `authenticated_reply_contract_uncaptured` |
| `ticket attach` | `support.ticket.attachment` | `attachment_contract_uncaptured` |
| `ticket close` | `support.ticket.close` | `close_contract_uncaptured` |
| `ticket reopen` | `support.ticket.reopen` | `reopen_contract_uncaptured` |

The reason is more specific than “not implemented”: it records which private
contract is missing and confirms that no provider operation was attempted.
There is no browser-handoff fallback behind a ticket command.
The aggregate `support.private` capability may still report `configured` when
an account has a portal session; that reports authentication material only, not
that any ticket operation has a verified contract.

Because the current contract gate runs before browser access, an expired or
missing session does not mask the more fundamental capability reason and
reauthentication cannot enable these operations. Once an operation has a
captured contract, session loss must return `authentication_expired` and require
`regru auth login`; it must never fall back to an anonymous request.

`ticket list` reserves and locally validates `--limit` (1–100), one-based
`--page`, and `--status all|open|closed`. These flags do not imply a guessed
provider mapping; execution remains unavailable until authenticated capture
proves one.

## Message and attachment input

`ticket create` and `ticket reply` accept exactly one message source:

```text
--body TEXT
--file PATH
stdin when neither flag is present
```

`--attachment PATH` is repeatable, and `ticket attach <id> <path>` reserves the
standalone attachment workflow. Prefer `--file` or stdin for sensitive text;
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

Once a future captured adapter is enabled, each mutation gets one dispatch
attempt. A proven pre-dispatch failure is `not-sent`; a recognized success is
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
The CLI never synthesizes a ticket URL from an opaque locator. Until a captured
detail contract distinguishes a normal ticket number from an opaque secret,
`ticket get/show`, reply, close, reopen, attachment, and download all remain
unavailable.

The evidence and risk analysis live in
[the support capability report](research/support-portal-capability.md),
[the accepted boundary](research/support-boundary-contract-refresh.md), and
[the failure analysis](research/support-boundary-failure-risk.md).
