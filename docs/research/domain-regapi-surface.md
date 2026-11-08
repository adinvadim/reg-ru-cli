# REG.API 2 domain surface for `regru domain …`

Research date: 2026-11-08

## Question and evidence boundary

What does the official REG.API 2 expose for "find a free domain, price it, buy
it from balance, point it at nameservers, and confirm the result", and what
does that mean for a `regru domain` command group built on the existing
`internal/provider/regapi` client?

Primary source is the current REG.API 2 reference
(`https://www.reg.ru/reseller/api2doc`). A plain `curl` of that URL returns a
JavaScript bot-check shell, so the text was retrieved through a rendering
fetcher on 2026-11-08 and read section by section; anchors below point at the
live page. Secondary first-party sources are REG.RU help/marketing pages about
REG.API limits and the 2026 Gosuslugi (ESIA) identification rules. Local
context: `internal/provider/regapi/{client.go,types.go,client_test.go}`,
`docs/research/capability-matrix-regapi-refresh.md`, `README.md`.

No live REG.API call was made. The CLI currently has no read-only domain
command, and the task boundary forbids calling the live API outside an existing
read-only `regru` path (see "Live check" below). No credential was read.

## Headline findings

1. **`domain/check` is partner-only.** The function list and the function page
   both mark it `Партнёры`, as are `domain/get_suggest`,
   `domain/get_premium_prices`, and `domain/get_deleted`. An ordinary client
   account should expect `RESELLER_AUTH_FAILED` ("Only resellers can access to
   this function"). ([function list][function-list], [`domain/check`][check],
   [common errors][errors]) This is the single biggest design constraint: the
   "find a free domain" half of the flow may not be available through REG.API
   for the configured account.
2. **`domain/create` is client-accessible and charges the balance
   synchronously.** Default `pay_type` is `prepay`; with sufficient balance the
   answer carries `bill_id`, `payment`, `pay_type: prepay`,
   `pay_notes: "Amount successfully charged"` and per-domain `service_id`.
   Registration itself is an *application* ("подать заявку"), so the domain
   service exists immediately but becomes active later. ([`domain/create`][create],
   [payment params][payment])
3. **There is a documented client-supplied idempotency handle:** `user_servid`
   (exactly 32 hex chars) on `domain/create`, rejected on reuse with
   `USER_SERVID_IS_NOT_UNIQUE`, and usable afterwards as a service identifier
   in `service/get_info` and friends. Combined with `DOUBLE_ORDER` /
   `DOMAIN_ALREADY_ORDERED`, this gives a real reconcile-before-retry path.
   ([`domain/create`][create], [service identification][service-ident],
   [common errors][errors])
4. **ESIA identification status is exposed:** `user/get_persons` (clients)
   returns every person of the account with `esia_identified`. Domain details
   (`service/get_details`) return `id_state` (e.g. `VERIFIED`) for .RU domains.
   ([`user/get_persons`][persons], [`service/get_details`][details])
5. **ESIA enforcement in the API is only partially documented.**
   `ESIA_IDENTIFICATION_REQUIRED` is listed for `domain/update_nss`,
   `domain/delegate`, `domain/undelegate`; `NOT_ESIA_IDENTIFIED` for
   `domain/set_rereg_bids`. `domain/create` lists no ESIA-specific code. REG.RU
   states it keeps operating under current rules until it is added to the
   registrar list or 18 January 2027. ([`domain/update_nss`][update-nss],
   [REG.RU ESIA page][esia], [partner ESIA guide][partner-esia])

## 1. Availability check, suggestions, premium

### `domain/check` ([reference][check])

- Availability: **partners**. Supports list processing: **yes**.
- Input: either `domain_name` (single) or `domains` — an array of hashes keyed
  `dname` or `domain_name`; the array form is only valid in JSON/XML
  `input_format`. Optional `is_transfer` (0/1, transfer-eligibility instead of
  registration), `premium_as_taken` (0/1, report every premium as taken),
  `currency` (`RUR`, `UAH`, `USD`, `EUR`; default roubles).
- Batch size: the function page states no own limit. The generic
  list-identification rule caps a request at **1,000 services**, and the
  common error `TOO_MANY_OBJECTS_IN_ONE_REQUEST` exists for overflow.
  Applying the 1,000 cap to `check` is an inference.
  ([list identification][list-ident], [common errors][errors])
