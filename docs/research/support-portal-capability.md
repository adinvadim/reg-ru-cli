# REG.RU support-portal capability

Research date: 2026-07-30

## Scope and source quality

This report asks whether support-ticket creation, listing/reading, replies,
attachments, and status changes have a stable authorized or public interface
that a CLI can safely depend on.

Only first-party REG.RU sources and anonymous read-only HTTP requests were used:
the current REG.API 2 reference, current help pages and support rules, and
REG.RU's published frontend JavaScript. No account session, credentials, ticket
data, uploads, or mutating requests were used.

The distinction between **product capability** and **supported integration
contract** is central here. Help pages and frontend code prove that the REG.RU
website can perform some ticket operations. They do not turn private,
unversioned website endpoints into an authorized public API. The frontend
bundle is particularly useful for identifying questions to test after login,
but it is implementation evidence rather than a compatibility promise.

## Bottom line

REG.RU provides a web ticket system and documents it as an electronic form in
the customer's account panel. The current public form supports creating a
ticket and attaching files, and the account UI stores a user's tickets. However,
REG.RU does **not** publish a support-ticket category in REG.API 2, an OpenAPI
schema, a dedicated support API reference, a documented programmatic
authentication method, or a versioned contract for any of the five requested
capabilities.

The website's current first-party JavaScript exposes internal endpoints for
ticket creation and pre-uploading attachments. These endpoints are coupled to
the browser's session/CSRF machinery, use undocumented request and response
shapes, and have no stated stability, error, authorization, pagination,
idempotency, or rate-limit contract. No public evidence establishes endpoints
for listing/reading tickets, replying to an existing ticket, downloading or
adding reply attachments, or changing a ticket's status.

Therefore the safe CLI decision is:

- do not ship a stable support adapter against the observed website endpoints;
- report support-ticket operations as an unavailable/unverified capability;
- keep browser automation or private-endpoint emulation outside the stable
  core;
- revisit the decision only after an authenticated read-first inspection and,
  ideally, written confirmation from REG.RU that the interface is intended for
  programmatic clients.

## Documented product behavior

### The supported channel is the website form

REG.RU's current support article says that customers should submit a request
through the support form and that all of their requests are collected in the
“Заявки в поддержку” section. It explicitly says support does not process
requests sent by email or through the article-feedback control; the stated
reasons include owner confirmation through the contact email and routing by
category and topic.
([How to contact support][contact-support])

The current support rules define the “Тикет-система” as message exchange
between the user and REG.RU by sending and receiving requests through an
electronic form in the customer's account control panel. The rules direct users
to the REG.RU support sites and describe interaction in terms of a web panel,
not an API.
([Support-service rules][support-rules])

The current personal-cabinet guide says the support section stores the user's
tickets and can create a new one. Its embedded screenshot shows Open, Closed,
and All filters and a closed ticket, which demonstrates UI-visible state but
does not document a status-transition operation or machine-readable enum.
([Personal-cabinet guide][cabinet-guide],
[ticket-list screenshot][ticket-list-screenshot])

These sources establish a supported human workflow:

1. choose a support category/topic or service;
2. submit through the REG.RU website;
3. use the account's ticket section to see the conversation.

They do not establish a supported CLI workflow.

### REG.API 2 has no support-ticket surface

The concrete REG.API 2 reference enumerates category names as `user`, `domain`,
`service`, `folder`, `bill`, and `zone`. Its complete function table also covers
DNSSEC, hosting, and domain-shop functions, but has no support/ticket category
or method.
([REG.API 2 categories][regapi-categories],
[REG.API 2 function table][regapi-functions])

One introductory support article describes REG.API broadly as access to
personal-cabinet functions. That marketing-level description cannot override
the reference's actual method inventory. For a CLI, only the enumerated
functions form a documented wire contract.
([What REG.API is][regapi-intro], [REG.API 2 reference][regapi])

## Evidence from the current public frontend

The current support homepage loads a first-party, content-hashed JavaScript
bundle from `help.reg.ru`. At the time of research the server reported it as
last modified on 2026-07-02. The bundle contains the source module names for
REG.RU's `@regru/support-request` frontend package and reveals the following
browser calls:

| Website action | Observed internal request |
| --- | --- |
| Load account services | `GET /support/get_services_for_user` |
| Upload a temporary file | `POST /support/upload_file` |
| Remove a temporary file | `POST /support/remove_file` |
| Create the support request | `POST /support/send_universal_support_message` |
| Load phone data | `GET /support/get_user_phone_data` |
| Toggle ticket notification | `POST /support/tickets/toggle_notify` |
| Load a support alert | `GET /support/get_alert` |

