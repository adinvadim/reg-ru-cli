# Authenticated portal billing implementation contract

Research date: 2026-07-31

## Question and evidence boundary

This report asks what the current authenticated REG.RU portal can safely add
to the published REG.API 2 billing contract after `regru auth login`:
invoice enrichment, methods available for a particular bill, and a runtime
checkout handoff.

The investigation used current REG.RU documentation and anonymously served
first-party frontend code. It did not log in, read account data, call the
account GraphQL service, open a checkout route, choose a payment method, or
change provider state. Existing repository research was used to locate likely
sources, but every material claim below was checked against the current
first-party source.

The evidence is deliberately split:

- **Published contract** is behavior REG.RU documents for customers or API
  clients.
- **Private implementation evidence** is behavior present in the current
  personal-cabinet build. It can support an explicitly private, probed adapter,
  but is not a compatibility promise.
- **Unverified authenticated behavior** is a gap that public source cannot
  close. It needs an authorized, value-redacted capture before the relevant
  capability is enabled.

On 2026-07-31 the anonymous account shell and all inspected assets returned
`Last-Modified: Fri, 31 Jul 2026 06:21:15 GMT`. The shell referenced the
hashed [account application][account-index], [authentication client][account-auth],
and [account GraphQL client][account-gql]. The application still maps
`/balance/bills` to the hashed [Bills route chunk][account-bills] and
`/balance/` to the hashed [Balance route chunk][account-balance]. The hashes
make the observations reproducible for this build, not durable API versions.

## Decision

There is one useful current private read contract, but no complete public-only
contract for bill-specific payment availability or provider checkout URLs.

1. The private `userBills` GraphQL query is sufficient to design a guarded
   invoice-list enrichment adapter. It returns cabinet history and display
   fields that REG.API does not expose to an ordinary client.
2. The adapter must remain disabled until one authorized redacted capture
   verifies the request envelope, response nullability/types, and—most
   importantly—that portal `id` is the same invoice identifier as REG.API
   `bill_id` for the same account.
3. The portal's current `bill_sid` and
   `/billing/payment/{order,choose}` routes are sufficient to design an
   in-browser handoff. They are not a shareable “payment link” contract.
   `bill_sid` must remain inside the browser-session adapter and never appear
   in CLI output, config, logs, or fixtures.
4. No current public asset exposes the methods available for a particular
   bill. `pay_type_title` is the title of the bill's selected method, while
   `allowedBindingTypes` belongs to saved balance-payment bindings. Neither is
   a bill checkout availability list.
5. No current published or public-frontend source specifies a provider
   redirect URL, token, QR payload, expiry, replayability, or shareability.
   A command may eventually open the provider flow in the managed browser, but
   must not return the URL as a durable artifact.

The implementation boundary is therefore:

| Capability | Readiness | Required behavior |
| --- | --- | --- |
| REG.API unpaid invoice fields and line items | Published | Implement directly |
| Portal invoice history/display enrichment | Private; structurally mapped | Enable only after the redacted capture and a build/schema probe |
| Bill-specific available payment methods | Not mapped | Keep unavailable until the choose page's read contract is captured |
| Open the current REG.RU checkout chooser | Private route mapped | Keep behind the session broker, confirmation, and capture gate |
| Return a provider payment URL | No contract | Do not implement as a URL-returning capability |

## Published baseline

### REG.API remains the authoritative public invoice contract

`bill/get_not_payed` is available to clients and accepts `limit` (default 100,
maximum 1,024) and `offset`. Its documented bill records include `bill_id`,
date, currency, base and total payment, payment type/status, and line items
with service identifiers and actions. It returns only unpaid bills.
([REG.API `bill/get_not_payed`][api-unpaid])

`bill/get_for_period` supplies broader history but is explicitly partner-only.
An ordinary-client CLI must not use it as the fallback for portal history.
([REG.API bill capabilities][api-bills])

`bill/change_pay_type` is available to clients, but it is a consequential
mutation, not checkout-link creation. It accepts only `prepay`, `yamoney`, or
`bank`; REG.RU says `prepay` pays immediately. Its documented result has bill,
currency, amount, payment-type, and payment-status fields, but no checkout URL,
provider token, expiry, or redirect metadata.
([REG.API `bill/change_pay_type`][api-change-pay-type])