- Response: `answer.domains[]` with `dname`, `result`, optional `error_code`.
  Availability is the literal `result: "Available"`; anything else carries an
  `error_code` and free-text `result`. Premium names additionally carry
  `is_premium: "1"`, `price` (1-year registration), `renew_price` (1-year
  renewal), `currency` — prices are shown only for *available* premium names.
- Per-item error codes shown in the official example: `DOMAIN_ALREADY_EXISTS`,
  `INVALID_DOMAIN_NAME_PUNYCODE`, `TLD_DISABLED`, `DOMAIN_BAD_NAME`,
  `INVALID_DOMAIN_NAME_FORMAT`, `DOMAIN_INVALID_LENGTH`,
  `HAVE_MIXED_CODETABLES`. The common table adds the rest of the availability
  family the CLI should map: `DOMAIN_BAD_NAME_ONLYDIGITS`, `DOMAIN_BAD_TLD`,
  `DOMAIN_NAME_MUSTBEENG`, `DOMAIN_NAME_MUSTBERUS`, `DOMAIN_STOP_LIST`,
  `DOMAIN_STOP_PATTERN`, `INVALID_PUNYCODE_INPUT`, `CONNECTION_FAILED`
  (registry check failed, retry later), `UNAVAILABLE_DOMAIN_ZONE`.
  ([common errors][errors])
- IDN: the example mixes Unicode (`ййй.me`, `qqйй.com`) and punycode
  (`xn--000.com`) in one request, and returns the `dname` as submitted.
  Service identification generally accepts Russian names "in punycode or in
  the national encoding". ([`domain/check`][check], [service identification][service-ident])
- Important shape detail: per-item failures arrive inside a top-level
  `result: "success"` envelope, so the existing `call()` will return them as
  success; the domain layer must parse each item.

### `domain/get_suggest` ([reference][suggest])

Exists, **partners only**. Inputs `word` (required), `additional_word`,
`category` (`pattern` | `search_trends` | `all`), repeated `tlds` limited to
`com, info, name, net, org, ru, ru.com, su, рф`, `use_hyphen`. Returns
`suggestions[]` of `{name, avail_in[]}`, max 100 entries. Not a substitute for
`check` outside those nine zones.

### Premium

- `domain/check` flags premiums (`is_premium`, `price`, `renew_price`) or
  hides them with `premium_as_taken=1`. ([`domain/check`][check])
- `domain/get_premium_prices` (**partners**): `domains` list of at most 100,
  `currency`; returns `domain_name`, `register_price`, `renew_price`,
  `register_period`, `renew_period`. Only registry-designated premiums are
  priced. ([reference][premium])
- `domain/create` requires `reg_premium=1` to register a premium name; the
  docs warn premium prices can be 10–1000× regular. Without it, the stop-list
  codes `DOMAIN_BAD_NAME` / `DOMAIN_STOP_LIST` describe premiums as
  "offered by special price". ([`domain/create`][create], [common errors][errors])

### Client-accessible substitutes if `check` is unavailable

- The reference itself recommends doing WHOIS lookups directly against
  registry WHOIS servers rather than via REG.API.
  ([recommendations][recommendations]) An RDAP/WHOIS pre-filter is therefore
  the documented path for "is it taken"; it is not authoritative for reserved,
  stop-listed or premium names.
- `domain/nop` with `dname` (availability **all**) returns `service_id` only if
  the domain already exists **in the caller's account** — it is an ownership
  probe, not an availability check. ([`domain/nop`][domain-nop])
- `service/check_create` (availability **all**) validates `domain_name` plus
  optional `servtype`/`subtype`/`period`, but defaults to hosting
  (`srv_hosting_ispmgr`) and documents hosting-specific errors; whether
  `servtype=domain` performs a registration-availability check is not
  documented. ([`service/check_create`][check-create])
- The final authority is `domain/create` itself: a taken name comes back per
  item as `Unavailable domain name`, and `NOT_ENOUGH_MONEY` stops the order
  before a bill exists (unless `ok_if_no_money`). ([`domain/create`][create],
  [payment params][payment])

## 2. Prices

### `domain/get_prices` ([reference][prices])

- Availability: **everyone** except customers on individual "Рег.решения"
  tariff plans; no `username` is required. Authenticating returns the
  caller's (partner) prices instead of retail.
- Inputs: `show_renew_data` (0/1) to include renewal prices,
  `show_update_data` (0/1), `currency` (`RUR` default, `UAH`, `USD`, `EUR`).
