# Failure and security risk of an experimental REG.RU support adapter

Research date: 2026-07-30

## Scope and conclusion

This note narrows the findings from [Research: characterize private portal
failure and retry semantics][resilience-ticket] to the product choice in
[Grilling: choose the REG.RU support integration boundary][boundary-ticket].
It uses current first-party REG.RU documentation and publicly served frontend
code, plus the HTTP and cookie standards needed to interpret retry and session
behavior. No account was authenticated and no upload or mutation was sent.

The central risk is not merely that the interface is private. A support
mutation can commit while its response is lost, yet the current interface has
neither a client-supplied idempotency key nor a public independent ticket-read
contract. The adapter therefore cannot distinguish “not created” from “created
but response lost.” Contract probes, serialization, confirmation, and local
deduplication reduce the frequency or blast radius of failures; none makes an
ambiguous create, reply, or attachment mutation safe to retry.

REG.RU documents support as a human workflow through its support form and the
account's **Заявки в поддержку** section. Its published REG.API 2 inventory
does not provide support-ticket methods. The current website implementation is
therefore evidence about today's behavior, not a supported programmatic
contract. ([REG.RU support guidance][support-guidance], [REG.API 2
reference][reg-api])

## Current implementation evidence

The public support page currently loads the content-hashed
`knowledge-base-main.1d65b0a5ec7920d5b7e6.js` bundle (observed SHA-256
`168c46c43df9c68ccd77a4ee36f979a250809e2437d917c0442a453f180cc8e9`).
That bundle exposes unversioned, same-origin POSTs for temporary upload,
temporary-file removal, ticket creation, and notification toggling. Its POST
wrapper first calls `/user/regenerate_csrf_token` and injects the returned
value as `X-Csrf-Token`. Ticket creation itself sends multipart form data to
`/support/send_universal_support_message`. ([Current support page][support-page],
[current support bundle][support-bundle])

The create flow is multi-stage:

1. Each file is uploaded independently to `/support/upload_file`.
2. A successful response returns an opaque `file_id`.
3. Ticket creation sends each `file_id` with a client-supplied filename and
   browser MIME type.
4. Only a recognized success response yields the opaque `Token` used in the
   ticket URL and the separate `TicketNumber`.

The bundle supplies no client request ID or idempotency key, performs no
operation-status lookup, and contains no retry path for these calls. Its
visible five-file/8 MiB and extension checks are client implementation
constraints; no public source establishes their stability, server enforcement,
temporary-upload lifetime, cleanup, malware/MIME policy, or applicability to
replies. ([Current support bundle][support-bundle])

The CSRF flow and relative routes couple every mutation to the active browser
session. HTTP cookies are selected by domain/path and returned on applicable
requests, while their application meaning is server-defined. Exporting cookies
and CSRF values into a standalone client would therefore expand the secret
boundary without creating a documented credential contract. Because the
multipart create request contains no explicit REG.RU account identity, the
active session is also the effective account selector. ([RFC 6265 cookie
semantics][rfc6265], [current support bundle][support-bundle])

## Failure and security consequences

| Failure surface | Consequence | What can bound it | What remains unbounded |
| --- | --- | --- | --- |
| Timeout, disconnect, malformed response, `429`, or `5xx` after a POST may have left the client | Duplicate ticket/reply/upload if the adapter retries; false “failed” result if it does not | One attempt, bounded timeout, explicit `outcome-unknown`, and no automatic or hidden transport retry | Whether the first request committed; there is no provider deduplication key or public read-after-write proof |
| Multi-stage attachments | Successful temporary uploads can be orphaned; a ticket can be created without the operator receiving its identifiers; remove itself can have an ambiguous outcome | Upload serially, stop before ticket creation on any unrecognized upload result, keep handles ephemeral, and make cleanup best-effort | Upload lifetime, atomicity, server validation, orphan cleanup, and whether retrying an upload duplicates stored data |
| Content-hashed but unversioned frontend and unversioned backend routes | A field, error, CSRF rule, or semantic meaning can change without a CLI-compatible release signal | Allowlist an audited bundle hash and exact route/request/recognized-response shape; fail closed on any mismatch | Backend-only or semantic drift that preserves the same visible shape; authorization or deprecation policy |
| Cookie/CSRF/session coupling | Wrong-account submission, expired-token failures, or disclosure if browser secrets escape into config/logs | Dedicated browser profile per account, first-party-origin execution, immediate identity preflight, serialized mutation, no cookie/CSRF export | Server-side session races, undocumented refresh behavior, and any right to automate the website |
| Opaque `file_id`, `Token`, and `TicketNumber` | Secret leakage or incorrect identifier assumptions; inability to reconcile a lost response | Treat all as opaque and potentially secret; redact logs, traces, fixtures, crash reports, and analytics | Whether `Token` is also an authorization secret, identifier lifetime, or a durable machine ID |
| Unknown rate and error contract | Account/IP throttling and misclassified failures | Low concurrency, finite local budget for read-only probes, circuit breaker, and honoring a valid `Retry-After` only before a later *authorized* attempt | Permitted rate, ban behavior, SLA, and whether an application-level error arrived with HTTP success |

