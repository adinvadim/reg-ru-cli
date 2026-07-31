# CloudVPS financial history contract

Research date: 2026-07-31

## Scope and source freshness

This report defines the public CloudVPS read contract for account balance and
refill history, and the boundary between those records and REG.API 2 bills. It
uses only first-party sources: the live CloudVPS v1 and v2 OpenAPI documents,
the matching Swagger UI, the published Sphinx documentation and source, and
current REG.Cloud support material. No credentials were used and no mutation
was performed.

The live v1 OpenAPI document was retrieved on 2026-07-31 at 09:18 UTC and had
SHA-256
`f1acbebaee7de77f372826125de175866e60688b1d5ed143c18556f0b1a6d1e3`.
The v2 document was retrieved at 09:22 UTC and had SHA-256
`83271d469c10cc6356c4670e9820c1415d9bd6ac0ecddf71114dc1e9a307ed95`.
The v1 document identifies itself as OpenAPI 3.0.3, `api cloudvps v1`, version
`1.0`. The v2 document contains only `/v2/images` and `/v2/plans`, so all
published CloudVPS financial reads remain in v1.
([v1 OpenAPI][v1-openapi], [v2 OpenAPI][v2-openapi])

The HTML documentation still displays the build label `2021.01.14`, but its
published source and authentication page were last modified on 2026-05-22 when
retrieved. The documentation home explicitly directs readers to the live
Swagger documents for the available operations and wire formats. The current
billing documentation covers prices and balance, but does not contain a
narrative page for refill history; the live OpenAPI is therefore the only
published response schema for `/billing_history`.
([documentation home][docs-home], [billing source index][billing-source],
[balance source][balance-source], [Swagger UI][swagger])

## Conclusion

CloudVPS exposes two distinct, read-only financial views:

- `GET /v1/balance_data` returns a point-in-time Cloud environment balance,
  bonus balance, projected runway, and current resource cost detail.
- `GET /v1/billing_history` returns refill credits only, with the two published
  types `refill` and `refill_bonus`.

Neither endpoint returns a REG.API bill or invoice. Refill records have no bill
ID, payment status, payment method, service items, currency field, stable event
ID, account ID, or link to a REG.API record. They must remain CloudVPS records
even when a refill was operationally funded from the same customer's REG.RU
account. Matching a CloudVPS refill to a REG.API bill by amount and date would
be an unsupported heuristic.
([v1 OpenAPI][v1-openapi], [Cloud balance help][cloud-balance-help])

`GET /v1/history` is not an alternative financial ledger. It is tagged
`History`, describes user/system actions, and returns `action`, arbitrary
`data`, `insert_time`, and `resource_id`, without a monetary amount. Exclude it
from `billing history`.
([v1 OpenAPI][v1-openapi])

## Exact HTTP contract

All three reads below use the origin `https://api.cloudvps.reg.ru`, have no
request body, and require `Authorization: Bearer <token>`. The v1 OpenAPI uses
the relative server base `/v1`.
([authentication][auth], [v1 OpenAPI][v1-openapi])

| Meaning | Request | Query parameters | Published success | Published error |
| --- | --- | --- | --- | --- |
| Cloud balance and current cost detail | `GET /v1/balance_data` | None | `200 application/json` | `401` |
| Cloud refill history | `GET /v1/billing_history` | None | `200 application/json` | `401` |
| Operational audit, excluded from billing | `GET /v1/history` | Optional `limit` `1..50`, optional `offset >= 0` | `200 application/json` | `401` |

The balance documentation's curl example also sends
`Content-Type: application/json`, but a GET has no entity. A client should send
`Accept: application/json` and the bearer header; it need not invent a request
body.
([balance documentation][balance-doc])

### Authentication and account identity

The bearer token is obtained or changed in the CloudVPS environment's
**Settings** tab. REG.RU warns that it grants access to all operations on that
CloudVPS service, so it is a high-value secret.
([authentication][auth], [authentication source][auth-source])

The public responses do not contain a username, REG.RU account ID, Cloud
environment ID, contract number, or token subject. Provider identity is
therefore implicit in the bearer token and cannot be verified from either
response. A normalized record should carry:

- `source = "cloudvps"`;
- `surface = "cloudvps_v1"`;
- the selected local account alias;
- the locally selected Cloud environment context, if one exists;
- `providerAccountId` omitted because the API did not assert one.

