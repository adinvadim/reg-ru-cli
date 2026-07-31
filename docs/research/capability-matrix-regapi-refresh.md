# REG.API 2 authentication and read-capability matrix refresh

Research date: 2026-07-31

## Question and evidence boundary

This note establishes the current public REG.API 2 authentication contract,
the source-IP, certificate, and signature requirements, the supported
read-only probes for account balance and unpaid bills, and the states that
[Research: authenticate and map REG.RU capabilities][capability-research]
should record when authorization is absent or misconfigured.

The evidence is limited to REG.RU's current first-party REG.API 2 reference,
current REG.RU help material, and the first-party `regru-api-perl` source.
Existing local research is used only as context. No credential, certificate,
private key, cookie, login, or account identifier was read; no authenticated
or provider API request was made.

## Decision for [Research: authenticate and map REG.RU capabilities][capability-research]

**Decision:** record REG.API authentication and each REG.API capability as two
separate, evidence-bearing states.

- Authentication has one of `not-configured`, `ready-unverified`, `verified`,
  `rejected`, `blocked`, `rate-limited`, `transport-failed`, or
  `provider-error`, plus a normalized `reason` and the unmodified provider
  `error_code` when one exists.
- Each of `balance` and `unpaid-bills` has one of `not-checked`, `available`,
  `auth-blocked`, `temporarily-unavailable`, `contract-error`, or
  `unsupported`.
- Only a successful call to the capability's own documented read method marks
  that capability `available`. Missing credentials or an authorization error
  marks it `auth-blocked`, not `unsupported`. A rate limit or network/provider
  failure leaves it `temporarily-unavailable`, not denied.

This is a product-model inference from the documented distinction between
authenticated client functions, public functions, structured authorization
errors, and transport/API response layers. The state names are not REG.RU
enums. ([function availability][function-access],
[response envelope and errors][errors])

For the authorized characterization in
[Research: authenticate and map REG.RU capabilities][capability-research], use exactly one
`user/get_balance` request and one `bill/get_not_payed` request per profile,
cache their results, and do not retry rejected credentials automatically.
REG.RU documents both a 1,200-request hourly account/IP limit and a separate
one-hour restriction after ten identical invalid requests, so repeated probing
can turn a configuration mistake into a temporary account-wide API outage.
([REG.API restrictions][restrictions])

## Verified facts

### Request transport

REG.API 2 functions are called with HTTPS `POST` at
`https://api.reg.ru/api/regru2/<category>/<function>`. Simple parameters are
sent as `application/x-www-form-urlencoded`; structured parameters may be
serialized into the `input_data` form field. The reference documents
`HTTPS_ONLY`, `ONLY_POST_ALLOWED`, and `QUERY_PARAMS_DISALLOWED`, so the CLI
must not put authentication material in a URL or use GET.
([request transport][request-format], [common errors][errors])

Authentication parameters must remain outside serialized `input_data`: the
reference explicitly says `username` plus `password`/signature are not to be
placed inside `input_data`. ([API management parameters][management-params])

### Mandatory source-IP authorization

At least one source IP must be configured before REG.API can be used; the
reference says API operation is impossible when no IP address has been added.
The current help page says access is possible only from IP addresses listed in
API settings and presents the control as **IP ranges**. A source rejected by
that policy has the structured provider code `ACCESS_DENIED_FROM_IP`.
([authentication overview][auth], [REG.API restrictions][restrictions],
[common errors][errors])

The public material does not define accepted CIDR syntax, IPv4/IPv6 behavior,
maximum entries, or propagation time. Therefore
[Research: authenticate and map REG.RU capabilities][capability-research] may
report an IP allowlist as verified only from an authorized setting observation
or a successful authenticated probe; a locally supplied source IP is merely
`ready-unverified`. This is an inference from the documented requirement and
the absence of a public allowlist introspection contract.
([authentication overview][auth], [REG.API restrictions][restrictions])

### Password authentication

Password mode sends `username` and `password`. The password may be the main
REG.RU account password or an alternate API password configured in Partner/API
settings. ([authentication parameters][auth-params],
[password authentication][password-auth])

The documented structured error for failed password authentication is
`PASSWORD_AUTH_FAILED`. The common table also lists
`USER_AUTHENTICATION_FAILED` for an incorrect login/email or password and
`MORE_THAN_ONE_ACCOUNT_WITH_THE_SAME_EMAIL` when an email maps to multiple
accounts and a login is required. The reference does not state which endpoint
or authentication path chooses between the two invalid-credential codes, so
the CLI must preserve the exact code while normalizing both failed-credential
codes to the same status. ([common errors][errors])