([Current support-request frontend bundle][support-bundle])

Anonymous read-only requests corroborated only the low-risk GET behavior:
`get_services_for_user` returned an empty service list,
`get_user_phone_data` returned `{"success":0}`, and `get_alert` returned a
current administrative alert. These responses did not expose an API
description, authentication challenge, ticket schema, or account data.
([Anonymous service-list response][services-read],
[anonymous phone-data response][phone-read],
[anonymous support-alert response][alert-read])

The creation form's observed multipart flow is:

1. upload each file separately with fields `upload_file` and URL-encoded
   `filename`;
2. receive a temporary `file_id`;
3. submit optional serialized `service`, `message`, `sms`, and attachment
   triplets `attachments[n][file_id]`, `attachments[n][name]`, and
   `attachments[n][type]`;
4. for a guest or user with unconfirmed email, also submit `email` and `fio`;
5. on one observed success branch, read `response_data.Token` and
   `response_data.TicketNumber`, then construct a website link under
   `/support/tickets/<token>`.

The same frontend advertises a maximum of five attachments, a maximum of 8 MB
per file, rejection of empty files, and an allow-list of common image,
document, archive, certificate, and key extensions. These are current UI
constraints, not a server contract. In particular, the bundle does not specify
temporary-file lifetime, MIME validation, download semantics, malware handling,
or whether the same rules apply to replies.
([Current support-request frontend bundle][support-bundle])

The frontend also contains a branch that asks guests or users with unconfirmed
email to confirm the request from an email. A 2021 REG.RU announcement said
email confirmation applied to every request, including requests from logged-in
users. The current frontend's control flow appears narrower, so the exact
current rule must be verified with an authenticated account; the older
announcement should not be encoded as a present-day invariant.
([2021 confirmation announcement][confirmation-news],
[Current support-request frontend bundle][support-bundle])

### Why these endpoints are not suitable as a stable CLI API

The evidence supports the inference that these are private web-application
endpoints:

- they are relative, unversioned `/support/...` routes rather than REG.API 2
  methods;
- their only discovered client is the REG.RU website bundle;
- the public API reference does not document them;
- the page initializes browser authentication, session, and CSRF-related
  machinery, while no API-key, OAuth, signature, or service-token flow is
  specified for support;
- their request and response fields are recoverable only from minified frontend
  implementation code;
- there is no published compatibility, deprecation, error, rate-limit,
  idempotency, or authorization contract.

This is an inference from first-party implementation evidence, not a claim
that REG.RU intentionally forbids non-browser use. The narrower claim is enough
for the product decision: stability and authorization for a CLI have not been
established.

## Capability matrix

| Capability | Product/UI evidence | Stable authorized/public interface | CLI disposition |
| --- | --- | --- | --- |
| Create ticket | Current web form; internal multipart submission endpoint | **Not established** | Capability error |
| List/read tickets and conversation | Current help says tickets are stored in the account section; private route `/support/tickets/index` | **Not established** | Capability error |
| Reply to an existing ticket | Support rules describe message exchange through the web form | **Not established**; no reply endpoint or schema found | Capability error |
| Attach files to a new ticket | Current form pre-uploads files and submits temporary IDs | **Not established**; UI-only constraints and no lifetime/download contract | Capability error |
| Attach files to a reply | No public first-party contract found | **Not established** | Capability error |
| Read status | Account guide screenshot shows open/closed views | **Not established**; no machine-readable status schema | Capability error |
| Close/reopen/change status | No public first-party evidence found | **Not established** | Capability error |
| Toggle notifications | Internal website POST route is visible | **Not established** and is not a ticket status change | Do not expose |

The notification route is worth calling out because its name includes
`support/tickets`: `toggle_notify` changes notification preference, not ticket
lifecycle status. It must not be misinterpreted as close/reopen support.

## Precise authenticated questions that remain

An authorized, read-first browser/network inspection should answer these
questions before the map can consider a support adapter:

1. **Authentication and intended use**
   - Which host actually serves the ticket list and conversation APIs after
     login?
   - Are calls authorized by the normal website cookie plus CSRF/JWT, or is
     there a documented API token/OAuth/service credential?
   - Does REG.RU authorize non-browser clients, and is there a supported
     versioned endpoint or partner interface not linked from the public
     reference?
   - Can one credential select among the user's REG.RU profiles, or is each
     account/session isolated?