The account alias and Cloud environment context are request context, not
provider-returned identity. Do not emit the bearer token, a token hash, or the
opaque credential reference as a substitute for an account ID. A portal
principal and a Cloud environment are also distinct concepts in this
repository; a successful bearer request proves access to the latter, not a
cross-surface identity join.
([authentication][auth], [project domain terminology](../../CONTEXT.md))

## `GET /v1/balance_data`

The response envelope is:

```json
{
  "balance_data": {
    "balance": 2154.55,
    "bonus_balance": 41736.72,
    "days_left": 424,
    "detalization": [],
    "hourly_cost": 4.31128,
    "hours_left": 10180,
    "monthly_cost": 2896.83
  }
}
```

The wire spelling is **`detalization`**. It is not `detail`,
`details`, or `detailization`; the adapter must accept and retain the published
spelling at the transport boundary.
([balance documentation][balance-doc], [v1 OpenAPI][v1-openapi])

### `balance_data` fields

| Wire field | OpenAPI type | Required | Meaning and normalization |
| --- | --- | --- | --- |
| `balance` | number | Yes | Cash balance. Decode as `json.Number`, then an exact decimal string; never through binary floating point. |
| `bonus_balance` | number | Yes | Bonus balance. Keep separate from cash; current product documentation says bonuses cannot be converted into cash. |
| `days_left` | integer | Yes | Provider estimate of remaining days. Preserve; do not derive or promise a billing horizon. |
| `detalization` | array of `BilledResources` | Yes | Current resource cost detail, default `[]`. It is not transaction history. |
| `hourly_cost` | number | Yes | Current aggregate hourly cost. Normalize to an exact decimal string. |
| `hours_left` | integer | Yes | Provider estimate of remaining hours. Preserve independently of `days_left`. |
| `monthly_cost` | number | Yes | Current aggregate monthly cost. Normalize to an exact decimal string. |
| `state` | string | No | Published enum: `active`, `suspended`, `stopped`. Preserve unknown future strings as `unknown` plus their raw value. |

The OpenAPI does not specify minimums, precision, scale, rounding, a formula
for runway, whether bonus funds are included in runway, or behavior when cost
is zero. The client must not recompute `days_left` or `hours_left`, add
`balance` and `bonus_balance`, or infer service health from the numeric fields.
([v1 OpenAPI][v1-openapi], [Cloud balance help][cloud-balance-help])

### `BilledResources`

Each item recursively supports:

| Wire field | OpenAPI type | Required |
| --- | --- | --- |
| `plan` | string | Yes |
| `type` | string | Yes |
| `price` | decimal string | Yes |
| `price_month` | decimal string | Yes |
| `linked` | array of `BilledResources` | No |
| `name` | string | No |
| `resource_id` | integer | No |
| `state` | `active`, `suspended`, or `stopped` | No |

The HTML example shows a server with a linked backup, but neither `type` nor
the recursion depth is exhaustively enumerated. Decode `linked` recursively
with ordinary response-size and nesting limits, retain unknown resource types,
and expose `resource_id` as a string in stable CLI output even though the
current wire type is integer. The `price` fields are already strings; preserve
their decimal value and do not coerce them to the number-typed aggregate cost
fields.
([balance documentation][balance-doc], [v1 OpenAPI][v1-openapi])

The HTML example contains a trailing comma after `monthly_cost`, so it is
illustrative rather than a valid JSON fixture. The live OpenAPI schema owns
machine validation.
([balance source][balance-source], [v1 OpenAPI][v1-openapi])

### Snapshot semantics

The response has no provider timestamp, revision, ETag contract, or statement
that balance and resource detail were calculated atomically. Attach a
client-generated RFC 3339 `observedAt` after the complete response is read.
Label it as observation time, not provider accounting time. A subsequent read
replaces the snapshot; it is not a ledger event.
([v1 OpenAPI][v1-openapi])

## `GET /v1/billing_history`

The success envelope is:

```json
{
  "billing_history": [
    {
      "amount": "100.00",
      "date": "2018-06-18 11:22:09",
      "description_params": {},
      "type": "refill"
    }
  ]
}
```

The OpenAPI operation summary is “Get refill history” and its response
description is “get list of refills”. It publishes no debit, consumption,
invoice, or payment-history record type.
([v1 OpenAPI][v1-openapi], [Swagger UI][swagger])

