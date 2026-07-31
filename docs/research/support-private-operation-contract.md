# Private REG.RU support operation contract

Research date: 2026-07-30

## Scope and evidence boundary

This report characterizes the operation shapes visible in current first-party
REG.RU documentation and publicly served first-party frontend code. It does not
use an authenticated account, private ticket data, uploads, or mutating
requests, and it does not replay private browser requests.

The evidence has two different grades:

- **Published product contract** means REG.RU documents a customer workflow or
  a public API method.
- **Unversioned implementation evidence** means a content-hashed REG.RU
  JavaScript asset contains a route, field, or control-flow branch. It describes
  the current website build but is not a compatibility or authorization
  promise.

The current support-request asset was last modified on 2026-07-02. The current
account shell and relevant hashed account assets were last modified on
2026-07-30. Their filenames make the observations reproducible for those
builds, not stable across future builds.
([support-request bundle][support-bundle], [account shell][account-shell],
[account main bundle][account-main], [account GraphQL bundle][account-gql])

## Finding

REG.RU publishes a supported **human** ticket workflow, not a support API.
Its help centre says that customers create requests through the support form
and that account tickets are collected under **Заявки в поддержку**. The
personal-cabinet guide says that section stores tickets and can create a new
one. The support rules define the ticket system as message exchange through an
electronic form in the account control panel.
([contacting support][contact-support], [personal-cabinet guide][cabinet-guide],
[support rules][support-rules])

REG.API 2 has no support or ticket category or method. Its documented
categories and function inventory therefore provide no programmatic ticket
contract, authentication grant, identifier definition, pagination model, or
error schema.
([REG.API 2 categories][api-categories],
[REG.API 2 function inventory][api-functions])

The public frontend reveals a reasonably detailed create/pre-upload flow and
two aggregate ticket counts. It does **not** reveal a list, detail,
conversation, reply, status-transition, ticket-pagination, or attachment
download contract. None of the private support operations is safe enough to
ship behind a stable typed adapter.

## Operation matrix

| Capability | Current first-party evidence | Observable shape | Missing contract | Fail-closed adapter decision |
| --- | --- | --- | --- | --- |
| Aggregate counts | Private account GraphQL queries `support` and `newTicketsCount` read `supportNewTickets.count` and, in the first query, `supportAllTickets.count`. | No variables; nested integer-like `count` fields. | Meaning, nullability, consistency, authorization, polling limit, and compatibility. Counts are not ticket records. | Do not expose as a stable support API. |
| List tickets | Published UI stores tickets and its screenshot shows Open, Closed, and All views. | No publicly served list query/request or item schema was found. | Identifier, fields, sort order, filter enum, pagination, timestamps, unread semantics, and error behavior. | Unsupported capability. Never infer records from counts or scrape HTML. |
| Read ticket/detail | Create success code constructs `/support/tickets/<Token>` and separately receives `TicketNumber`. | An opaque path token and a human-facing number are distinct values. No detail request or response schema was found. | Whether `Token` is a locator, bearer secret, or both; message/participant/status schema; authorization; expiry. | Unsupported. Never derive a URL from `TicketNumber`, log `Token`, or accept either as a durable typed ID. |
| Create ticket | Private `POST /support/send_universal_support_message`. | Multipart fields described below; response branches on `success`, `errors`, and `response_data`. | Supported authentication, server validation, rate limits, idempotency, safe retry, compatibility, and full error vocabulary. | Unsupported mutation despite the visible shape. |
| Reply | The published rules describe message exchange in the web panel. | No reply endpoint, mutation, request fields, or response fields were found in the public assets inspected. | Everything operational, including whether replying reopens a closed ticket. | Unsupported; do not repurpose create. |
| Pre-upload attachment | Private `POST /support/upload_file`. | Multipart `upload_file` and URL-encoded `filename`; observed success needs `success` plus opaque `file_id`. | Authorization, file-ID scope/lifetime, server limits, malware/MIME policy, cleanup, replay, and reply applicability. | Unsupported mutation. |
| Remove temporary upload | Private `POST /support/remove_file`. | Multipart `file_id` and URL-encoded `filename`; frontend ignores the response body. | Idempotency, ownership, not-found behavior, retention, and whether removal is required. | Unsupported mutation. |
| Attach to create | Create multipart repeats `attachments[n][file_id]`, `attachments[n][name]`, and `attachments[n][type]`. | Client-supplied filename and browser MIME type accompany the temporary ID. | Whether metadata is trusted or normalized, ordering, deduplication, and atomicity with ticket creation. | Unsupported; never synthesize attachment records. |
| Attach to reply | No public operation evidence found. | Unknown. | Whether upload IDs are reusable and whether reply limits differ. | Unsupported. |
| Download attachment | No public route, query, signed-URL field, or metadata contract found. | Unknown. | Authentication, URL lifetime, redirect behavior, content disposition/type, range support, and filename safety. | Unsupported; do not construct download URLs. |
| Read status | Published screenshot shows human Open and Closed filters. | No machine-readable status field or enum found. | Raw states, filter mapping, terminal states, timestamps, and forward-compatible unknown values. | Unsupported. Do not encode Open/Closed as a wire enum. |
| Change status | No close, reopen, cancel, or transition operation found. | Unknown. | Transition graph, permissions, implicit transitions, concurrency, and errors. | Unsupported mutation. |
| Toggle notifications | Private `POST /support/tickets/toggle_notify` with query parameters supplied by its caller. | The public bundle does not define a typed parameter object at the request wrapper. | Exact fields, effect, authorization, and error schema. It is notification preference, not ticket status. | Do not expose or misclassify as a lifecycle operation. |
| Ticket pagination | No ticket-list pagination operation found. | Unknown. | Offset/cursor model, page size, stable ordering, end condition, and snapshot consistency. | Unsupported. The `page` parameter on service selection is unrelated and must not be generalized to tickets. |

