# REG.API 2 billing implementation contract

Research date: 2026-07-31

## Question and evidence boundary

This note answers the implementation question from
[Research: define the REG.API 2 billing implementation contract][ticket]:
which parts of account balance, billing history, and invoice list, show,
create, and delete are available through the current published REG.API 2
contract, and which require a different backend.

The authoritative source is REG.RU's live [REG.API 2 reference][api], read on
the research date. The page returned HTTP 200 but publishes neither a document
version nor a `Last-Modified` value; its surrounding site shell is dynamic, so
a whole-page hash is not a reproducible contract identifier. The first-party
[`Regru::API::Bill` source][bill-sdk] corroborates the five published `bill`
methods, but the file was last touched on 2014-06-23 and is not authoritative
for current wire behavior. Existing repository research was used only as a
pointer. No credential, account, or authenticated endpoint was used, and no
mutation was attempted.

REG.API 2 is documented by an HTML reference and examples, not a downloadable,
versioned OpenAPI or JSON Schema. Consequently, the request and response models
below distinguish documented fields from example-only observations and avoid
inventing types or guarantees that the reference does not state.

## Resolution

REG.API 2 is sufficient for a conservative billing core, but not for the whole
command surface in [Task: implement billing and invoice commands][billing-task].

| Product operation | Published REG.API 2 contract | Implementation decision |
| --- | --- | --- |
| REG.RU account balance | `user/get_balance`, client-accessible | Implement |
| Unpaid invoice list | `bill/get_not_payed`, client-accessible | Implement |
| Paid/all invoice history | `bill/get_for_period`, **partner-only** | Implement behind a partner capability; it is not ordinary-client history |
| Invoice status by known ID | `bill/nop`, client-accessible | Implement as status lookup only |
| Full invoice show by known ID | No get-by-ID detail method | Use a list result when available or another backend |
| Generic invoice create | No `bill/create` method | Another backend is required; service order/renew side effects are not a generic invoice-create contract |
| Delete unpaid invoice | `bill/delete`, client-accessible, bulk and partially successful | Implement as a consequential mutation |
| Change payment type / pay from account balance | `bill/change_pay_type`; `prepay` may pay immediately | Implement as a separate consequential mutation |
| Discover payment methods available to this account/invoice | No method | Another backend is required |
| Create or obtain checkout/payment URL | No method or response field | Another backend is required |
| Account operation/refill history | No REG.API 2 method | Another backend is required |

The complete published `bill` inventory is `nop`, `get_not_payed`,
`get_for_period`, `change_pay_type`, and `delete`. The current reference and
the first-party Bill client agree on that inventory.
([bill function inventory][bill-functions], [first-party Bill client][bill-sdk])

## Transport contract

Call functions with HTTPS `POST` at:

```text
https://api.reg.ru/api/regru2/<category>/<function>
```

Parameters are form data. The current reference says GET/query-string
transmission is deprecated and discontinued, and publishes
`ONLY_POST_ALLOWED`, `QUERY_PARAMS_DISALLOWED`, and `HTTPS_ONLY`.
([request transport][request-transport], [common errors][common-errors])

For one uniform Go client, send:

```text
Content-Type: application/x-www-form-urlencoded

username=<login>
password=<password>                    # password mode only
sig=<base64-signature>                 # signature mode only
input_format=json
input_data=<JSON object with method parameters>
output_format=json
```

`input_data` carries method parameters serialized according to `input_format`.
Authentication fields must remain outside `input_data`. `output_format=json`
is explicit even though JSON is the default. `output_content_type` changes only
the response media type and is unnecessary for this client.
([management parameters][management-params])

The reference also permits plain form fields instead of `input_data`, but using
one JSON-input path avoids separate encoders for single and bulk bill calls.
The official Perl client uses the same outer form plus JSON `input_data`
pattern for password authentication.
([JSON input example][json-input], [first-party client transport][client-sdk])

Do not set `show_input_params` or `show_sig_params` in production. They are
debug features and can echo sensitive or identifying request values.
([response envelope][response-envelope])

## Authentication and signing

### Password mode