The published payment-type vocabulary is not internally uniform:
`bill/get_not_payed` documents `prepay`, `bank`, `pbank`, and `yacard`, while
`bill/change_pay_type` accepts `prepay`, `yamoney`, and `bank`. The CLI must
preserve raw read values and validate mutations against the specific
documented method rather than inventing one shared enum.
([REG.API bill fields][api-unpaid],
[REG.API `bill/change_pay_type`][api-change-pay-type])

### The published checkout journey is interactive

For an existing bill, REG.RU instructs the user to authenticate, open
**Balance → Bills**, choose **Change payment method** for the unpaid bill, and
select one of the methods available on the next page. The help page links
**Balance** to the generic account shell, not to a documented bill-specific
URL. ([change or delete a bill][help-change])

The current help page names account balance, cards, bank transfer, foreign bank
transfer, ЮMoney, SberPay, SBP, cash, and Yandex Split. Availability depends on
conditions such as balance sufficiency, account-owner type, residency,
currency, amount, device/app availability, and saved-instrument choices. The
page is a product catalog and procedure guide, not a machine-readable
per-invoice availability contract.
([payment methods and conditions][help-methods])

The help page also shows that some handoffs require card/SMS interaction,
another site, a phone push, or a QR code. It publishes none of the generated
URLs or payload formats. These interactions must remain in a user-visible
browser. ([payment methods and conditions][help-methods])

## Current private portal read contract

### Session and CSRF choreography

The account frontend uses a browser session coordinated through
`https://login.reg.ru`. Its current auth client recognizes
`session_refreshed`, exposes current `user_id` and `screen_name` to the
frontend, and refreshes a stale local auth check after 150 seconds. The
150-second value is a client refresh threshold, not a provider session
lifetime. ([current authentication client][account-auth])

Account GraphQL uses a distinct service-specific CSRF mechanism:

- HTTP endpoint `https://gql-acc.svc.reg.ru/`;
- browser credentials included;
- cookie `acc-csrftoken`;
- header `x-acc-csrftoken` containing that cookie's value;
- credentialed `GET
  https://gql-acc.svc.reg.ru/account/issue_csrf_token` when the cookie is
  absent;
- `Apollo-Require-Preflight: true`;
- operation name appended as the URL query parameter `opName`.

The same bundle configures `wss://gql-acc.svc.reg.ru/` for subscriptions, but
`userBills` is an HTTP query. ([current account GraphQL client][account-gql])

This is a browser-origin contract. The billing adapter must execute inside the
same isolated browser profile and first-party origin choreography as the
session broker. It must not export cookies to Go's general HTTP client or
generalize the login CSRF cookie/header to account GraphQL.

### `userBills` operation

The current `/balance/bills` route issues this operation:

```graphql
query userBills(
  $limit: Int!
  $offset: Int!
  $searchQuery: String
  $startDate: String
  $finishDate: String
) {
  userBills(
    limit: $limit
    offset: $offset
    searchQuery: $searchQuery
    startDate: $startDate
    finishDate: $finishDate
  ) {
    has_more
    total_count
    items {
      id
      create_date
      pay_date
      pay_type
      pay_type_title
      pay_time
      amount
      description
      state
      bill_sid
      freezed
      pay_status
      submode
      is_prepayment
      isDownloadGarantLetter: is_download_garant_letter
    }
  }
}
```

The UI requests 50 rows at a time. It sends empty strings for unset search and
date filters, advances `offset`, and continues while `has_more` is true. It
renders `id`, creation date, state, paid date/time, selected payment-method
title, description, and amount. No separate bill-detail query or line-item
field appears in the current Bills route chunk.
([current Bills route chunk][account-bills])

Important limits of that shape:

- It is a list/history query, not an invoice-detail endpoint.
- It has no direct `id` argument. A `show` adapter must page and match; the
  semantics of `searchQuery` are not documented precisely enough to use it as
  an exact identifier lookup.
- It contains no currency and no service line items. REG.API remains the
  source for those published fields when available.
- It contains `pay_type_title`, but no available-method list or provider
  checkout metadata.