### Refill fields

| Wire field | OpenAPI type | Published meaning | Normalization |
| --- | --- | --- | --- |
| `amount` | string | Refill amount | Validate as a bounded decimal string and preserve exactly. No sign or scale constraint is published. |
| `date` | string | Example `YYYY-MM-DD HH:MM:SS` | Preserve as `providerDate`; do not convert to UTC because no timezone is specified. |
| `description_params` | nullable object | No property schema | Decode as a bounded opaque object. Do not log it or promote unknown keys into the stable output contract. |
| `type` | string enum | `refill` or `refill_bonus` | Normalize to `cloudvps_refill` or `cloudvps_bonus_refill`; preserve a future unknown raw type rather than dropping a monetary record. |

The OpenAPI schema technically marks neither the envelope property nor the item
fields as required. Operationally, a `200` response without a
`billing_history` array must be `provider_contract_drift`, not an empty
history. Likewise, an item without a valid `amount`, `date`, or `type` cannot
be represented safely: retain no partial monetary claim, fail that source, and
report contract drift. An empty, present array is a successful empty history.
This strictness is a client decision that prevents malformed success from
silently becoming “no refills.”
([v1 OpenAPI][v1-openapi], [CLI contract](../cli-contract.md))

`description_params` is the only potential extension bag, but REG.RU publishes
no keys or semantics for it. Keep it inside the provider adapter if needed for
future redacted fixtures. Stable output may expose only that metadata was
present until a first-party schema establishes safe fields.

## Pagination, ordering, and consistency

`/balance_data` and `/billing_history` declare no query parameters at all.
There is no `limit`, `offset`, page, cursor, total, next link, date filter, or
documented retention window. The `limit` and `offset` components used by
`/history` do not apply to `/billing_history`.
([v1 OpenAPI][v1-openapi])

REG.RU also publishes no guarantee about:

- newest-first or oldest-first ordering;
- stable ordering between identical dates;
- list completeness or maximum retained refills;
- a stable refill identifier or deduplication key;
- consistency between refill history and a simultaneous balance read;
- posting or settlement delay;
- timestamp timezone, daylight-saving behavior, or locale;
- exactly-once appearance of a refill.

The adapter should therefore preserve provider order, attach `observedAt` to
the returned list, and describe the list as a provider snapshot. It must not
claim chronological order, append pages, or persistently deduplicate on
`amount + date + type`. If a cache is introduced, replace the whole snapshot;
do not merge it as an event stream. A sort requested only for presentation
must be explicitly client-side and cannot repair unknown timezone or equal-date
ordering.
([v1 OpenAPI][v1-openapi])

The refill list cannot reconcile the current balance: it contains no resource
charges or withdrawals, while `balance_data.detalization` describes current
costs rather than historical debits.

## Currency and money representation

The two response schemas contain no currency field. First-party CloudVPS price
documentation labels hourly and monthly prices in rubles, and current
REG.Cloud product documentation expresses Cloud balance thresholds and minimum
refills in rubles. The Cloud balance is a distinct cloud-service account:
current help material explicitly distinguishes transfers from the REG.RU
account balance to the cloud balance and says unused cloud funds are returned
to the personal account through support.
([price documentation][prices], [Cloud balance help][cloud-balance-help])

The normalized adapter should assign ISO `RUB` as provider metadata while
recording that the currency was **not wire-reported**:

```json
{
  "amount": "100.00",
  "currency": "RUB",
  "currencySource": "cloudvps_documentation"
}
```

This is stronger and more honest than leaving an amount currency-free, while
still allowing a future wire currency field or documentation change to
override the adapter constant. Do not use REG.API 2's requested/returned
currency to label CloudVPS values.

Every monetary value in stable CLI output must be a decimal string. Use an
exact decimal parser or `math/big`-style representation: `balance_data`
aggregates arrive as JSON numbers, resource prices and refill amounts arrive
as strings, and converting either path through `float64` can change the value.
([v1 OpenAPI][v1-openapi], [CLI contract](../cli-contract.md))

## Errors and retries

Both financial operations publish only:

- `200` with the success schema;
- `401` through the common `UnauthorizedError`, described as a missing or
  invalid access token.