Password mode sends `username` plus `password`; the password may be the main
REG.RU account password or an alternate API password. At least one allowed
source IP must be configured before REG.API can be used.
([authentication parameters][auth-params], [authentication overview][auth])

If at least one API SSL certificate is configured on the account, the client
must present a client certificate on every authenticated request, including
password-mode requests. The public API does not expose a way to query whether
that account setting is enabled.
([client-certificate authentication][cert-auth])

### Signature mode

The wire field is `sig`, despite one overview sentence calling it
`signature`. Signature mode always requires the corresponding TLS client
certificate and private key.
([signature authentication][signature-auth], [authentication parameters][auth-params])

The current first-party algorithm is:

1. Build the semantic request tree before serializing `input_data` to JSON.
   The tree contains outer management/authentication values such as `username`
   and `input_format`, plus the nested method-parameter values.
2. Recursively visit arrays and objects. Object **keys** are not signed.
3. Exclude the `sig` member itself.
4. Exclude zero, the empty string, and undefined/null values.
5. Treat the remaining scalar values as UTF-8 strings, sort all values, and
   join them with `;`.
6. Sign that byte string with RSA/SHA-512, then Base64-encode the signature.
7. Only after signing, JSON-encode `input_data` and submit the form together
   with `sig`.

The reference's complex Perl example implements exactly that recursive
flatten-before-JSON sequence.
([signature algorithm and examples][signature-algorithm])

There is one material documentation defect: the simple prose example says the
canonical input contains two `test` values, but the displayed OpenSSL command
signs a string containing only one. The recursive algorithm and both code
examples include each actual scalar value once and therefore support the
one-`test` command, not the duplicated prose value.
([signature algorithm and examples][signature-algorithm])

The reference does not define cross-language sort collation, numeric
stringification, or a known public key/signature test vector. Implement the
documented algorithm behind its own signer interface and fixture tests, but do
not declare signature authentication production-verified until a separately
authorized contract test succeeds. Password authentication does not depend on
this unresolved canonicalization edge.

## Common response and error contract

Every decoded API response has required `result`, either `success` or `error`.
A successful response normally has `answer`. An API error has `error_code`,
`error_text`, and optional `error_params`. REG.RU explicitly says
`error_text` may change without notice and must not drive program logic.
([response envelope][response-envelope])

Bulk operations have two error levels. The top-level request may be
`result: success` while individual objects fail. Inspect every returned bill
object for `result`, `error_code`, and the operation-specific status fields;
never infer complete success from the top-level envelope.
([nested errors][nested-errors], [`bill/delete`][bill-delete])

The decoder should retain unknown fields and raw provider codes for diagnostics
without placing sensitive values in normal output. It should reject a
top-level success missing the documented operation answer shape as contract
drift. The reference does not publish an HTTP-status mapping for API errors, so
classification must use a decoded `error_code` when present rather than
inventing meaning from HTTP 401/403 alone.

Relevant published common codes include:

| Class | Codes |
| --- | --- |
| Request/transport | `ONLY_POST_ALLOWED`, `QUERY_PARAMS_DISALLOWED`, `HTTPS_ONLY` |
| Authentication/access | `NO_USERNAME`, `NO_AUTH`, `PASSWORD_AUTH_FAILED`, `USER_AUTHENTICATION_FAILED`, `MORE_THAN_ONE_ACCOUNT_WITH_THE_SAME_EMAIL`, `RESELLER_AUTH_FAILED`, `ACCESS_DENIED`, `ACCESS_DENIED_FROM_IP`, `PURCHASES_DISABLED`, `ACCOUNT_BLOCKED` |
| Input | `PARAMETER_MISSING`, `PARAMETER_INCORRECT`, `UNSUPPORTED_CURRENCY` |
| Billing/concurrency | `NOT_ENOUGH_MONEY`, `BILLING_LOCK` |
| Provider availability | `SERVICE_UNAVAILABLE`, `INTERNAL_ERROR` |

([common errors][common-errors])