HTTP explicitly advises clients not to retry a non-idempotent request unless
they know its semantics are idempotent or can prove the original was never
applied. A POST is retryable here only when the implementation has positive
evidence that no request bytes were sent; a generic network error is not that
evidence. `Retry-After` specifies delay, not deduplication, so its presence on
`429` or `503` would not make the earlier support POST safe to repeat.
([RFC 9110 idempotency rules][rfc9110-idempotency], [RFC 9110
`Retry-After`][rfc9110-retry-after], [RFC 6585 `429`][rfc6585])

## Minimum experimental safety envelope

If the private adapter is accepted despite these limits, it should be
off-by-default and explicitly labelled unsupported. A dry run must perform no
POST and must not regenerate CSRF. Before each mutation, the adapter should
verify the first-party origin, active account identity, audited bundle hash,
CSRF wrapper markers, route, multipart field set, and recognized response
branches. It should refuse cross-origin redirects and serialize all support
mutations per browser profile.

The execution state machine should have only four terminal results:
`committed` for a recognized success response, `rejected` for a recognized
provider rejection, `not-sent` when the client can prove dispatch never
started, and `outcome-unknown` for every ambiguous post-dispatch result.
`outcome-unknown` must disable further automated mutations for that profile
until a human inspects **Заявки в поддержку**. A local intent fingerprint and
pending ledger can prevent the same CLI installation from casually repeating
the command, but it is not a provider idempotency key and cannot coordinate
another machine or a manual browser submission.

Attachments need a separate explicit opt-in because they add confidentiality
and partial-completion risk. Read each selected file once after confirmation,
apply the currently audited conservative size/type checks, never persist its
contents or opaque upload handle outside the short-lived operation, and report
partial temporary uploads without claiming cleanup. These controls protect the
client; they cannot establish REG.RU's retention, scanning, or atomicity
behavior.

Even under this envelope, the allowed public evidence only supports an
experiment around the observed create/pre-upload implementation. It does not
provide list, detail, reply, status-transition, pagination, or download wire
contracts. Those capabilities require separately authorized authenticated
observation before they can even be typed, and ambiguous mutation delivery
would still remain unresolved without provider idempotency or a reliable
independent read.

[resilience-ticket]: https://github.com/adinvadim/reg-ru-cli/issues/21
[boundary-ticket]: https://github.com/adinvadim/reg-ru-cli/issues/22
[support-guidance]: https://help.reg.ru/support/lichnyy-kabinet/registratsiya-i-kontaktnyye-dannyye/kak-svyazatsya-so-sluzhboy-podderzhki
[reg-api]: https://www.reg.ru/reseller/api2doc
[support-page]: https://help.reg.ru/support/
[support-bundle]: https://help.reg.ru/dist/knowledge-base-main.1d65b0a5ec7920d5b7e6.js
[rfc6265]: https://datatracker.ietf.org/doc/html/rfc6265#section-4.1.2
[rfc9110-idempotency]: https://datatracker.ietf.org/doc/html/rfc9110#section-9.2.2
[rfc9110-retry-after]: https://datatracker.ietf.org/doc/html/rfc9110#section-10.2.3
[rfc6585]: https://datatracker.ietf.org/doc/html/rfc6585#section-4

## Conditional recommendations for the boundary decision

1. **Safe authenticated browser handoff only:** choose this when support access
   is needed now but reliability and credential containment matter more than
   unattended automation. Let `regru` open the documented first-party support
   page in the selected isolated profile; the user reviews and submits in the
   provider UI. This is the recommended present default because REG.RU owns
   session refresh, validation, attachment handling, and recovery visibility.
2. **Block automation pending a supported contract:** choose this when
   “agent-safe” means a command must have deterministic retry/reconciliation
   behavior or when organizational policy requires explicit authorization.
   Reconsider only after REG.RU supplies a supported auth grant, versioned
   schemas, stable identifiers and reads, rate/error rules, attachment
   lifecycle, and either mutation idempotency or a documented reconciliation
   contract.
3. **Accept an experimental private adapter:** choose this only if the owner
   deliberately accepts provider-policy, maintenance, duplicate-delivery,
   wrong-session, and attachment-retention risk. Keep it opt-in, interactive,
   one-attempt, fail-closed, and limited initially to the publicly observed
   create/pre-upload flow. Do not promise unattended agent use, automatic
   retries, duplicate prevention, or the full ticket lifecycle: the safeguards
   above reduce exposure but cannot bound those claims.