- Response: `currency`, `price_group` (tariff plan, e.g. `Retail`),
  `last_update_time`, and `prices` keyed by zone. Per zone:
  `reg_price`, `retail_reg_price`, `renew_price`, `retail_renew_price`,
  `reg_min_period`, `reg_max_period`. Prices are **per year**; some zones have
  a minimum term above one year.
- IDN: if Cyrillic registration in a zone is priced differently from Latin,
  an extra entry with prefix `__idn.` is returned (example `__idn.com`). The
  example keys .РФ as Unicode `рф`, not `xn--p1ai`; a client must normalize
  both forms.
- Promo: there is **no promo/discount field**. Only "your tariff" vs "retail"
  is distinguished.

Reference snapshot, not API data: on 2026-11-08 the public `reg.ru/domain/new`
page showed .ru and .рф at 169 ₽ promo (regular 226 ₽), .su 669 ₽, .com
1,905 ₽, .net 1,913 ₽, .org 1,737 ₽, .online 249 ₽ promo (regular 4,067 ₽),
.site 249 ₽ promo (regular 2,949 ₽); renewal prices were not shown.
([public price page][price-page]) Whether `get_prices` returns the promo or
the regular figure as `reg_price` is unknown.

### `service/get_prices` ([reference][service-prices])

Same audience; returns non-domain service prices as `prices[]` of
`servortld`, `subtype`, `unit`, `price`, plus `show_renew_data` and
`currency`. Not needed for domains.

### Renewal

`service/renew` (clients, list-capable) takes `period`, `pay_type`,
`ok_if_no_money` and returns `period`, `bill_id`, `payment`, `currency`,
`status` (`renew_success` or `only_bill_created`). ([reference][renew])
`domain/get_tld_info` (clients) returns `regperiods`, `renewperiods`, grace
periods, `idn_allowed`, `min/max_dname_length`, `min/max_nss_count`,
`email_verification`. ([reference][tld-info])

## 3. `domain/create` ([reference][create])

Availability: **clients**. List processing (`domains[]` in one request): VIP
clients only.

| Field | Contract |
| --- | --- |
| `domain_name` | Required (or `domains[]`). Zone-dependent charset/length. Unicode **or** punycode accepted. |
| `period` | Required. Zone-dependent; `.ru`/`.su` only `1`. |
| `enduser_ip` | Required for partners; for an ordinary client defaults to the request source IP. |
| `contacts` | Hash of zone-specific contact fields (JSON/XML only). For .RU/.SU/.РФ: person (`person_*`, `person_r_*`, passport, `birth_date`, `p_addr_*`, `phone`, `e_mail`, `country`, optional `code`, `is_entrepreneur`) or organization (`org`, `org_r`, `code`=INN, `kpp`, addresses). |
| `profile_type` + `profile_name` | Use a saved contact profile instead of `contacts`; overrides explicit contacts. Types: `GTLD`, `EU`, `TJ`, `RU.PP`, `RU.ORG`, `NEWTCI.ORG`, `NEWTCI.PP`. Profiles can only be created in the web UI. |
| `nss` (`ns0`..`ns3`, `ns0ip`..`ns3ip`) | At least two nameservers. IPs only for in-bailiwick NS. `ns1.reg.ru`/`ns2.reg.ru` get a free hosted zone. |
| `not_delegated` | .ru/.su/.рф only: ignore NS and register undelegated. |
| `user_servid` | Optional 32-char `[0-9a-f]` caller-defined service ID; not auto-generated. |
| `pay_type`, `ok_if_no_money` | See payment params. |
| `reg_premium` | `1` to acknowledge a premium price. |
| `subtype` | Only `preorder` (for .РФ) documented. |
| `folder_name`/`folder_id`/`no_new_folder`, `comment`, `admin_comment` | Optional. |

Payment ([payment params][payment]): `pay_type` default `prepay`; auto-payment
happens only with `prepay` and enough balance. Without `ok_if_no_money`,
insufficient funds return an error (`NOT_ENOUGH_MONEY`) and **no order is
created**; with it, an unpaid order/bill is created and only executes after
the payment type is changed in the web UI. The create answer documents
`pay_type` as "only prepay possible at the moment".

Response: `bill_id`, `payment` (roubles), `pay_type`, `pay_notes`
(`"Amount successfully charged"` in examples), `domains[]` with `dname`,
`result`, and `service_id` on success. Per-domain `result` values:
`success` ("registration order accepted"), `Invalid TLD`,
`Registraion in this TLD unavailable`, `Invalid punycode input`,
`Domain_name is invalid or unsupported zone`, `Unavailable domain name`.