The common OpenAPI `Error` schema requires string `code` and string `message`.
Read-only unauthenticated probes on 2026-07-31 returned HTTP `401` from
`/balance_data`, `/billing_history`, and `/history` with a **numeric** `code`
of `401` and a Russian message saying the authorization token was absent or
invalid. The live response also included `X-Request-ID`, but that header is not
declared in OpenAPI.
([v1 OpenAPI][v1-openapi], [live balance endpoint][live-balance],
[live refill endpoint][live-refills])

Implementation behavior:

1. Treat HTTP status as authoritative; accept provider `code` as string or
   number and normalize it to a string.
2. Map `401` to the existing CloudVPS authentication error and do not retry it.
3. Preserve a bounded provider message internally, but normal output uses the
   stable redacted CLI message. Include `X-Request-ID` only when present and
   safe; it is diagnostic metadata, not a contract requirement.
4. Treat a malformed `200`, a missing success envelope, an unsafe decimal, or
   an unusable refill item as public `provider_contract_drift`.
5. Treat transport failures and undeclared `5xx` separately from API errors.
   These GETs may be retried with bounded exponential backoff and jitter after
   connection failures and transient `5xx`.
6. If an undeclared `429` occurs, honor `Retry-After` when present. Its absence
   from OpenAPI means no quota or retry interval can be promised.
7. Do not retry `4xx` other than an explicitly classified transient response.

The OpenAPI does not document `400`, `403`, `404`, `429`, `5xx`, rate limits,
or `Retry-After` for these operations. The retry policy above is conservative
client behavior, not a provider guarantee.
([v1 OpenAPI][v1-openapi], [CLI contract](../cli-contract.md))

## Normalized boundary with REG.API 2

REG.RU maintains separate financial concepts and surfaces. Current product
documentation confirms that the Cloud balance can be funded from the personal
REG.RU account balance but remains a separate cloud account from which cloud
resource charges are taken.
([Cloud balance help][cloud-balance-help])

| Concept | CloudVPS v1 | REG.API 2 | Normalized ownership |
| --- | --- | --- | --- |
| Balance | Cloud environment cash, bonus, runway, and current costs | REG.RU account `prepay`, optional `blocked`, and partner `credit`, in a requested/returned currency | Separate balance entries with an explicit `source` |
| History | Refill credits only | Bills/invoices and, for partners, period bill history | Separate discriminated record families |
| Identity | Implicit bearer-token Cloud environment | REG.RU API username/principal | Never joined from response data |
| Currency | No wire field; CloudVPS docs establish RUB | Explicit request/response currency | Never copied across surfaces |
| Record ID | None | `bill_id` on bills | Cloud refill has no invoice identifier |
| Status | Refill type only | Bill `pay_status` and per-operation results | Never map refill type to invoice status |
| Items/payment method | None | Bill items and `pay_type` | Portal/REG.API concern, not CloudVPS refill metadata |

The stable domain types should therefore remain discriminated:

- `cloudvps_balance_snapshot`;
- `cloudvps_refill`;
- `cloudvps_bonus_refill`;
- `regapi2_account_balance`;
- `regapi2_invoice`.

Do not create a generic “transaction” that erases source semantics. In
particular:

- a CloudVPS `refill` is not a paid REG.API invoice;
- a CloudVPS `refill_bonus` is not REG.API credit;
- a matching decimal amount and nearby date do not establish causality;
- `description_params` is not a hidden bill-link contract;
- `/v1/history` actions are not billing transactions.

The REG.API details above are established separately in the
[REG.API 2 billing research](regapi2-billing-contract.md); CloudVPS does not
inherit its currency, invoice fields, authentication, or availability.

## Failure behavior across provider surfaces

Source calls are independent and must never substitute for one another.

- A CloudVPS failure must not cause the CLI to display REG.API `prepay` as the
  Cloud balance or REG.API bills as Cloud refill history.
- A REG.API failure must not cause the CLI to present CloudVPS refills as
  invoice history.
- Failure of `/balance_data` does not prove `/billing_history` unavailable,
  and vice versa; aggregate reads may retain the independently successful
  dataset.

For a command that intentionally aggregates multiple configured sources, use a
partial-success result when at least one source succeeds:

```json
{
  "complete": false,
  "sources": [
    { "source": "cloudvps", "status": "ok" },
    {
      "source": "regapi2",
      "status": "unavailable",
      "errorCode": "capability_unavailable"
    }
  ]
}
```