### Signature and client-certificate authentication

Signature mode sends `username` and the wire field `sig`. `sig` is the
Base64-encoded RSA signature using SHA-512 over a semicolon-joined, sorted
array of non-zero, non-empty, defined UTF-8 parameter values, including
`username`. The private RSA key must correspond to a certificate previously
uploaded in the account's security settings. The detailed parameter table and
authentication section consistently use `sig`, even though one overview
sentence calls the alternative field `signature`; `sig` is therefore the
documented wire name. ([signature parameters][signature-params],
[signature authentication][signature-auth])

Signature mode always requires the corresponding client certificate to be
presented with the request. Password mode does not inherently require a client
certificate, but once at least one API SSL certificate has been uploaded, a
certificate must be presented on **every** authenticated request regardless of
whether password or signature mode is used. ([authentication
parameters][auth-params], [client-certificate authentication][certificate-auth])

The reference does **not** publish a signature-specific or
client-certificate-specific structured error code. Its common authorization
table contains `NO_USERNAME`, `NO_AUTH`, `PASSWORD_AUTH_FAILED`,
`RESELLER_AUTH_FAILED`, `ACCESS_DENIED`, `PURCHASES_DISABLED`,
`ACCESS_DENIED_FROM_IP`, `ACCOUNT_BLOCKED`,
`USER_AUTHENTICATION_FAILED`, and
`MORE_THAN_ONE_ACCOUNT_WITH_THE_SAME_EMAIL`, but no
`SIGNATURE_AUTH_FAILED` or `CERTIFICATE_AUTH_FAILED`.
([common errors][errors])

Consequently, a TLS/client-certificate failure may be reported as
`transport-failed` with reason `client-certificate-failed` only when the TLS
layer itself establishes that diagnosis. An API-level rejection in signature
or certificate mode without a documented specific code must be
`rejected/auth-rejected-unclassified`, retaining the raw response; it must not
be relabeled as a bad password. This is a conservative implementation
inference from the documented error vocabulary.

### Function availability

REG.API divides functions into public functions, authenticated functions
available to registered clients, and partner-only functions.
`user/get_balance` and `bill/get_not_payed` are both documented for
**clients**, so an ordinary registered REG.RU account does not need partner
status for these two reads. `bill/get_for_period`, by contrast, is partner-only.
([function availability][function-access], [function list][function-list],
[`user/get_balance`][balance], [`bill/get_not_payed`][unpaid])

The first-party Perl client's method inventory independently includes
`get_balance` under `user` and `get_not_payed` under `bill`. This corroborates
the method names but does not supersede the current reference for authentication
or response behavior. ([first-party User client][user-sdk],
[first-party Bill client][bill-sdk])

## Supported read-only probes

| Purpose | Probe | What success proves | Important non-proof |
| --- | --- | --- | --- |
| Transport only | `user/nop` | The public REG.API function is reachable. It requires no authentication and returns `result: success`. ([`user/nop`][user-nop]) | It does **not** prove that username/password, source IP, signature, or certificate authorization works. |
| General authenticated preflight | top-level `nop` | Client authentication succeeded for that request; the documented response includes the submitted login and a user ID. ([authenticated `nop`][common-nop]) | It does not prove that either billing read method works, and identity fields must not be logged. |
| Balance capability | `user/get_balance` with `currency=RUR` | `result: success` with `answer.currency` and `answer.prepay` proves the `balance` capability. `blocked` is present only when non-zero and `credit` is partner-only, so either field may legitimately be absent. ([`user/get_balance`][balance]) | A missing `blocked` or `credit` field is not a capability or authorization failure. |
| Unpaid-bills capability | `bill/get_not_payed` with `limit=1`, `offset=0` | `result: success` with `answer.bills` proves the `unpaid-bills` capability. The method is client-accessible, read-only, paginated, and permits a limit up to 1,024. ([`bill/get_not_payed`][unpaid]) | An empty `bills` array means “no unpaid bills”, not “capability unsupported”. |
| Known-bill status | `bill/nop` with an existing `bill_id` | Returns payment status for a known bill and is read-only. ([`bill/nop`][bill-nop]) | It is not a discovery/capability probe because it requires a bill identifier and may fail for reasons specific to that bill. |