- The GraphQL source reveals selected field names, not scalar types,
  nullability, domain enums, maximum page size, ordering guarantee, or a
  snapshot/consistency contract.
- `description` and all server titles are provider text. Escape them for the
  selected output format and never interpret them as terminal markup.

### Identifier semantics

The cabinet has two identifiers:

- `id`, displayed as the invoice number (`№<id>`) and also used by the
  guarantee-letter route as `bill_id=<id>`;
- `bill_sid`, used only in payment and printable-bill route construction.

This is strong implementation evidence that portal `id` denotes the familiar
bill number, but it is not a published proof that `id == REG.API bill_id`
across all invoice sources and account roles. The capture gate must verify
that equality on at least one bill visible through both surfaces before the
CLI joins the records. `bill_sid` must be treated as an opaque,
account/session-scoped locator; never derive it from `id`, accept it as user
input, or expose it as stable output.
([current account application][account-index],
[current Bills route chunk][account-bills],
[REG.API bill identifier][api-unpaid])

### Safe initial enrichment model

After the capture gate passes, merge a portal row into a REG.API invoice only
when all of these hold:

1. the portal principal probe matches the selected account profile;
2. the private-build compatibility probe passes;
3. the normalized portal `id` exactly matches the normalized REG.API
   `bill_id`;
4. invariant fields available on both sides do not conflict (at minimum bill
   amount and payment state after an explicit documented mapping);
5. at most one portal row matches.

No match means `portal_enrichment: unavailable`, not “invoice missing.”
Multiple matches or invariant conflict means `private-contract-incompatible`;
do not guess. A portal-only history row may be returned by a separately named
portal-history command after capture, but must not be represented as a
REG.API-enriched invoice without a verified join.

Expose only stable CLI-owned fields. Preserve raw private values in memory for
decision logic, but do not promise `state`, `submode`, `freezed`, or
`pay_status` as durable provider enums. Never expose `bill_sid`.

## Current checkout boundary

### Routes generated by the cabinet

The current bill-row component constructs:

```text
/billing/payment/order?bill_sid=<opaque>
/billing/payment/choose?bill_sid=<opaque>
/bill/form?bill_sid=<opaque>&pdf=1
```

For row navigation it sends a cancelled bill to `choose`, an on-hold bill
nowhere, and other rows to `order`. The explicit **Change payment method**
action sends a non-paid, non-on-hold bill to `choose`. The component's visible
**Pay** button is enabled when `state != paid` and
`pay_status != onhold`.
([current account application][account-index])

These predicates are UI behavior, not a provider state-transition contract.
In particular, the mere presence of a constructed route does not prove that
the server will accept the bill, that a paid row is payable, or that every
unknown future state is safe.

### Handoff contract for `regru`

If authorized capture validates the route, `billing invoice payment-link`
should behave as a browser handoff despite its command name:

1. perform the normal principal and private-contract probes;
2. find exactly one portal row by verified `id`;
3. reject paid, on-hold, missing, ambiguous, or unknown states locally;
4. require the CLI's financial-operation confirmation or `--force`;
5. navigate the existing managed browser context to the relative `choose`
   route while keeping `bill_sid` inside the browser world;
6. leave method selection, credentials, QR/SMS/push/CAPTCHA, and payment
   confirmation to the user in the visible browser.

The machine-readable result should describe the action, not leak a URL:

```json
{
  "handoff": "browser_opened",
  "destination": "reg-ru-checkout",
  "shareable": false,
  "expires_at": null
}
```

`expires_at: null` means “unknown,” not “does not expire.” The route is
session-bound and contains a private opaque locator, so it must be classified
as non-shareable even if copying it happens to work in one observation.

Do not navigate directly to `order` in the first implementation. The current
UI uses it for its default row transition, but public code does not reveal
whether loading it can immediately select, bind, or initiate a payment path.
Starting from `choose` keeps the consequential method choice visible to the
user.

### Why saved bindings are not availability

The current Balance page's `userBalanceInit` query returns persistent saved
bindings and `allowedBindingTypes`. It also exposes a ЮMoney binding
configuration and the same chunk contains mutations whose responses may
include `binding_url` or `bind_pay_url`.
([current Balance route chunk][account-balance])