The account GraphQL transport is itself private: the current frontend targets
`https://gql-acc.svc.reg.ru/`, includes browser credentials, obtains an
account-specific CSRF token, and sends it in an account-specific header. No
REG.RU source publishes that mechanism as third-party support API
authentication.
([account GraphQL transport][account-gql])

## Create and attachment shapes

The current support-request bundle exposes these relative website calls:

```text
GET  /support/get_services_for_user?page=<page>
GET  /support/get_services_for_user?search=<text>&page=<page>
POST /support/upload_file
POST /support/remove_file
POST /support/send_universal_support_message
GET  /support/get_user_phone_data
POST /support/tickets/toggle_notify
GET  /support/get_alert
```

These routes are relative, unversioned website endpoints. The bundle does not
identify an API version, third-party credential, scope, or deprecation policy.
([support-request bundle][support-bundle])

The create composer performs this sequence:

1. For each selected file, send multipart `upload_file` plus
   `filename = encodeURIComponent(file.name)`.
2. On `success` with `file_id`, retain
   `{ file_id, name: encodedName, type: file.type }`.
3. Submit multipart `message` and `sms`; optionally submit serialized
   `service`. For every retained file submit `attachments[n][file_id]`,
   `attachments[n][name]`, and `attachments[n][type]`.
4. If the browser considers the user a guest or a user with unconfirmed email,
   also submit `email` and `fio`.
5. On one success branch, read `response_data.Token` and
   `response_data.TicketNumber`; only the token is used to build the website
   detail path.

This is not an atomic upload contract. The bundle exposes no transaction,
rollback, upload expiry, or cleanup guarantee if one upload succeeds and a
later upload or ticket creation fails. It also exposes no idempotency key, so a
network failure after submission has an unknown commit outcome and must not be
retried automatically.
([support-request bundle][support-bundle])

### Visible constraints

The client rejects files larger than 8,388,608 bytes and files whose size is
zero or less. It slices a selection so that at most five successfully retained
files appear in the composer. Its localized uploader error text lists these
extensions:

```text
jpg jpeg png bmp gif pdf txt gz zip tar tgz rar rtf
xls xlsx doc docx csv pfx p12 cer crt key csr sig
```

The file input itself has no `accept` restriction in this build. The extension
list is therefore evidence of current UI/error copy, not proof of a complete or
stable server allow-list. There is no public contract for MIME sniffing, archive
contents, aggregate ticket size, malware scanning, filename normalization,
certificate/key handling, or different limits on replies.
([support-request bundle][support-bundle])

The form requires a non-empty `message` in client validation and validates the
guest/unconfirmed-email field as required email syntax. No stable maximum
message length or encoding/markup contract is exposed. The selected service is
submitted as a JSON serialization of a frontend object whose durable schema is
not documented.

### Visible response and error handling

Upload handling recognizes:

- an `errors` array, copied directly into UI errors;
- `success` plus `file_id`;
- a transport rejection, mapped locally to `UNEXPECTED_ERROR`.

Create handling recognizes:

- an `errors` object whose field entries are copied into form errors;
- `errors.main` containing `CREATE_TICKET_ERROR`, which opens a generic failure
  state;
- `success` plus `errors.main` containing `CREATE_TICKET_PENDING`, which opens
  the email-confirmation success state;
- `success` plus `response_data.Token`, with optional use of
  `response_data.TicketNumber`;
- every other shape or transport rejection as a generic failure.

Those branches do not define a typed provider error contract. HTTP status
mapping, code completeness, field-error shape, retryability, rate-limit
signaling, conflict semantics, and partial-success behavior remain unknown.
`remove_file` is even weaker: the frontend does not inspect its response body.
([support-request bundle][support-bundle])

## Identifiers and data-handling boundary

Three observed values must remain opaque:

