# REG.API 2 billing contract

Research date: 2026-07-30

## Scope and source quality

This report establishes what a CLI can rely on from REG.RU's current first-party
REG.API 2 documentation and its first-party support material. The official
`regru/regru-api-perl` client is used only as corroborating API metadata: the
REG.API documentation itself identifies that repository as an official client
library. No credentials or authenticated requests were used.

The strongest source is the current [REG.API 2 reference][api]. The support
articles describe current cabinet behavior, not an API schema, and are called
out as such. The official Perl client is old and therefore supports absence
claims only weakly; it cannot prove that an undocumented server method does not
exist.

## Bottom line

REG.API 2 documents stable read contracts for account balance and unpaid bills,
plus bill lookup, period history (partners only), payment-type change, and
deletion of unpaid bills. It does **not** document a bill-creation method in the
`bill` namespace: bills are produced by order/renew operations. It also does
**not** document any bill method that returns a REG.RU checkout or external
payment URL. `bill/change_pay_type` can select a payment mechanism, but its
documented response contains payment metadata and status, not a URL.

The safest initial CLI surface is therefore:

- read account balance via `user/get_balance`;
- list unpaid bills via `bill/get_not_payed`;
- inspect known bill IDs via `bill/nop`;
- expose period history only for partner accounts;
- treat payment-type mutation as a separate, explicitly consequential command;
- do not promise an API-generated payment link.

## Documented facts

### Transport and authentication

Function calls use `POST` to
`https://api.reg.ru/api/regru2/<category>/<function>`, with form data. The
reference says GET/query-string transmission is deprecated and its common error
table now includes `ONLY_POST_ALLOWED` and `QUERY_PARAMS_DISALLOWED`, so a new
client should use HTTPS POST form data only.
([request format][request-format], [request errors][errors])

Authenticated functions accept either `username` + `password` or `username` +
`sig`. The password may be the main account password or an alternate API
password. For signature authentication, `sig` is a Base64-encoded RSA signature
using SHA-512. The text to sign is formed from non-zero, non-empty, defined UTF-8
parameter **values** (including `username`), sorted and joined with semicolons;
the private key must correspond to a certificate previously uploaded in the
security settings. The reference's overview once calls the field `signature`,
but both the detailed table and authentication section name the wire field
`sig`; implementations should use `sig`.
([authentication parameters][auth-params], [authentication modes][auth])

If at least one API SSL certificate is configured, the client certificate must
be sent on every authenticated request regardless of password or signature
mode. Signature authentication always requires the certificate check. The
examples use a TLS client certificate and private key.
([authentication parameters][auth-params], [certificate authentication][cert-auth])

An allowed source IP is mandatory: the API reference says that API use is
impossible when no IP has been added, and the current support article says
requests work only from IP ranges configured in API settings. The structured
authorization error for a rejected source is `ACCESS_DENIED_FROM_IP`. The same
support article documents simultaneous limits of 1,200 requests per hour per
account and per IP, with
`ACCOUNT_EXCEEDED_ALLOWED_CONNECTION_RATE` or
`IP_EXCEEDED_ALLOWED_CONNECTION_RATE` on excess.
([authentication][auth], [API restrictions][restrictions], [authorization errors][errors])

### Account balance

`user/get_balance` is available to clients. Its optional `currency` input
defaults to `RUR`; documented values are `RUR`, `USD`, `EUR`, and `UAH`, with
conversion from rubles at the current rate. Its `answer` documents:

- `currency`: the currency used for the values at request time;
- `prepay`: prepaid funds on the account;
- `blocked`: blocked funds, present only when non-zero;
- `credit`: available credit, for partners.

The examples encode monetary values as JSON strings. The reference does not
specify decimal precision, rounding, or a formula for "spendable balance", so a
CLI should preserve the returned strings/decimal values and fields rather than
inventing `prepay - blocked + credit`.
([`user/get_balance`][balance])

### Unpaid bills and history

`bill/get_not_payed` (the spelling is part of the API) is available to clients.
It supports `limit` (default 100, maximum 1,024) and `offset`, and returns
`answer.bills`. Documented bill fields include `bill_id`, `bill_date`,
`currency`, `payment`, `total_payment`, `pay_type`, `pay_status`, and `items`.
`payment` excludes payment-system fees; `total_payment` includes service prices,
transfer fees, and any currency-conversion charges. Items document
`itemtype` (`prepayment` or `service`), optional domain/service identity, and
`action` (new order or renewal). This endpoint's `pay_status` is always
`notpayed`.
([`bill/get_not_payed`][unpaid])

The example also contains `pay_date` and `orig_payment`, but those fields are
not listed in the method's response schema. They should be treated as
opportunistic data, not a stable contract.
([`bill/get_not_payed`][unpaid])

`bill/get_for_period` returns bills between required ISO `start_date` and
`end_date`, with optional `pay_type`, `limit`, `offset`, and `all`. `all`
includes inactive bills whose ordered service expired or whose funds were
returned because the order could not be fulfilled. Crucially, this method is
available to **partners**, not all clients. Its documented payment statuses are:

- `notpayed`: not paid;
- `confirmed`: payment confirmed but funds not yet received, for example a slow
  bank transfer;
- `payed`: paid;
- `cancelled`: payment cancelled by the payment system.

([`bill/get_for_period`][period])

`bill/nop` accepts one `bill_id` or a `bills` list and returns payment status,
which gives clients a documented way to inspect already-known bill IDs.
([bill functions][bill-functions])

### Bill creation and lifecycle

There is no documented `bill/create`. The documented `bill` namespace consists
of `nop`, `get_not_payed`, `get_for_period`, `change_pay_type`, and `delete`;
the official Perl client's `available_methods` contains the same five methods.
Bills instead emerge from ordering and renewal functions, which return a
`bill_id`.
([bill functions][bill-functions], [official Bill client metadata][bill-sdk])

Order/renew functions share payment inputs. `pay_type` defaults to `prepay`.
Automatic payment is documented only for `prepay` when the account has enough
funds. Otherwise, `ok_if_no_money` allows an unpaid bill/order to be created; if
that flag is absent, insufficient funds produce `NOT_ENOUGH_MONEY` and the
request is not created. The reference says the unpaid request then requires a
manual payment-type operation through the web interface. A renewal response can
return `status: only_bill_created` together with a `bill_id` when only the bill
was created.
([common payment parameters][payment-params], [`service/renew`][renew], [common errors][errors])

The status names above describe a vocabulary, not a complete state machine.
The reference does not specify allowed transitions, terminal states, polling
intervals, webhooks, or a completion SLA.

`bill/change_pay_type` accepts a bill ID or list, a required new payment type,
and currency. It says `prepay` pays immediately and that some other types may
issue a bill in the selected payment system. Its response documents only
`bill_id`, `currency`, `payment`, `total_payment`, `pay_type`, and `pay_status`
inside `answer.bills`; no URL field is documented.
([`bill/change_pay_type`][change-pay-type])

`bill/delete` deletes unpaid bills. It returns per-bill `status`: `deleted` for
an unpaid bill removed successfully, or `active` for an already-paid bill that
cannot be removed. Its example shows per-item `BILL_CAN_NOT_REMOVED` and
`BILL_ID_NOT_FOUND` codes while the top-level result remains `success`.
([`bill/delete`][bill-delete])

Current cabinet documentation adds operational context: an unpaid bill can be
manually deleted; a warning is sent after 30 days; and an unpaid order is
automatically deleted after 60 days. This is first-party current product
behavior, but it is not stated as part of the REG.API response contract and
should not be encoded as a guaranteed API TTL.
([change or delete a bill in the cabinet][cabinet-delete])

### Payment types: a documented inconsistency

The API reference is internally inconsistent:

| Context | Documented values |
| --- | --- |
| Common order/renew `pay_type` and bill-list response fields | `bank`, `pbank`, `prepay`, `yacard` |
| `bill/change_pay_type` input | `prepay`, `yamoney`, `bank` |

For `bill/change_pay_type`, `yamoney` is limited to `RUR`; `bank` and `prepay`
accept `RUR` and `USD`. The docs do not explain whether `yacard` and `yamoney`
are aliases, whether `pbank` can be selected by this method, or whether the
method's list is stale.
([common payment parameters][payment-params], [`bill/get_for_period`][period],
[`bill/change_pay_type`][change-pay-type])

The current cabinet supports a much broader set—account balance, cards, bank
transfer, ЮMoney, SberPay, SBP, cash, and Yandex Split—but that support article
does not establish that REG.API accepts those methods.
([current cabinet payment methods][cabinet-payment])

Therefore:

- preserve raw API `pay_type` values;
- validate mutations against the specific method, not the cabinet list;
- do not silently translate `yacard` to `yamoney`;
- regard only `prepay` and `bank` as consistently named across the relevant API
  sections; clarify/test the other values before presenting them as stable CLI
  choices.

### Structured errors

All API responses are standardized around required `result` equal to `success`
or `error`. A success normally carries `answer`. An error carries
`error_code`, `error_text`, and optionally `error_params`. The reference says
`error_code` is for programmatic handling and explicitly warns that
`error_text` can change without notice.
([response envelope][responses])

Bulk operations have two error levels. A top-level request can be `success`
while individual objects contain errors, so clients must inspect every bill
entry for `result` and/or `error_code`, not only the top-level envelope. The
`bill/delete` example is especially important because failed items show
`error_code` without a per-item `result: error`.
([response envelope and nested errors][responses], [`bill/delete`][bill-delete])

Relevant common machine codes include the POST/query errors, authentication
errors (`NO_AUTH`, `PASSWORD_AUTH_FAILED`), access controls
(`ACCESS_DENIED_FROM_IP`, `PURCHASES_DISABLED`, `ACCOUNT_BLOCKED`), input
errors (`PARAMETER_MISSING`, `PARAMETER_INCORRECT`), and billing errors
(`NOT_ENOUGH_MONEY`, `UNSUPPORTED_CURRENCY`). Rate-limit codes are documented
separately in the current restrictions article.
([common errors][errors], [API restrictions][restrictions])