The two capability methods themselves are the highest-fidelity safe probes.
Running an additional authenticated `nop` is optional and should not be
required when both capability calls will be made anyway. This is an
implementation inference from the documented function contracts.

## Exact status mapping for [Research: authenticate and map REG.RU capabilities][capability-research]

The following mapping is the recommended normalized contract. Provider codes
remain available separately as `provider_error_code`; `error_text` is
diagnostic only because REG.RU explicitly warns that it may change without
notice. ([response envelope and errors][errors])

| Observed evidence | Authentication status / reason | Capability status | Required report behavior |
| --- | --- | --- | --- |
| No local username | `not-configured / missing-username` | `auth-blocked` | Do not call the provider. If observed from the provider, retain `NO_USERNAME`. |
| Username exists but neither password nor usable signature material exists | `not-configured / missing-auth-mechanism` | `auth-blocked` | Do not call the provider. If observed from the provider, retain `NO_AUTH`. |
| Required client-certificate setting is known, but certificate/private-key material is absent | `not-configured / missing-client-certificate` | `auth-blocked` | Do not attempt a known-incomplete TLS/auth request. The normalized reason is inferred; REG.RU publishes no matching error code. |
| Required fields exist but have not been tested from the current source IP | `ready-unverified / none` | `not-checked` | Do not say “configured” or “available”; source-IP and remote certificate state are not proven locally. |
| Capability method returns `result: success` with its documented answer shape | `verified / none` | `available` | Empty unpaid-bill results and optional balance fields remain successful. |
| `NO_USERNAME` | `not-configured / missing-username` | `auth-blocked` | Correct the profile; no automatic retry. |
| `NO_AUTH` | `not-configured / missing-auth-mechanism` | `auth-blocked` | Supply exactly one documented auth mode; no automatic retry. |
| `PASSWORD_AUTH_FAILED` or `USER_AUTHENTICATION_FAILED` | `rejected / credentials-rejected` | `auth-blocked` | Preserve the exact code; do not distinguish “bad login” from “bad password” beyond provider evidence. |
| `MORE_THAN_ONE_ACCOUNT_WITH_THE_SAME_EMAIL` | `rejected / login-ambiguous` | `auth-blocked` | Require the account login instead of email; do not guess which account. |
| `ACCESS_DENIED_FROM_IP` | `rejected / source-ip-denied` | `auth-blocked` | Report the current request source as unauthorized without printing a private account identifier. |
| TLS layer proves missing/rejected client certificate | `transport-failed / client-certificate-failed` | `auth-blocked` | Report TLS evidence; there is no documented REG.API certificate error code. |
| Signature/certificate-mode request is rejected without a documented specific provider code | `rejected / auth-rejected-unclassified` | `auth-blocked` | Retain raw code/status safely; do not call it a password failure. |
| `ACCESS_DENIED` | `blocked / api-access-denied` | `auth-blocked` | Direct the user to REG.RU support, matching the provider description. |
| `ACCOUNT_BLOCKED` | `blocked / account-blocked` | `auth-blocked` | Direct the user to account recovery in the cabinet. |
| `PURCHASES_DISABLED` | `blocked / purchases-disabled` | `auth-blocked` only for the failed probe | Do not infer that all reads are blocked; probe/report each read capability independently. |
| `RESELLER_AUTH_FAILED` | `rejected / partner-required` | `contract-error` for these two probes | Balance and unpaid-bill reads are documented for clients, so this code on either probe is endpoint/profile drift requiring investigation, not evidence that the feature is globally unsupported. |
| `IP_EXCEEDED_ALLOWED_CONNECTION_RATE` | `rate-limited / ip-rate-limited` | `temporarily-unavailable` | Stop probing and retain the provider code. |
| `ACCOUNT_EXCEEDED_ALLOWED_CONNECTION_RATE` | `rate-limited / account-rate-limited` | `temporarily-unavailable` | Stop probing all REG.API capabilities for that account until the restriction clears. |
| Network failure, timeout, non-JSON body, or provider 5xx with no decoded API error | `transport-failed` or `provider-error` | `temporarily-unavailable` | Do not convert reachability into an authorization or support conclusion. |
| `result: success` but the documented answer cannot be decoded | `verified` for the request, with `contract-drift` diagnostic | `contract-error` | Fail closed and preserve a redacted shape diagnostic; do not return partial stable output. |
| A documented function is absent for the account/API family | authentication unchanged | `unsupported` | Reserve this state for affirmative contract/capability evidence, never for bad credentials, denial, rate limiting, or transport failure. |