`bill/delete` additionally exhibits per-item `BILL_CAN_NOT_REMOVED` and
`BILL_ID_NOT_FOUND`, although they are not listed in the common error table.
Treat them as operation-specific provider codes, not as the only possible
per-item errors.
([`bill/delete` example][bill-delete])

## Exact operation models

The reference's examples encode identifiers and money as JSON strings. Its
field tables do not specify a numeric schema, decimal precision, rounding, or
maximum identifier size. Use strings at the provider boundary for
`bill_id`, `service_id`, `payment`, `total_payment`, `prepay`, `blocked`, and
`credit`. Parse monetary values with an arbitrary-precision decimal only for
calculation or validation; preserve the original text for stable output.

### `user/get_balance`

Availability: clients.

Request:

| Field | Required | Contract |
| --- | --- | --- |
| `currency` | No | `RUR`, `USD`, `EUR`, or `UAH`; default `RUR`. Conversion is from rubles at the current rate. |

Response `answer`:

| Field | Presence | Contract |
| --- | --- | --- |
| `currency` | Documented | Currency into which amounts were converted at request time |
| `prepay` | Documented | Prepaid funds on the account |
| `blocked` | Optional | Blocked funds; shown only when non-zero |
| `credit` | Partner-only | Available credit |

([`user/get_balance`][balance])

Do not invent a "spendable" formula from these fields. The reference does not
state whether the correct arithmetic is `prepay - blocked + credit`, nor does
it specify rate timestamp, decimal scale, or rounding. Keep `RUR` exactly as
the provider's enum rather than normalizing it to `RUB`.

### Shared invoice detail shape

`bill/get_not_payed` and `bill/get_for_period` document this bill shape:

| Field | Contract |
| --- | --- |
| `bill_id` | Invoice identifier |
| `bill_date` | Invoice creation date |
| `currency` | `RUR`, `USD`, `EUR`, or `UAH` |
| `payment` | Amount excluding payment-system percentages; the reference describes this field as rubles |
| `total_payment` | Full amount in the selected currency, including services, transfer fees, and conversion charges |
| `pay_type` | Raw payment-type value |
| `pay_status` | Raw payment-status value |
| `items` | Invoice line items |

Documented item fields are:

| Field | Contract |
| --- | --- |
| `itemtype` | `prepayment` or `service` |
| `dname` | Domain name when applicable |
| `servtype` | Service type |
| `service_id` | Service identifier |
| `action` | New order or renewal; no closed wire enum is specified |

([unpaid invoice fields][bill-unpaid], [period invoice fields][bill-period])

The unpaid-list example also contains `pay_date` and `orig_payment`, but neither
is in that method's response-field table. Conversely, `total_payment` is in
the table but absent from the example. Decode these fields when present, but
only the table fields belong in the stable contract; example-only fields must
remain optional extensions.
([`bill/get_not_payed` example][bill-unpaid])

The reference calls `bill_date` a creation date and demonstrates
`1917-10-26`, but does not define its output format, timezone, or time-of-day
precision. Preserve it as provider text and optionally expose a parsed
date-only value only when it exactly matches `YYYY-MM-DD`.

### `bill/get_not_payed`

Availability: clients.

Request:

| Field | Required | Contract |
| --- | --- | --- |
| `limit` | No | Default 100; maximum 1024 |
| `offset` | No | Offset from the initial position |

Response: `answer.bills[]` with the shared invoice shape. This method returns
only `pay_status: notpayed`.
([`bill/get_not_payed`][bill-unpaid])

The reference specifies no total count, ordering, next offset, stable snapshot,
or consistency guarantee while pages are traversed. A page API can safely
return the caller's `offset`, the number received, and a candidate next offset.
An "all pages" convenience is an implementation inference: stop on an empty or
short page, de-duplicate by raw `bill_id`, bound the page count, and do not
promise snapshot consistency.

### `bill/get_for_period`

Availability: **partners**, not ordinary clients.

Request:

| Field | Required | Contract |
| --- | --- | --- |
| `start_date` | Yes | Start date in "ISO format"; example `YYYY-MM-DD` |
| `end_date` | Yes | End date in "ISO format"; example `YYYY-MM-DD` |
| `pay_type` | No | Filter by the list/history payment-type vocabulary |
| `limit` | No | Default 100; maximum 1024 |
| `offset` | No | Offset from the initial position |
| `all` | No | Also include inactive invoices for expired ordered services or orders whose funds were returned because fulfillment failed |

Response: `answer.bills[]` with the shared invoice shape. Documented statuses
are `notpayed`, `confirmed`, `payed`, and `cancelled`.
([`bill/get_for_period`][bill-period])

The reference does not state whether date bounds are inclusive, what timezone
defines a day, the maximum date span, sorting, or page consistency. Send strict
`YYYY-MM-DD` values because that is the only documented example, preserve
returned dates as strings, and do not add unstated inclusive/exclusive
semantics to the CLI contract.

This is invoice history, not account ledger/refill history. An ordinary REG.RU
client has no published full-history method in REG.API 2.

### `bill/nop`

Availability: clients. It accepts either `bill_id` or a `bills` list and
returns `answer.bills[]` entries containing only `bill_id` and `pay_status`.
([`bill/nop`][bill-nop])

This is the only published lookup by known invoice ID, and it is a status
lookup, not invoice show. It does not document amount, currency, date, payment
type, or line items. A full `billing invoice show <id>` therefore cannot be
implemented solely as `bill/nop`; it must use a matching invoice already
obtained from an applicable list or use another backend.

### Related `service/get_bills`

`service/get_bills` is a partner-only method that accepts the standard service
identifiers and returns each requested service with a list of associated bill
IDs. It does not return invoice amounts, dates, statuses, or line items and is
not a generic bill list or get-by-ID detail method. It is useful only for the
partner capability "find bill IDs associated with these known services."
([`service/get_bills`][service-get-bills])

### `bill/change_pay_type`

Availability: clients. This is a consequential bulk mutation.

Request:

| Field | Required | Contract |
| --- | --- | --- |
| `bill_id` or `bills` | Yes | One invoice ID or a list |
| `pay_type` | Yes | `prepay`, `yamoney`, or `bank` |
| `currency` | Yes | `RUR` for `yamoney`; `RUR` or `USD` for `bank` and `prepay` |

The reference says `prepay` pays immediately and that some other methods may
issue an invoice in the selected payment system. The response documents
`answer.bills[]` fields `bill_id`, `currency`, `payment`, `total_payment`,
`pay_type`, and `pay_status`; the example additionally contains
`old_pay_type` and per-item `result`.
([`bill/change_pay_type`][change-pay-type])

The response has no URL, redirect, provider token, QR payload, expiry, or
shareability field. This method must not back a `payment-link` command.

There is a current documentation mismatch:

| Context | Published values |
| --- | --- |
| Common order/renew parameters and bill list/history responses | `bank`, `pbank`, `prepay`, `yacard` |
| `bill/change_pay_type` request | `prepay`, `yamoney`, `bank` |

([common payment parameters][payment-params],
[`bill/change_pay_type`][change-pay-type])

Therefore, preserve all observed list values but validate this mutation only
against its own three-value input contract. Do not silently translate
`yacard` to `yamoney`, and do not send `pbank` to `change_pay_type`. REG.API 2
publishes no method that discovers which types are currently available for a
particular account or invoice.

### `bill/delete`

Availability: clients. It accepts either `bill_id` or `bills` and deletes
unpaid invoices.

The response documents `answer.bills[]` fields `bill_id`, `status`, and
`pay_status`. `status: deleted` means an unpaid invoice was deleted;
`status: active` means an already-paid invoice was not removable. The example
demonstrates all of the following under top-level `result: success`:

- successful deletion with per-item `result: success`;
- an already-paid invoice with `BILL_CAN_NOT_REMOVED`, `pay_status: payed`,
  and `status: active`, but no per-item `result`;
- a missing invoice with `BILL_ID_NOT_FOUND`, but no per-item `result`.

([`bill/delete`][bill-delete])

The implementation must return one outcome per requested ID and define overall
CLI success from all item outcomes, not from the provider's top-level result.
Unknown combinations are contract drift, not success.