2. **Listing and reading**
   - What read-only calls populate `/support/tickets/index` and a ticket detail
     page?
   - What are the stable ticket identifier, pagination/cursor fields, sort
     order, timestamps/time zone, unread markers, participant model, category,
     service binding, and conversation schema?
   - Does the URL token act as an opaque locator, an authorization secret, or
     both? It must not be logged until this is known.
   - Can guest-created tickets later be listed under an account, and how are
     they linked?

3. **Replies**
   - What endpoint adds a customer reply, and which ticket identifier does it
     require?
   - Are replies plain text, Markdown, or HTML; what size and encoding limits
     apply?
   - Is there an idempotency mechanism, client message ID, duplicate-detection
     behavior, or safe retry rule?
   - Does replying to a closed/resolved ticket reopen it implicitly?

4. **Attachments**
   - Do replies reuse `upload_file`, or use a separate upload service?
   - Are five files, 8 MB, and the frontend extension list enforced by the
     server and stable across both creation and replies?
   - How long does an uncommitted `file_id` live, and is cleanup automatic?
   - How are attachment metadata and authenticated download URLs represented?
   - Are MIME type, archive contents, certificate/key files, malware scanning,
     and per-ticket totals validated independently of file extension?

5. **Status and lifecycle**
   - What status values actually appear on the wire, and which are terminal?
   - Can a customer explicitly close, reopen, cancel, or otherwise transition a
     ticket? What transition graph and permissions apply?
   - Are Open/Closed merely UI filters mapped from a richer support-platform
     state model?
   - Does notification preference have any coupling to status? Public evidence
     currently suggests it is separate.

6. **Operational contract**
   - What are the structured error envelope and machine-readable codes?
   - Are there per-account/IP rate limits, request-size limits, polling
     guidance, webhooks, or conditional-read support?
   - What are the compatibility/deprecation policy and support contact for an
     integration?
   - Does email confirmation still apply to logged-in accounts with a confirmed
     contact email, and which operations require additional confirmation?

The inspection should begin with network observation of list and detail pages
only. Creation, replies, uploads, notification changes, and status mutations
should remain untested until their exact calls are understood and separately
authorized.

## Recommended decision for the Wayfinder map

The route is clear enough for the current planning boundary: REG.RU support is
a real web product, but there is no demonstrated stable support API to build
into the initial `regru` CLI. The support adapter should remain capability
gated. The CLI may provide a human-facing pointer to the official support page,
but it should not claim that this is API-backed ticket support, synthesize
ticket URLs, reuse browser cookies, or reproduce the private multipart calls.

If authenticated research later finds only the same session-bound internal
routes, that should be treated as evidence for a separate experimental browser
adapter decision, not as approval to place those routes in the stable core.

## Sources

- [Current REG.API 2 reference][regapi]
- [What REG.API is and who can use it][regapi-intro]
- [How to contact REG.RU support][contact-support]
- [Personal-cabinet guide][cabinet-guide]
- [Current support-service rules][support-rules]
- [Current support-request frontend bundle][support-bundle]
- [2021 ticket email-confirmation announcement][confirmation-news]

[regapi]: https://www.reg.ru/reseller/api2doc
[regapi-categories]: https://www.reg.ru/reseller/api2doc#common_functions_description
[regapi-functions]: https://www.reg.ru/reseller/api2doc#common_functions_list
[regapi-intro]: https://help.reg.ru/support/partneram/reg-api/chto-takoye-reg-api-i-kto-mozhet-ispolzovat-reg-api
[contact-support]: https://help.reg.ru/support/lichnyy-kabinet/registratsiya-i-kontaktnyye-dannyye/kak-svyazatsya-so-sluzhboy-podderzhki
[cabinet-guide]: https://help.reg.ru/support/lichnyy-kabinet/registratsiya-i-kontaktnyye-dannyye/znakomstvo-s-lichnym-kabinetom-reg-ru
[ticket-list-screenshot]: https://img.reg.ru/faq/240520_zaavki_lk.png
[support-rules]: https://img.reg.ru/faq/pravila_obsluguvania_rules-of-service-support_11082025.pdf
[support-bundle]: https://help.reg.ru/dist/knowledge-base-main.1d65b0a5ec7920d5b7e6.js
[confirmation-news]: https://www.reg.ru/company/news/11693
[services-read]: https://help.reg.ru/support/get_services_for_user?page=0
[phone-read]: https://help.reg.ru/support/get_user_phone_data
[alert-read]: https://help.reg.ru/support/get_alert
