# Authenticated billing and checkout contract

Research date: 2026-07-30

## Scope and evidence boundary

This report characterizes the current authenticated REG.RU billing surface
without using an account. It relies on the published REG.API 2 reference,
current first-party help articles, and JavaScript served by the public personal
cabinet shell. No authenticated request was made or replayed, and no bill,
payment, payment method, or account state was read or changed.

The evidence has two deliberately separate grades:

- **Published contract** means REG.RU documents the behavior for customers or
  API clients. It is the only grade suitable as a durable `regru` dependency.
- **Private implementation evidence** means the current first-party frontend
  contains the operation, field, or route. It explains today's cabinet but is
  not a compatibility promise.

The inspected account shell and hashed assets were served with `Last-Modified:
Thu, 30 Jul 2026 13:13:26 GMT`. The relevant current-build sources are the
[account shell][account-shell], [main cabinet bundle][account-main],
[GraphQL transport bundle][account-graphql], [Bills route chunk][account-bills],
and [Balance route chunk][account-balance].
The hashes in their filenames make this observation reproducible for this
build, not stable across future builds.

## Finding

REG.RU exposes a useful authenticated billing experience, but no published
authenticated-cabinet API. The stable contract remains REG.API 2 for bill
metadata and supported bill operations, followed by a human handoff to the
generic personal cabinet. The cabinet's GraphQL operations, `bill_sid`, saved
payment-binding schema, and `/billing/payment/*` routes are private,
session-bound implementation details and are not safe foundations for
`regru`.

## Published contract

### Bill reads

`bill/get_not_payed` is documented for client accounts. It supports `limit`
(default 100, maximum 1,024) and `offset`, and returns unpaid bill IDs, dates,
currency, amounts, payment type/status, and line items. That is the published
read contract `regru` can depend on for an ordinary client's unpaid bills.
([REG.API `bill/get_not_payed`][api-unpaid])

REG.API does not publish the richer cabinet list contract described below.
In particular, public API documentation does not promise search-by-text,
arbitrary date-filtered history for every client, `bill_sid`, localized
payment-method titles, or the cabinet's UI state fields. The documented
`bill/get_for_period` history method is partner-only, so `regru` must not
present it as an ordinary-client capability.
([REG.API bill functions][api-bills])

### Changing payment type is not checkout-link creation

`bill/change_pay_type` is a documented client operation. It accepts bill IDs,
currency, and `prepay`, `yamoney`, or `bank`; REG.RU warns that `prepay` pays
immediately. Its documented response contains bill ID, currency, amounts,
payment type, and payment status, but no checkout URL, opaque checkout
identifier, provider token, expiry, or redirect metadata.
([REG.API `bill/change_pay_type`][api-change-pay-type])

The published API is internally inconsistent about payment-type vocabulary:
bill-list/history fields document `prepay`, `bank`, `pbank`, and `yacard`,
while `bill/change_pay_type` accepts `prepay`, `yamoney`, and `bank`. A client
should preserve raw values and validate a mutation against the specific method
instead of treating the cabinet's current payment-method list as an API enum.
([REG.API bill fields and `bill/change_pay_type`][api-change-pay-type])

### The supported human journey