## Invoice creation and mutation ambiguity

REG.API 2 publishes no `bill/create`. Bills can be created as side effects of
service order and renewal operations. Common payment parameters default
`pay_type` to `prepay`; when funds are insufficient, `ok_if_no_money` permits
an unpaid request to be created, while omitting it produces
`NOT_ENOUGH_MONEY` and no request. `service/renew` may return
`status: only_bill_created` with a `bill_id`.
([common payment parameters][payment-params], [`service/create`][service-create],
[`service/renew`][service-renew])

Those methods require service-specific order inputs and may activate or renew
services after payment. They are not a generic "create empty invoice" contract.
`billing invoice create` must therefore use a separately characterized portal
operation or be redesigned as a service order/renew command; it must not hide
a service purchase behind an invoice noun.

The published mutation methods have no idempotency key, request ID,
read-after-write token, or delivery-status endpoint. If the connection is lost
after request bytes may have reached REG.RU:

- do not automatically retry `bill/change_pay_type`, especially `prepay`;
- do not automatically retry `bill/delete`;
- do not automatically retry a service order/renew that may have created or
  paid an invoice;
- return `outcome_unknown` and require explicit reconciliation/user action.

This retry policy is an implementation inference from the mutation effects and
the absence of an idempotency contract. `bill/nop` can check a known invoice's
current payment status, but it cannot prove whether a lost delete request
executed: "missing after the call" is indistinguishable from concurrent
deletion or an originally invalid ID. Likewise, unpaid-list absence does not
distinguish deletion from payment or list movement.

`BILLING_LOCK` means another active REG.API connection is performing a billing
operation, but no retry delay or lock lifetime is documented. Surface it as
provider busy; do not spin or replay a mutation automatically.
([common errors][common-errors])

## Backend boundary for the billing commands

The implementation should keep provider domains explicit instead of combining
unrelated balances or histories into one number.

| CLI need | REG.API 2 boundary | Required source |
| --- | --- | --- |
| REG.RU prepaid/blocked/credit balance | Published | `user/get_balance` |
| REG.RU unpaid invoices | Published | `bill/get_not_payed` |
| REG.RU partner invoice history | Published, partner-only | `bill/get_for_period` |
| REG.RU ordinary-client paid history / account operations | Absent | Authenticated portal/private billing adapter |
| Full invoice show by ID | Status only in REG.API | Portal adapter, or a matching documented list record when sufficient |
| Generic invoice create | Absent | Portal adapter or a service-specific order/renew design |
| Current saved/available payment methods | Absent | Portal adapter |
| Runtime checkout/payment link | Absent | Portal adapter/browser handoff |
| CloudVPS balance and refill history | Not REG.API data | CloudVPS REST API |

The current first-party CloudVPS v1 OpenAPI separately publishes
`GET /v1/balance_data` and `GET /v1/billing_history`; the latter is explicitly
refill history. Its retrieved schema hash on the research date was
`f1acbebaee7de77f372826125de175866e60688b1d5ed143c18556f0b1a6d1e3`.
Those results must not be labeled as REG.API account balance or invoice
history.
([CloudVPS v1 OpenAPI][cloudvps-openapi])

## Concrete implementation rules

1. Build a typed REG.API form/RPC transport with a strict top-level envelope
   and operation-specific answer decoders.
2. Model provider IDs, money, dates, enums, and example-only fields as raw
   strings/optional values at the wire boundary.
3. Expose list paging explicitly. Do not claim a stable all-pages snapshot.
4. Keep `invoice status` (`bill/nop`) distinct from full invoice detail.
5. Gate `bill/get_for_period` on partner capability; do not turn
   `RESELLER_AUTH_FAILED` into an empty history.
6. Treat bulk top-level success as transport/command acceptance only. Inspect
   and report every bill item.
7. Require mutation confirmation for delete and payment-type change. Label
   `prepay` as immediate payment from account funds.
8. Never replay a mutation after ambiguous delivery.
9. Preserve raw `pay_type` and `pay_status`; use method-specific validation
   rather than one global enum.