That model governs adding/reordering saved instruments, autorefill, and
autorenew. It takes no bill identifier and does not return the methods
available for a specific invoice. `binding_url` is a binding-enrollment
handoff, not a bill checkout URL. Reusing either shape for invoice payment
availability would conflate two different domains and risk unexpected
persistent-access or one-click-payment behavior.

## Failure and state contract

The adapter must fail closed and keep transport, GraphQL, domain, and local
compatibility failures distinct:

| Observation | CLI classification | Behavior |
| --- | --- | --- |
| Core session refresh is not the expected authenticated principal | `reauth-required` or `account-mismatch` | Stop before account GraphQL |
| CSRF issuer fails or `acc-csrftoken` remains absent | `portal-session-incompatible` | Do not send `userBills` |
| Network failure before a read is sent | `portal-unavailable` | A bounded read retry is permissible |
| HTTP `401` or an authenticated rejection confirmed by the session probe | `reauth-required` | Stop and ask for login |
| HTTP non-success, GraphQL `errors`, null top-level data, or an unexpected response shape | `private-contract-incompatible` unless clearly transient | Do not scrape rendered page text |
| `total_count == 0` with an empty `items` list | successful empty result | Not an auth failure |
| Pagination does not advance, exceeds a finite local budget, or conflicts with `has_more`/`total_count` | `private-contract-incompatible` | Stop; do not loop indefinitely |
| Requested `id` has no match | `invoice-not-found-on-portal` | Preserve the REG.API record without enrichment |
| Duplicate `id` or REG.API/portal invariant conflict | `private-contract-incompatible` | Never choose one heuristically |
| `state == paid` | `invoice-already-paid` for handoff | Do not open checkout |
| `pay_status == onhold` or `freezed` is true | `invoice-not-payable` | Do not open checkout |
| Unknown state/status or missing `bill_sid` | `checkout-unavailable` | Do not generalize the current UI's fallback |
| Choose page rejects, redirects to auth, or no bill-specific methods are present | typed handoff failure | Keep the browser visible for recovery; emit no URL/token |

The public sources do not define rate limits, read timeouts, GraphQL error
codes, CSRF-token lifetime, ordering consistency, or checkout idempotency.
Use a finite local deadline and one in-flight billing read per profile. Never
automatically retry checkout navigation or a payment-method action after an
ambiguous result.

## Contract and version probes

Before every private billing capability, or once per short-lived session with
fail-closed invalidation, perform these checks:

1. **Principal probe:** the session broker reports the expected authenticated
   profile, not merely a loaded account shell.
2. **Build probe:** fetch the anonymous account shell with no cookies and
   locate its current hashed account application, GraphQL client, and Bills
   route chunk. Record only hashes/ETags/Last-Modified values, never session
   data.
3. **Transport-shape probe:** verify the current public GraphQL client still
   names `gql-acc`, `acc-csrftoken`, `x-acc-csrftoken`,
   `/account/issue_csrf_token`, credential inclusion, and `opName`.
4. **Operation-shape probe:** parse the GraphQL document from the current
   Bills chunk and require the expected operation name, arguments, and
   allowlisted fields. Compare a normalized AST or canonical query, not
   minified byte offsets.
5. **Route-shape probe:** require the current application to map
   `/balance/bills` and construct the exact relative `choose` route from
   `bill_sid`. Do not accept a new host, scheme, parameter, or provider
   redirect automatically.
6. **Response probe:** on the first authorized read, validate exact envelope,
   scalar JSON kinds/nullability learned from the redacted fixture, pagination
   invariants, and unique identifiers before returning data.

A new hash alone is not an incompatibility if all semantic probes pass. A
semantic change is an incompatibility even when a cached hash appears
unchanged. The probe must never execute a mutation or navigate checkout.

## Authorized redacted capture required before implementation

One capture session may close the read and chooser gaps, but it must be
explicitly authorized because it reads private invoice data. Preserve only a
minimal fixture with values replaced while retaining JSON kinds, nullability,
array cardinality class, and enum spelling.

### Required for invoice enrichment

- Capture one `userBills` HTTP request and response from the authenticated
  Bills view, including method, URL path/query keys, non-secret header names,
  content type, GraphQL envelope, variables, status, and GraphQL error
  envelope. Do not retain cookies, CSRF values, principal fields, descriptions,
  amounts, dates, service names, or identifiers.