- `file_id` is a temporary upload handle with unknown scope and lifetime.
- `response_data.Token` is used as a ticket path component. Until REG.RU
  documents its security properties, treat it as potentially secret and never
  log, guess, transform, or persist it as a public identifier.
- `response_data.TicketNumber` is a separate display/analytics value. The
  frontend does not prove that it can address a ticket operation.

The distinction is important: a typed adapter cannot safely define one
`TicketID` by choosing either value, and it cannot derive one from the other.
The same applies to serialized `service` objects and attachment `file_id`
values.

## Pagination and downloads

The only visible pagination-like parameter in the support-request bundle is
`page` on the **service selector**, including its search variant. That code is
for choosing a service while composing a request, not listing tickets. It
provides no evidence for ticket page size, order, cursor/offset semantics, or
completion.

No public first-party frontend source inspected here exposes an attachment
download operation. A detail-page path is not a download base URL, and neither
`Token` nor `file_id` may be interpolated into a guessed route. Downloads must
remain unavailable until REG.RU publishes or explicitly supports a contract
covering authenticated retrieval and response metadata.

## Published versus implementation-only boundary

| Evidence | What it establishes | What it does not establish |
| --- | --- | --- |
| Help article and cabinet guide | Customers can create and view tickets through REG.RU's web UI. | A programmable interface or stable wire schema. |
| Support rules | Ticket exchange is an account-panel form workflow. | Non-browser authentication or automation authorization. |
| REG.API 2 inventory | The published API surface and its omission of support methods. | That private website routes inherit REG.API compatibility guarantees. |
| Content-hashed support bundle | Current create, pre-upload, removal, and notification wrapper shapes. | Stability, authorization for a CLI, complete server validation, or safe retries. |
| Content-hashed account bundle | Current aggregate count queries and browser GraphQL transport. | Ticket list/detail records or a public GraphQL schema. |
| Open/Closed/All screenshot | Human-visible grouping exists. | A machine-readable status enum or transition graph. |

The absence of a public list/detail/reply operation in the inspected assets is
not proof that REG.RU has no such backend. It means the contract is not
available from the allowed evidence and must be treated as unknown.

## Decision for a fail-closed typed adapter

A stable `regru` support adapter should expose **no private ticket operation**.
In particular:

1. Return a deterministic unsupported/unverified capability error for list,
   detail, create, reply, upload, download, status, and notification operations.
2. Do not implement only create because its fields are visible. It is a
   consequential, non-idempotent mutation with undocumented authentication and
   ambiguous network-failure outcome.
3. Do not define typed `TicketID`, status, pagination, attachment, or error
   enums from the current frontend. Preserve the fact that their contracts are
   unknown.
4. Do not use aggregate counts as a substitute for listing and do not infer
   ticket state from count changes.
5. Do not synthesize ticket or download URLs from `TicketNumber`, `Token`, or
   `file_id`.
6. A human handoff may open REG.RU's documented support form or generic ticket
   section, clearly labelled as a website workflow rather than API-backed
   support.

Reconsider this boundary only if REG.RU publishes a versioned support contract
or explicitly authorizes an integration with documented authentication,
identifiers, schemas, pagination, errors, mutation idempotency, attachment
lifecycle, and compatibility policy.

## Sources

- [How to contact REG.RU support][contact-support]
- [Personal-cabinet guide][cabinet-guide]
- [Current support-service rules][support-rules]
- [Current REG.API 2 reference][regapi]
- [Current support-request frontend bundle][support-bundle]
- [Current account shell][account-shell]
- [Current account main bundle][account-main]
- [Current account GraphQL transport bundle][account-gql]
- [Current account support-card chunk][account-support-chunk]

[contact-support]: https://help.reg.ru/support/lichnyy-kabinet/registratsiya-i-kontaktnyye-dannyye/kak-svyazatsya-so-sluzhboy-podderzhki
[cabinet-guide]: https://help.reg.ru/support/lichnyy-kabinet/registratsiya-i-kontaktnyye-dannyye/znakomstvo-s-lichnym-kabinetom-reg-ru
[support-rules]: https://img.reg.ru/faq/pravila_obsluguvania_rules-of-service-support_11082025.pdf
[regapi]: https://www.reg.ru/reseller/api2doc
[api-categories]: https://www.reg.ru/reseller/api2doc#common_functions_description
[api-functions]: https://www.reg.ru/reseller/api2doc#common_functions_list
[support-bundle]: https://help.reg.ru/dist/knowledge-base-main.1d65b0a5ec7920d5b7e6.js
[account-shell]: https://www.reg.ru/user/account/
[account-main]: https://www.reg.ru/user/account/index.82f5f8db7d99ba5ed418.js
[account-gql]: https://www.reg.ru/user/account/4229.37275d176a2e742ac00c.js
[account-support-chunk]: https://www.reg.ru/user/account/1144.0ec38cc04acd1ff94414.js