`RESELLER_AUTH_FAILED`, `ACCESS_DENIED`, `PURCHASES_DISABLED`,
`ACCESS_DENIED_FROM_IP`, `ACCOUNT_BLOCKED`, and the credential-related codes
above come from the current common error table. The two rate-limit codes and
their one-hour window come from the current restrictions article.
([common errors][errors], [REG.API restrictions][restrictions])

REG.API documents an API-level envelope whose required `result` is `success` or
`error`, with `answer` on success and `error_code`/`error_text` on error. It
does not publish an HTTP-status mapping for each authorization failure.
Therefore the CLI should classify a decoded provider error by `error_code`,
not invent semantics from HTTP 401/403 alone. ([response envelope and
errors][errors])

## Verified facts versus inference

Verified:

- HTTPS POST form/RPC transport and the two authentication alternatives;
- mandatory source-IP allowlisting;
- conditional certificate requirement for password mode and unconditional
  certificate requirement for signature mode;
- the `sig` construction algorithm and wire field;
- client availability and response contracts for `user/get_balance` and
  `bill/get_not_payed`;
- the published authorization and rate-limit codes.

Inferred for the CLI:

- the normalized authentication and capability enums;
- probing each capability directly rather than treating a generic no-op as
  sufficient;
- not retrying credential rejection automatically;
- reporting unknown signature/certificate rejection without inventing a
  provider error code;
- treating empty unpaid bills and omitted optional balance fields as successful
  capability evidence;
- reserving `unsupported` for affirmative capability evidence.

The principal remaining unknown is runtime error discrimination for missing,
expired, unmatched, or otherwise rejected client certificates and signatures.
Only an explicitly authorized redacted characterization can establish those
observed shapes; the public contract does not.

## Local context consulted

- [`docs/research/regapi2-billing-contract.md`](regapi2-billing-contract.md)
- [`docs/research/credential-provisioning-capability.md`](credential-provisioning-capability.md)
- [`CONTEXT.md`](../../CONTEXT.md)

## Sources

- [Current REG.API 2 reference][api]
- [Current REG.API restrictions and source-IP setup][restrictions]
- [First-party `Regru::API::User` source][user-sdk]
- [First-party `Regru::API::Bill` source][bill-sdk]

[api]: https://www.reg.ru/reseller/api2doc
[capability-research]: https://github.com/adinvadim/reg-ru-cli/issues/2
[request-format]: https://www.reg.ru/reseller/api2doc#common_query_format
[management-params]: https://www.reg.ru/reseller/api2doc#common_api_management_params
[auth-params]: https://www.reg.ru/reseller/api2doc#common_auth_params
[signature-params]: https://www.reg.ru/reseller/api2doc#common_auth_params_sig_auth
[errors]: https://www.reg.ru/reseller/api2doc#common_errors
[auth]: https://www.reg.ru/reseller/api2doc#common_auth
[password-auth]: https://www.reg.ru/reseller/api2doc#common_password_auth
[signature-auth]: https://www.reg.ru/reseller/api2doc#common_sig_auth
[certificate-auth]: https://www.reg.ru/reseller/api2doc#common_ssl_auth
[function-access]: https://www.reg.ru/reseller/api2doc#common_functions_accessibility
[function-list]: https://www.reg.ru/reseller/api2doc#common_functions_list
[common-nop]: https://www.reg.ru/reseller/api2doc#common_nop
[user-nop]: https://www.reg.ru/reseller/api2doc#user_nop
[balance]: https://www.reg.ru/reseller/api2doc#user_get_balance
[bill-nop]: https://www.reg.ru/reseller/api2doc#bill_nop
[unpaid]: https://www.reg.ru/reseller/api2doc#bill_get_not_payed
[restrictions]: https://help.reg.ru/support/partneram/reg-api/kakiye-ogranicheniya-yest-pri-rabote-s-reg-api
[user-sdk]: https://github.com/regru/regru-api-perl/blob/master/lib/Regru/API/User.pm
[bill-sdk]: https://github.com/regru/regru-api-perl/blob/master/lib/Regru/API/Bill.pm