- Capture an empty result and one non-empty page if the account can reach both
  without creating data.
- Compare one existing bill visible in REG.API and the portal inside the
  trusted process; record only `id_matches_bill_id: true|false` plus redacted
  invariant comparisons.
- Record actual scalar JSON kinds and nullability for every selected field and
  whether `bill_sid` changes across refresh/login. Never preserve its value.
- Let one session expire or use an already expired disposable profile to
  capture the non-secret auth/GraphQL failure classification without recording
  response bodies containing account data.

### Required for payment-method availability

- Open the `choose` page for an existing unpaid disposable/test bill, but do
  not select a method.
- Capture the read-only request(s) that populate the method list: operation
  name/route, redacted request shape, response schema, per-method stable key,
  localized title, disabled/unavailable reason, constraints, and whether a
  method is preselected.
- Prove the request is bill-scoped and identify whether it consumes `bill_sid`
  or another opaque locator.
- Capture no saved-instrument labels, last digits, person/contact data,
  balance, provider tokens, or account identifiers.

Until that capture exists, `billing invoice payment-methods` must return a
structured `capability-unavailable`, not the global help-page catalog and not
`allowedBindingTypes`.

### Required for runtime provider handoff

This is a separate, explicitly consequential capture. It needs a disposable
bill or provider-supported test environment and user approval immediately
before a method is selected.

- For each supported method class, record whether selection causes a POST,
  GraphQL mutation, same-site navigation, external redirect, QR payload, or
  device/push interaction.
- Preserve only host class, path-template shape, request/response field names,
  redirect count, and whether the handoff can be resumed in the same browser.
  Do not retain provider URLs, tokens, QR contents, card/bank data, phone
  numbers, or one-click binding identifiers.
- Stop before confirming, paying, binding an instrument, or entering
  card/SMS/CAPTCHA/identity data.
- Do not test shareability by sending a runtime URL to another browser or
  person. The CLI can conservatively classify every provider handoff as
  non-shareable and browser-only.

Until this capture exists, the only candidate handoff is opening the
bill-scoped REG.RU `choose` page in the managed browser. No external payment
URL should be parsed, persisted, printed, or returned.

## Implementation recommendation

Build the billing adapter in three gated layers:

1. **Published REG.API layer:** invoice metadata, line items, documented
   payment-type mutation, and structured API errors.
2. **Private read enrichment layer:** `userBills` through the browser-session
   broker, enabled only with a current semantic probe and the redacted
   identifier/type fixture.
3. **Visible checkout layer:** locate the private row internally and open the
   current `choose` page after confirmation. Add method enumeration or
   provider handoff only after their own capture fixtures.

This meets the product decision to use a browser-backed billing adapter
without pretending that a current cabinet implementation is a public API. It
also leaves a useful fail-closed boundary: REG.API billing continues to work
when the portal build drifts, while enrichment and checkout report a precise
private-contract capability error.

## Primary sources

[account-shell]: https://www.reg.ru/user/account/
[account-index]: https://www.reg.ru/user/account/index.af742acd14730e78f0c8.js
[account-auth]: https://www.reg.ru/user/account/1508.40f765beebd5cfa3df2d.js
[account-gql]: https://www.reg.ru/user/account/4229.37275d176a2e742ac00c.js
[account-bills]: https://www.reg.ru/user/account/4684.7fd0dca789c45f9a8482.js
[account-balance]: https://www.reg.ru/user/account/2442.13eef45f06e5eacfadae.js
[api-bills]: https://www.reg.ru/reseller/api2doc#bill_functions
[api-unpaid]: https://www.reg.ru/reseller/api2doc#bill_get_not_payed
[api-change-pay-type]: https://www.reg.ru/reseller/api2doc#bill_change_pay_type
[help-change]: https://help.reg.ru/support/finansovyye-voprosy/oplata-schetov-i-uslug/kak-izmenit-sposob-oplaty-ili-udalit-schet
[help-methods]: https://help.reg.ru/support/finansovyye-voprosy/oplata-schetov-i-uslug/sposoby-oplaty-kak-vystavit-i-oplatit-schet