Async: the operation is an application. The service is created at once
(`service_id` returned) but `service/get_info` documents state `N` as
"inactive (domain not registered / not transferred)" and `A` as active, so
poll for `N → A`. ([`service/get_info`][info]) The reference also recommends
running all ordering/mutating calls through a single-threaded queue and
retrying after connection failures. ([recommendations][recommendations])

Concurrency/duplicate codes relevant to create ([common errors][errors]):
`BILLING_LOCK` (another billing connection is active), `DOUBLE_ORDER`
(unpaid order for this name already exists), `DOMAIN_ALREADY_ORDERED`
(you already ordered it; pay to execute), `DOMAIN_ORDER_LOCKED` (another
operation on the same domain in progress), `ORDER_ALREADY_PAYED`,
`USER_SERVID_IS_NOT_UNIQUE`, `BAD_USER_SERVID`, `PURCHASES_DISABLED`,
`NOT_ALLOWED_ORDER_BY_API`, `DOMAIN_PROTECTION`.

## 4. Nameservers and read-after-write reconciliation

- `domain/update_nss` (clients, list-capable): `domain_name`, `ns0..ns3`,
  `ns0ip..ns3ip` (comma string or array), or `nss` hash in JSON; `undelegate`
  (0/1, partner-only per the purpose line); `payout_agreement` only for paid NS
  changes (`.md`). Answer `domains[]` with `dname`, `result`, `service_id` or
  per-item `error_code`. Errors: `INVALID_NSS`,
  `UNDELEGATION_NOT_SUPPORTED_BY_TLD`, `UNDELEGATION_NOT_SUPPORTED_BY_DOMAIN_STATE`,
  `ESIA_IDENTIFICATION_REQUIRED`. ([reference][update-nss])
- `domain/get_nss` (clients, list-capable): answer `domains[]` with `dname`,
  `service_id`, `nss[]` of `{ns, ip?}`. ([reference][get-nss])
- `service/get_info` (clients, list-capable, ≤1,000 services): per service
  `service_id`, `servtype`, `state` (`N`/`A`/`S`/`D`/`O`), `subtype`,
  `creation_date`, `expiration_date`; identify by `service_id`, `user_servid`,
  or `domain_name`. ([reference][info], [service identification][service-ident])
- `service/get_list` (clients): `servtype=domain` lists `service_id`, `dname`,
  `state`, dates, `uplink_service_id`. ([reference][list])
- `domain/nop` with `dname`: cheap "does this domain exist in my account, and
  what is its `service_id`". ([reference][domain-nop])
- `bill/nop` (already implemented as `Status`) gives the `pay_status` of the
  returned `bill_id`. ([capability matrix][matrix])
- Idempotency: no request-level idempotency header exists. The usable handle
  is `user_servid`: generate it before the first attempt, persist it, and on
  an ambiguous outcome query `service/get_info` by `user_servid` before any
  retry; a retry that reached the server would also fail with
  `USER_SERVID_IS_NOT_UNIQUE`, `DOUBLE_ORDER` or `DOMAIN_ALREADY_ORDERED`.
  That the uniqueness check runs before billing is an inference.

The reference asks integrators to keep domain state locally and avoid polling
`service/get_info`, `domain/get_nss` etc. for display; read-after-write
confirmation is fine, dashboards are not. ([recommendations][recommendations])

## 5. Identification (ESIA/Gosuslugi) and balance

- `user/get_persons` (clients, no inputs): `persons[]` with `person_id`,
  `user_id`, `type` (`pp`/`org`), `is_entrepreneur`, `esia_identified`,
  `person_contacts` (full passport/contact data — must be redacted).
  ([reference][persons])
- `service/get_details` with `show_contacts_only` / `separate_groups` returns
  `id_state` (example `VERIFIED`, group `ru_id`) per .RU domain.
  ([reference][details])
- `user/get_balance` (clients) is already implemented (`prepay`, optional
  `blocked`, partner `credit`). ([reference][balance])