For an existing unpaid bill, REG.RU tells the user to authenticate, open
**Balance → Bills**, choose **Change payment method** from that bill's menu,
and select one of the methods available on the resulting page. The help
article's Balance link is the generic
`https://www.reg.ru/user/account/`; REG.RU does not publish a bill-specific
handoff URL there.
([change an existing bill's payment method][help-change])

The current help article lists cabinet balance, cards, bank transfer, foreign
bank transfer, ЮMoney, SberPay, SBP, cash, and Yandex Split, while explicitly
marking Alfa-Click and WebMoney unavailable. These are product-journey
capabilities, not a machine-readable availability contract: the same article
describes account-, identity-, currency-, amount-, and device-specific
conditions for individual methods.
([current payment methods][help-methods])

The public cart and anonymous quick-renewal flows are separate journeys.
REG.RU documents cart checkout as creating a bill after the user chooses a
method, and says anonymous renewal may have a different price and does not
appear under cabinet **Bills**. Neither flow is a documented way to pay an
existing REG.API `bill_id`.
([bill creation and anonymous renewal][help-create])

## Private implementation evidence

### Session and transport boundary

The current cabinet frontend configures Apollo against
`https://gql-acc.svc.reg.ru/`, sends browser credentials, obtains an
`acc-csrftoken` via `/account/issue_csrf_token`, and mirrors that value in the
`x-acc-csrftoken` header. Operation names are added to the GraphQL URL. This is
strong evidence that the cabinet billing data is browser-session and
CSRF-bound; it is not a published authentication scheme for third-party
clients. ([current GraphQL transport bundle][account-graphql])

Consequently, REG.API credentials are not evidence of authority to call the
cabinet GraphQL service. No first-party document found here specifies a token
exchange between REG.API authentication and the cabinet session, a public
GraphQL endpoint contract, scopes, rate limits, error semantics, or lifecycle
guarantees.

### Bill list and “detail” shape

The current Bills route issues this private GraphQL query:

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
      is_download_garant_letter
    }
  }
}
```

The UI requests 50 rows at a time, supports free-text search and a start/finish
date filter, and renders each row directly from those fields. No separate
bill-detail GraphQL query appears in this Bills route chunk; the displayed
description is part of the list item. This describes the current UI, not a
schema guarantee or proof that no other private detail operation exists.
([current Bills route chunk][account-bills])

The cabinet model contains both the displayed numeric `id` and a distinct
`bill_sid`. REG.API documents `bill_id` but not `bill_sid`, so `regru` must not
derive one from the other, persist `bill_sid` as a durable public identifier,
or accept it as a supported user-facing input.
([current Bills route chunk][account-bills],
[REG.API unpaid-bill fields][api-unpaid])

### Observable checkout initiation boundary

The current bill-row component constructs:

- `/billing/payment/order?bill_sid=<value>` for a payable bill;
- `/billing/payment/choose?bill_sid=<value>` for a cancelled bill and for the
  explicit **Change payment method** action;
- `/bill/form?bill_sid=<value>&pdf=1` for a bill/receipt download.

It disables row navigation for `pay_status: onhold`, regards all non-paid and
non-on-hold rows as payable, and contains special download handling for
`bank`, `pbank`, and qualifying `credit` bills. These conditions are useful
for understanding the current frontend, but the route parameters, allowed
state transitions, response behavior, replay/share semantics, and URL lifetime
are undocumented. ([current main cabinet bundle][account-main])

This is the observable browser boundary at which the UI begins checkout. It
does **not** establish a stable external payment URL: the route is built from
private `bill_sid`, depends on a cabinet session, and is an intermediate REG.RU
page rather than a documented provider redirect.

### Payment-method metadata

The private bill list carries only the raw `pay_type` and server-provided
`pay_type_title`; it does not return an availability list or provider checkout
metadata. Current frontend constants additionally recognize `bank`, `pbank`,
and `credit` for row behavior, but those constants are not a complete payment
method registry. ([current main cabinet bundle][account-main],
[current Bills route chunk][account-bills])

The Balance route has a separate private `userBalanceInit` query for saved
payment bindings. Its fields include binding state, currency, autorefill and
autorenew flags, expiry, priority, settings, `allowedBindingTypes`, and a
ЮMoney binding configuration. The same chunk contains mutations to add and
reorder bindings, including runtime `binding_url`/`bind_pay_url` values. This
schema concerns persistent saved payment methods and one-click/autorenew
features, not the methods currently available for paying a particular bill;
it must not be reused as bill-checkout metadata.
([current Balance route chunk][account-balance])

## Stability decision

| Capability | Evidence | Decision for `regru` |
| --- | --- | --- |
| List unpaid bills | Published REG.API 2 | Implement |
| Inspect documented bill fields/status | Published REG.API 2 | Implement |
| Partner-only period history | Published, role-gated | Capability-gate |
| Change payment type | Published, consequential; `prepay` can charge immediately | Separate explicit command, never implicit |
| Open generic cabinet | Published help journey | Implement as human handoff |
| Search/date-filter all cabinet bills | Private GraphQL | Do not implement |
| Use `bill_sid` | Private frontend field | Do not expose or persist |
| Deep-link to `/billing/payment/*` | Private frontend route | Do not promise or construct |
| Enumerate checkout methods from saved bindings | Private, different domain model | Do not implement |
| Replay cabinet GraphQL/BFF calls | Private session/CSRF mechanism | Do not implement |

## Concrete recommendation

Implement the billing boundary as **documented data plus generic handoff**:

1. Read and display REG.API bill IDs, amounts, line items, raw payment type,
   and payment status.
2. Provide an `open`/handoff action that opens only
   `https://www.reg.ru/user/account/` and tells the user to continue through
   **Balance → Bills**.
3. Do not place `bill_id` or observed `bill_sid` in the handoff URL, and do not
   claim that the generic page will preselect a bill.
4. Keep `bill/change_pay_type` outside the handoff path. If added, make it an
   explicitly consequential command with a preview and special warning for
   `prepay`, because the published contract says it can pay immediately.
5. Do not ship a cabinet GraphQL client, payment-binding client, checkout deep
   link, or provider URL parser. Reconsider only if REG.RU publishes a
   supported contract or gives an explicit integration commitment.

This keeps `regru` on the part REG.RU promises while still giving users a short,
accurate route to complete payment.

[account-shell]: https://www.reg.ru/user/account/
[account-main]: https://www.reg.ru/user/account/index.82f5f8db7d99ba5ed418.js
[account-graphql]: https://www.reg.ru/user/account/4229.37275d176a2e742ac00c.js
[account-bills]: https://www.reg.ru/user/account/4684.7fd0dca789c45f9a8482.js
[account-balance]: https://www.reg.ru/user/account/2442.13eef45f06e5eacfadae.js
[api-bills]: https://www.reg.ru/reseller/api2doc#bill_functions
[api-unpaid]: https://www.reg.ru/reseller/api2doc#bill_get_not_payed
[api-change-pay-type]: https://www.reg.ru/reseller/api2doc#bill_change_pay_type
[help-change]: https://help.reg.ru/support/finansovyye-voprosy/oplata-schetov-i-uslug/kak-izmenit-sposob-oplaty-ili-udalit-schet
[help-methods]: https://help.reg.ru/support/finansovyye-voprosy/oplata-schetov-i-uslug/sposoby-oplaty-kak-vystavit-i-oplatit-schet#kak-oplatit-schet
[help-create]: https://help.reg.ru/support/finansovyye-voprosy/oplata-schetov-i-uslug/sposoby-oplaty-kak-vystavit-i-oplatit-schet