Transport failure and malformed/non-JSON responses should be handled separately
from an API-level `result: error`. The official response wrapper likewise
distinguishes non-200 service failure from decoded API failure, but this is SDK
behavior rather than a current server-side guarantee.
([official response-wrapper metadata][response-sdk])

## External payment URL finding

No documented bill method returns an external payment URL:

1. The current bill category exposes only the five methods listed above.
2. `bill/change_pay_type` has no URL in its documented response.
3. The common payment section says non-prepay orders need manual action in the
   personal cabinet.
4. The official Bill client metadata exposes no checkout/link method.

([bill functions][bill-functions], [`bill/change_pay_type`][change-pay-type],
[common payment parameters][payment-params], [official Bill client metadata][bill-sdk])

`user/get_reseller_url` is not a counterexample. It returns a URL configured by
a **partner** for redirects **from an external service**, and the only
documented service is `srv_seowizard` with `refill`/`refund` URL types. It is
not a REG.RU-generated checkout link for a REG.API bill.
([`user/get_reseller_url`][reseller-url])

The current payment support article describes cabinet-driven payment flows and
a separate unauthenticated "quick renewal" form for a limited set of services.
Neither is a method that turns a REG.API `bill_id` into a documented external
checkout URL.
([current cabinet payment methods][cabinet-payment])

## Gaps and implementation inferences

The following are **not** established by the documented contract:

- an external checkout/payment URL or a deterministic URL template from
  `bill_id`;
- a complete bill-status transition graph, terminality, webhook, or polling
  cadence;
- decimal precision/rounding and a documented formula for spendable balance;
- full bill history for an ordinary (non-partner) client;
- equivalence of `yacard` and `yamoney`, or support for `pbank` in
  `bill/change_pay_type`;
- the example-only `pay_date` and `orig_payment` fields;
- the cabinet's broader payment-method list as valid REG.API `pay_type` input;
- the cabinet's 60-day unpaid-order retention as a guaranteed API TTL.

The recommended CLI design inferred from these facts is to model the API
contract conservatively: raw decimal strings and raw status/type enums,
capability errors for partner-only history, recursive per-item error checking,
and no `payment_url` field until REG.RU publishes one. If a CLI needs to help a
user complete non-prepay payment, it should direct them to the personal cabinet
as guidance, not claim that REG.API produced a checkout link.

## Sources

- [REG.API 2 reference][api]
- [REG.API limitations and IP/rate restrictions][restrictions]
- [Current cabinet payment methods][cabinet-payment]
- [Changing or deleting a bill in the cabinet][cabinet-delete]
- [Official `Regru::API::Bill` source][bill-sdk]
- [Official `Regru::API::Response` source][response-sdk]

[api]: https://www.reg.ru/reseller/api2doc
[request-format]: https://www.reg.ru/reseller/api2doc#common_query_format
[auth-params]: https://www.reg.ru/reseller/api2doc#common_auth_params
[auth]: https://www.reg.ru/reseller/api2doc#common_auth
[cert-auth]: https://www.reg.ru/reseller/api2doc#common_ssl_auth
[responses]: https://www.reg.ru/reseller/api2doc#common_response_parameters
[errors]: https://www.reg.ru/reseller/api2doc#common_errors
[payment-params]: https://www.reg.ru/reseller/api2doc#common_payment_params
[balance]: https://www.reg.ru/reseller/api2doc#user_get_balance
[bill-functions]: https://www.reg.ru/reseller/api2doc#bill_functions
[unpaid]: https://www.reg.ru/reseller/api2doc#bill_get_not_payed
[period]: https://www.reg.ru/reseller/api2doc#bill_get_for_period
[change-pay-type]: https://www.reg.ru/reseller/api2doc#bill_change_pay_type
[bill-delete]: https://www.reg.ru/reseller/api2doc#bill_delete
[renew]: https://www.reg.ru/reseller/api2doc#service_renew
[reseller-url]: https://www.reg.ru/reseller/api2doc#user_get_reseller_url
[restrictions]: https://help.reg.ru/support/partneram/reg-api/kakiye-ogranicheniya-yest-pri-rabote-s-reg-api
[cabinet-payment]: https://help.reg.ru/support/finansovyye-voprosy/oplata-schetov-i-uslug/sposoby-oplaty-kak-vystavit-i-oplatit-schet
[cabinet-delete]: https://help.reg.ru/support/finansovyye-voprosy/oplata-schetov-i-uslug/kak-izmenit-sposob-oplaty-ili-udalit-schet
[bill-sdk]: https://github.com/regru/regru-api-perl/blob/master/lib/Regru/API/Bill.pm
[response-sdk]: https://github.com/regru/regru-api-perl/blob/master/lib/Regru/API/Response.pm