Return a normal success envelope with a structured warning and
`complete: false`; never omit the failed source from `sources`. This preserves
useful data while preventing automation from interpreting it as a complete
account view. If no requested source succeeds, return the ordinary top-level
error envelope. A future source filter must fail normally when that selected
source fails rather than falling back.
([CLI contract](../cli-contract.md))

Authentication failure, missing credentials, network failure, and contract
drift remain distinct per-source states. Do not automatically launch browser
login or use a private portal adapter to disguise failure of these public API
reads.

## Implementation-ready decisions

1. Implement exactly `GET /v1/balance_data` and
   `GET /v1/billing_history` with the existing lazy CloudVPS bearer-token
   source. Do not call `/v1/history` for financial history.
2. Buffer and size-limit each response before rendering. Require the success
   envelope to be present; distinguish a present empty list from a missing
   list.
3. Normalize all money to exact decimal strings plus adapter-assigned `RUB`
   and `currencySource = cloudvps_documentation`.
4. Keep cash and bonus balances separate; do not compute a spendable total or
   recompute runway.
5. Preserve the recursive resource-cost structure and the wire spelling
   `detalization` only inside the transport type; stable output may use the
   correctly spelled domain name `resources`.
6. Preserve refill provider order and raw local date text. Do not manufacture
   UTC, a refill ID, pagination, or a cross-provider bill link.
7. Keep `description_params` opaque and out of normal logs and stable fields
   until REG.RU publishes its schema.
8. Accept string-or-number error codes, treat status as authoritative, and map
   malformed success to public-provider contract drift.
9. Aggregate CloudVPS and REG.API data only as source-discriminated sections.
   Mark partial results explicitly; never use one as fallback data for the
   other.

## Remaining uncertainty

The following facts are not published and cannot be safely resolved without a
new first-party contract or an explicitly authorized, redacted observation:

- the timezone of refill `date`;
- list ordering, retention, completeness, and settlement delay;
- whether refill records can repeat and whether an undisclosed stable ID
  exists;
- the keys and sensitivity of `description_params`;
- exact decimal precision and rounding;
- the runway formula and treatment of bonuses;
- whether `balance_data` and `billing_history` are transactionally consistent;
- a provider-returned Cloud environment or account identifier;
- a provider-supported cross-reference from a refill to a REG.API bill;
- whether every deployed CloudVPS environment is necessarily RUB-denominated
  despite the current first-party documentation.

None of these gaps blocks the conservative read contract above. They do block
UTC conversion, incremental history synchronization, automatic invoice
correlation, arbitrary metadata exposure, and exact balance reconciliation.

## Primary sources

- [CloudVPS v1 OpenAPI][v1-openapi]
- [CloudVPS v1 Swagger UI][swagger]
- [CloudVPS v2 OpenAPI][v2-openapi]
- [CloudVPS developer documentation][docs-home]
- [Authentication][auth]
- [Published authentication source][auth-source]
- [Balance documentation][balance-doc]
- [Published balance source][balance-source]
- [Published billing index source][billing-source]
- [CloudVPS prices][prices]
- [Current REG.Cloud balance help][cloud-balance-help]

[v1-openapi]: https://api.cloudvps.reg.ru/v1/openapi.json
[swagger]: https://api.cloudvps.reg.ru/v1/ui/
[v2-openapi]: https://api.cloudvps.reg.ru/v2/api/swagger.json
[docs-home]: https://developers.cloudvps.reg.ru/
[auth]: https://developers.cloudvps.reg.ru/getting-started/authentication.html
[auth-source]: https://developers.cloudvps.reg.ru/_sources/getting-started/authentication.rst.txt
[billing-source]: https://developers.cloudvps.reg.ru/_sources/billing/index.rst.txt
[balance-doc]: https://developers.cloudvps.reg.ru/billing/balance.html
[balance-source]: https://developers.cloudvps.reg.ru/_sources/billing/balance.rst.txt
[prices]: https://developers.cloudvps.reg.ru/billing/prices.html
[cloud-balance-help]: https://help.reg.ru/support/servery-vps/oblachnyye-servery/zakaz-i-upravleniye-uslugoy-oblachnyye-servery/balans-uslugi-oblachnyye-servery
[live-balance]: https://api.cloudvps.reg.ru/v1/balance_data
[live-refills]: https://api.cloudvps.reg.ru/v1/billing_history