- Create without identification: **not documented** for `domain/create`.
  Neighbouring functions document `ESIA_IDENTIFICATION_REQUIRED` (update_nss,
  delegate, undelegate) and `NOT_ESIA_IDENTIFIED` with a link parameter
  (`set_rereg_bids`, whose contacts "must match contacts already verified via
  ESIA in the account"). ([`update_nss`][update-nss], [rereg bids][rereg])
- REG.RU's partner guide says that from 1 Sep 2026 an unidentified
  administrator cannot register, renew, change DNS, change administrator or
  registrar for .ru/.рф/.su, and that API "update"-type requests will need
  administrator confirmation via an emailed Gosuslugi link, with the operation
  **pending** until confirmed ("details will be published in the
  documentation"). ([partner ESIA guide][partner-esia])
- Current status: REG.RU's ESIA page says 1 Sep 2026 is the law's effective
  date, and that registration, renewal and management continue under current
  accreditation rules until REG.RU is included in the registrar list or until
  **18 January 2027**. ([REG.RU ESIA page][esia])

## 6. Limits, IP allowlist, test mode

- Rate limits: 1,200 requests/hour per account and per IP, both enforced;
  errors `ACCOUNT_EXCEEDED_ALLOWED_CONNECTION_RATE` /
  `IP_EXCEEDED_ALLOWED_CONNECTION_RATE`; a one-hour block, including after 10
  identical invalid requests from one user. No domain-specific limit is
  published. ([recommendations][recommendations], [restrictions][restrictions])
- One billing operation at a time per account (`BILLING_LOCK`).
  ([common errors][errors])
- IP allowlist: API use is impossible with no allowed IP; rejected source is
  `ACCESS_DENIED_FROM_IP`. ([authentication][auth], [restrictions][restrictions])
  `regru` already syncs browser and CLI egress IPv4 into the allowlist after
  `auth login` / `auth refresh` (`README.md`,
  `internal/provider/portal/cdp/regapi_ip_program.go`). `enduser_ip` on create
  defaults to that same source IP for clients.
- Test mode: `username=test`, `password=test`. All input validation runs, a
  response is returned, **no action is taken and no money is charged**;
  responses are illustrative and may not match production; no real domain
  data is returned. ([test and production access][test]) Separately, `nop`
  functions (`domain/nop`, `service/nop`, `bill/nop`…) are the sanctioned
  low-load probes under real credentials.

## 7. Live check

Not run. `regru` has no read-only `domain` command today, and its existing
REG.API reads (`user/get_balance`, `bill/*`) do not exercise the domain
surface; per the task boundary the live API was not called directly. The
first authorized live probe should be a single `domain/check` on one random
name under the real profile, purely to classify the account as partner
(`success`) or client (`RESELLER_AUTH_FAILED`), followed by one
`domain/get_prices?show_renew_data=1`. Do not loop: ten identical invalid
requests trigger a one-hour block.

## Fit with the existing client

`Client.call()` already sends `username`/`password` as form fields with JSON
`input_data`, maps transport/5xx/undecodable answers on mutations to
`AmbiguousMutationError`, and treats `BILLING_LOCK` as retryable. For domains:
per-item outcomes live inside a success envelope and must be validated item by
item (as `ChangePaymentType`/`Delete` already do); `RESELLER_AUTH_FAILED` is
not in `AuthenticationFailure()` and should become a capability state
("partner-only"), not an auth failure; `BILLING_LOCK` on `domain/create` is
safe to retry only after a `user_servid` lookup.

## Capability table

| Function | Read/action | Financial? | Idempotent? | Audience | Evidence | Confidence |
| --- | --- | --- | --- | --- | --- | --- |
| `domain/check` | read | no | yes | partners | [ref][check] | high (contract), medium (1,000 batch cap) |
| `domain/get_suggest` | read | no | yes | partners | [ref][suggest] | high |
| `domain/get_premium_prices` | read | no | yes | partners | [ref][premium] | high |
| `domain/get_prices` | read | no | yes | all (auth → own prices) | [ref][prices] | high; promo semantics unknown |
| `domain/get_tld_info` | read | no | yes | clients | [ref][tld-info] | high |
| `domain/nop` (+`dname`) | read | no | yes | all | [ref][domain-nop] | high |
| `service/check_create` | read | no | yes | all | [ref][check-create] | low for domains |
| `domain/create` | action | **yes**, charges balance (`prepay`) | no; `user_servid` uniqueness + `DOUBLE_ORDER` give reconcile path | clients | [ref][create], [payment][payment], [errors][errors] | high (contract), medium (async/idempotency inference) |
| `domain/update_nss` | action | no (except `.md`) | yes in effect (same NS set) | clients | [ref][update-nss] | high |
| `domain/get_nss` | read | no | yes | clients | [ref][get-nss] | high |
| `service/get_info` | read | no | yes | clients | [ref][info] | high |
| `service/get_list` | read | no | yes | clients | [ref][list] | high |
| `service/get_details` | read (PII) | no | yes | clients | [ref][details] | high |
| `service/renew` | action | **yes** | no | clients | [ref][renew] | high |
| `user/get_persons` | read (PII) | no | yes | clients | [ref][persons] | high |
| `user/get_balance` | read | no | yes | clients | [ref][balance] | high (implemented) |
| `bill/nop` | read | no | yes | clients | [matrix][matrix] | high (implemented) |

## Unknowns

- Whether the configured account is a REG.RU partner; if not, `domain/check`,
  `get_suggest` and `get_premium_prices` are unavailable and availability must
  come from RDAP/WHOIS plus `domain/create`'s own per-item result.
- Exact `domain/check` batch limit and whether `TOO_MANY_OBJECTS_IN_ONE_REQUEST`
  applies at 1,000.
- What `domain/create` returns for .ru/.рф/.su when the admin person/profile is
  not ESIA-identified (error code, pending state, or success), and whether
  that changes when REG.RU is listed or on 18 Jan 2027. Partner guide promises
  documentation that is not yet in the reference.
- How `profile_name`/`profile_type` relate to `user/get_persons` entries
  (`person_id`), i.e. whether an identified person can be referenced directly.
- Whether `get_prices.reg_price` reflects current promos (169 ₽ .ru on the
  site) or the regular price; renewal price for promo zones.
- Whether `user_servid` uniqueness is checked before billing, so a duplicate
  can never double-charge.
- Typical `N → A` latency per zone and whether `service/get_info` exposes a
  failed-registration state beyond `N`/`D`.
- Whether the `test`/`test` account is subject to the IP allowlist and how
  faithfully it emulates partner-only functions.
- Which `ok_if_no_money` path an agent should ever use; recommend never
  (fail closed on `NOT_ENOUGH_MONEY`).

[check]: https://www.reg.ru/reseller/api2doc#domain_check
[suggest]: https://www.reg.ru/reseller/api2doc#domain_get_suggest
[premium]: https://www.reg.ru/reseller/api2doc#domain_get_premium_prices
[prices]: https://www.reg.ru/reseller/api2doc#domain_get_prices
[create]: https://www.reg.ru/reseller/api2doc#domain_create
[update-nss]: https://www.reg.ru/reseller/api2doc#domain_update_nss
[get-nss]: https://www.reg.ru/reseller/api2doc#domain_get_nss
[domain-nop]: https://www.reg.ru/reseller/api2doc#domain_nop
[tld-info]: https://www.reg.ru/reseller/api2doc#get_tld_info
[rereg]: https://www.reg.ru/reseller/api2doc#domain_set_rereg_bids
[service-prices]: https://www.reg.ru/reseller/api2doc#service_get_prices
[check-create]: https://www.reg.ru/reseller/api2doc#service_check_create
[info]: https://www.reg.ru/reseller/api2doc#service_get_info
[list]: https://www.reg.ru/reseller/api2doc#service_get_list
[details]: https://www.reg.ru/reseller/api2doc#service_get_details
[renew]: https://www.reg.ru/reseller/api2doc#service_renew
[persons]: https://www.reg.ru/reseller/api2doc#user_get_persons
[balance]: https://www.reg.ru/reseller/api2doc#user_get_balance
[payment]: https://www.reg.ru/reseller/api2doc#common_payment_params
[service-ident]: https://www.reg.ru/reseller/api2doc#common_service_identification_params
[list-ident]: https://www.reg.ru/reseller/api2doc#common_service_list_identification_params
[errors]: https://www.reg.ru/reseller/api2doc#common_errors
[auth]: https://www.reg.ru/reseller/api2doc#common_auth
[test]: https://www.reg.ru/reseller/api2doc#common_test_and_prod_access
[function-list]: https://www.reg.ru/reseller/api2doc#common_functions_list
[recommendations]: https://www.reg.ru/reseller/api2doc#recommendations
[restrictions]: https://help.reg.ru/support/partneram/reg-api/kakiye-ogranicheniya-yest-pri-rabote-s-reg-api
[partner-esia]: https://help.reg.ru/support/partneram/partnerskaya-programma/gayd-dlya-partnerov-po-esia
[esia]: https://www.reg.ru/esia
[price-page]: https://www.reg.ru/domain/new/
[matrix]: capability-matrix-regapi-refresh.md