10. Do not expose a REG.API-backed generic invoice create, payment-method list,
    full get-by-ID, ordinary-client paid history, or payment link.

## Remaining uncertainty

Public first-party material is insufficient to settle:

- signature canonicalization for non-ASCII values, numeric representations,
  and sort collation, plus a trustworthy test vector;
- runtime error shapes for rejected signatures or client certificates;
- exact JSON types and decimal precision outside the published examples;
- invoice page ordering, total count, snapshot consistency, and end-of-list
  semantics;
- date-bound inclusivity, timezone, and output `bill_date` format;
- equivalence, migration, or current runtime support of `yacard` versus
  `yamoney`, and whether `pbank` is selectable by any current mutation;
- exact per-item failure shapes for `bill/change_pay_type`;
- whether a transport-lost mutation executed.

These gaps require an explicitly authorized, redacted contract test or the
separate portal-contract research. They do not justify broadening the stable
REG.API model.

## Local context consulted

- [`docs/research/regapi2-billing-contract.md`](regapi2-billing-contract.md)
- [`docs/research/capability-matrix-regapi-refresh.md`](capability-matrix-regapi-refresh.md)
- [`docs/research/billing-private-contract.md`](billing-private-contract.md)
- [`docs/cli-contract.md`](../cli-contract.md)

## Primary sources

- [Current REG.API 2 reference][api]
- [Current REG.API 2 request and authentication contract][request-transport]
- [Current REG.API 2 bill function inventory][bill-functions]
- [First-party `Regru::API::Bill` source][bill-sdk]
- [First-party `Regru::API::Role::Client` transport source][client-sdk]
- [Current CloudVPS v1 OpenAPI][cloudvps-openapi]

[ticket]: https://github.com/adinvadim/reg-ru-cli/issues/23
[billing-task]: https://github.com/adinvadim/reg-ru-cli/issues/6
[api]: https://www.reg.ru/reseller/api2doc
[request-transport]: https://www.reg.ru/reseller/api2doc#common_query_format
[management-params]: https://www.reg.ru/reseller/api2doc#common_api_management_params
[json-input]: https://www.reg.ru/reseller/api2doc#common_params_input_format_json
[auth-params]: https://www.reg.ru/reseller/api2doc#common_auth_params
[auth]: https://www.reg.ru/reseller/api2doc#common_auth
[signature-auth]: https://www.reg.ru/reseller/api2doc#common_sig_auth
[signature-algorithm]: https://www.reg.ru/reseller/api2doc#common_auth_params_sig_auth
[cert-auth]: https://www.reg.ru/reseller/api2doc#common_ssl_auth
[response-envelope]: https://www.reg.ru/reseller/api2doc#common_response_parameters
[nested-errors]: https://www.reg.ru/reseller/api2doc#common_response_parameters
[common-errors]: https://www.reg.ru/reseller/api2doc#common_errors
[payment-params]: https://www.reg.ru/reseller/api2doc#common_payment_params
[balance]: https://www.reg.ru/reseller/api2doc#user_get_balance
[bill-functions]: https://www.reg.ru/reseller/api2doc#bill_functions
[bill-nop]: https://www.reg.ru/reseller/api2doc#bill_nop
[bill-unpaid]: https://www.reg.ru/reseller/api2doc#bill_get_not_payed
[bill-period]: https://www.reg.ru/reseller/api2doc#bill_get_for_period
[change-pay-type]: https://www.reg.ru/reseller/api2doc#bill_change_pay_type
[bill-delete]: https://www.reg.ru/reseller/api2doc#bill_delete
[service-create]: https://www.reg.ru/reseller/api2doc#service_create
[service-renew]: https://www.reg.ru/reseller/api2doc#service_renew
[service-get-bills]: https://www.reg.ru/reseller/api2doc#service_get_bills
[bill-sdk]: https://github.com/regru/regru-api-perl/blob/master/lib/Regru/API/Bill.pm
[client-sdk]: https://github.com/regru/regru-api-perl/blob/master/lib/Regru/API/Role/Client.pm
[cloudvps-openapi]: https://api.cloudvps.reg.ru/v1/openapi.json
