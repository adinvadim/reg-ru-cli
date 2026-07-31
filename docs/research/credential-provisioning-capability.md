# REG.RU credential provisioning and authorization controls

Research date: 2026-07-30

## Scope and evidence standard

This note describes credential **shapes and control-plane behavior only** for
the REG.RU CloudVPS API, REG.API 2, and REG.RU S3-compatible storage. It records
where credentials are provisioned, what the public first-party documentation
says about their authority and lifecycle, and which setup actions a future UI
must treat as creating or materially expanding persistent access.

No account was opened, no credentials were read or entered, no authenticated
request was made, and no provider setting was changed. Where REG.RU does not
publish a lifecycle or scope contract, this note says so rather than importing
behavior from another provider.

## Decision summary

The three products do not share an authentication model and should not be
collapsed into one generic “API key” setup flow:

| Product | Credential and authorization shape | Provisioning location | Published scope and lifecycle |
| --- | --- | --- | --- |
| CloudVPS | One personal bearer token, sent as `Authorization: Bearer <token>` | CloudVPS control panel, **Settings** tab | The token grants all operations on the user's Cloud Servers service. It can be changed in the panel; no expiry, per-token scopes, multiple-token model, or programmatic creation contract is documented. |
| REG.API 2 | `username` plus either `password` or per-request RSA/SHA-512 `sig`; source IP allowlisting is mandatory; an uploaded client certificate may also be required | Alternate API password, allowed IP ranges, and certificates are managed in REG.RU's API/security settings; the main account password is also accepted | Function availability is account-role dependent (client versus partner), not expressed as credential scopes. Public docs do not give expiry or rotation contracts for the alternate password or uploaded certificates. |
| S3-compatible storage | A named key set containing `Access key ID` and `Secret access key`, used with the panel-provided `S3 API Endpoint` | REG.RU Cloud panel: **My resources → S3 storage → Access keys** | The pair authenticates private S3 access. The public guides do not document per-key scopes, expiry, rotation, maximum active key sets, or revocation semantics. Bucket access policy/ACL/public-private state is a separate authorization layer. |

Sources: [CloudVPS authentication][cloudvps-auth],
[REG.API 2 authentication][regapi-auth],
[REG.API IP restrictions][regapi-ip],
[S3 access methods][s3-access], and
[S3 key-set creation example][s3-create].

## CloudVPS bearer token

### Shape and provisioning

CloudVPS documents exactly one authentication mechanism: an opaque token in the
HTTP `Authorization` header using the Bearer scheme. The user can view, copy,
and change their personal token in the CloudVPS panel's **Settings** tab.
([CloudVPS authentication][cloudvps-auth])

The documentation calls it a personal token and warns that it opens access to
**all operations** on the user's Cloud Servers service. No restricted scopes
are described, so a CLI must treat it as a service-wide administrative secret,
not as a read-only or resource-specific key. REG.RU explicitly says to keep it
secret and change it after compromise.
([CloudVPS authentication][cloudvps-auth])

### Published lifecycle and gaps

Changing the token in the panel is the only documented rotation/recovery
operation. The first-party authentication page does not specify token expiry,
format guarantees, multiple concurrent tokens, grace periods, whether a change
immediately invalidates the previous token, or a token-management API. A setup
flow must not promise any of those behaviors.
([CloudVPS authentication][cloudvps-auth])

### Future setup-flow boundary

The following actions create or confer persistent access and therefore require
an explicit human confirmation immediately before execution:

- **Change/rotate the CloudVPS token in the panel.** This generates/replaces a
  service-wide credential and may invalidate existing automation.
- **Persist an existing token in the CLI, keychain, environment manager, or
  another application.** The token already exists, but configuring it for
  unattended reuse grants that destination ongoing CloudVPS access.

Opening the Settings page or reading non-secret token metadata is read-only and
does not itself create access. A UI should mask token material by default and
must never put it in command arguments, logs, diagnostics, issue text, or
analytics; those are client security requirements inferred from the token's
documented all-operations authority.

## REG.API 2 credentials and authorization

### Password mode

Authenticated functions accept `username` plus `password`. The password may be
the user's main REG.RU account password or an alternate API password configured
on **Partner settings** or **API settings** pages. A CLI should prefer the
alternate API password because using the main account password couples
automation to the interactive account credential, although REG.RU does not
publish narrower privileges for the alternate password.
([authentication parameters][regapi-auth-params])

The credential is submitted in HTTPS POST form data. REG.RU marks GET and
query-string parameters as deprecated/disallowed for security reasons and
publishes `HTTPS_ONLY`, `ONLY_POST_ALLOWED`, and `QUERY_PARAMS_DISALLOWED`
errors. A CLI must never place the password or signature in a URL.
([request format and common errors][regapi-request])

### Signature and certificate mode

Signature authentication uses `username` plus `sig`. `sig` is a Base64-encoded
RSA signature over a SHA-512 digest derived from the request parameter values;
the private key must correspond to a certificate previously uploaded in the
account's security settings. The certificate is also presented with the
request. The private key remains a client-side secret; the uploaded artifact is
the corresponding certificate.
([REG.API signature authentication][regapi-auth-params])

Certificate behavior is account-wide and easy to misconfigure:

- Signature authentication always requires the certificate check.
- Password authentication may use the additional certificate check.
- Once **at least one** API SSL certificate is uploaded, a certificate must be
  sent with every authenticated request regardless of whether password or
  signature mode is used.

These rules mean that uploading the first certificate is not merely adding an
optional credential: it changes the acceptance requirements for all existing
authenticated REG.API clients on the account.
([certificate authentication][regapi-cert])

REG.RU does not publish certificate issuer requirements, a maximum certificate
count, expiry handling, overlap/rotation behavior, revocation propagation, or
what happens to existing clients when the last certificate is removed. Those
must remain explicit unknowns in product copy.

### Mandatory source-IP authorization

REG.API is unavailable until at least one source IP address is added in
**API settings**. The current support guide routes users through **API settings
→ IP ranges → Add IP → Save** and says requests then work only from the listed
addresses. REG.RU publishes `ACCESS_DENIED_FROM_IP` for rejected sources.
([REG.API authentication][regapi-auth],
[IP restrictions][regapi-ip])

The public material calls the setting IP addresses/ranges but does not specify
the accepted CIDR syntax, IPv4/IPv6 support, maximum entries, update latency, or
whether changes affect in-flight requests. A setup UI should validate only what
the live control supplies rather than inventing its own range grammar.

### Scope and lifecycle

REG.API's documented authorization boundary is the REG.RU account plus the
function's availability class: some functions are available to ordinary
clients and others only to partners. Passwords, signatures, and certificates
are not documented as holding independent scopes. The source-IP allowlist and
certificate requirement constrain where/how the same account authority can be
used; they do not establish a least-privilege token model.
([function availability and authentication][regapi-reference])

The public reference does not define expiry, rotation, versioning, recovery, or
revocation semantics for the alternate API password or uploaded certificate.
It does document safe non-mutating availability checks (`nop` family) and a
synthetic `test` login/password mode that validates inputs without performing
real operations, but test responses can be stale and do not return real domain
data. These are verification aids, not credential lifecycle mechanisms.
([test and production access][regapi-test])

### Future setup-flow boundary

Explicit human confirmation is required immediately before:

- **Setting or changing the alternate API password.** This creates/replaces a
  reusable account credential.
- **Persisting the main or alternate password in a CLI or another
  application.** This grants that destination ongoing account access.
- **Adding an allowed IP address or range.** Adding the first entry activates
  API use; every additional entry expands the set of network sources from which
  the credentials can be exercised.
- **Uploading/registering an API certificate.** It registers a new persistent
  trust credential, and the first certificate also changes all authenticated
  requests to require certificate presentation.
- **Persisting the certificate's private key or configuring unattended
  certificate use.** This grants the destination ongoing ability to satisfy
  the certificate check and, in signature mode, authorize requests.

A well-designed UI should present the certificate-wide compatibility warning
before the final upload action and identify existing clients that may stop
working. Removing IPs or certificates shrinks access rather than expands it,
but may still warrant a separate destructive/lockout confirmation; that is
outside this note's “create or expand persistent access” criterion.

## S3 access and secret keys

### Shape and provisioning

REG.RU documents a named **key set** with three connection parameters:
`S3 API Endpoint`, `Access key ID`, and `Secret access key`. A current
first-party workflow creates one in the REG.RU Cloud panel under
**My resources → S3 storage → Access keys → Create key set**, asks for a set
name, and then exposes its parameters from the key-set list.
([S3 key-set creation example][s3-create])

For configuration, REG.RU tells AWS CLI users to supply the Access Key ID and
Secret Access Key, leave the region blank, and use the panel-provided endpoint.
The current documented endpoint is `https://s3.regru.cloud`. The endpoint is
connection metadata, not a secret.
([S3 authenticated access][s3-access])

### Scope and lifecycle

The key pair authenticates private access to S3 buckets. Bucket visibility and
authorization remain separately configurable: a bucket can require key
authentication or be public, and policies/ACLs can regulate access. The
first-party key guides do **not** say that an individual key set is bound to one
bucket, carries its own action/resource policy, or has a read-only/full-access
scope. A CLI must therefore avoid both “account-wide administrator” and
“bucket-scoped” claims until REG.RU documents or an authorized characterization
establishes the binding.
([S3 access methods][s3-access],
[bucket access modes][s3-buckets])

The surveyed first-party guides do not document key expiry, scheduled rotation,
re-keying in place, maximum active key sets, deletion/revocation behavior, or
whether the secret is only visible once. In fact, the guides direct users back
to a key set's parameters after creation, which suggests later retrieval in
the current panel, but this is not a durable promise that every secret will
always remain recoverable.
([S3 access methods][s3-access],
[S3 key-set creation example][s3-create])

### Future setup-flow boundary

Explicit human confirmation is required immediately before:

- **Creating a key set.** This generates a new long-lived access/secret pair.
- **Persisting the pair in the CLI, AWS configuration, backup software, a
  plugin, or another application.** The destination receives ongoing access to
  the S3 service.
- **Changing a bucket from private/key-authenticated to public.** This is not a
  credential step, but it materially expands persistent access and is adjacent
  to S3 setup; REG.RU states that public objects can be downloaded by URL
  without authorization.

Reading the endpoint or listing key-set names is read-only. Secret values
should remain masked until deliberately revealed, and logs/diagnostics must
redact both fields—especially the Secret Access Key. This redaction guidance is
a client-side security inference; REG.RU's documented authentication contract
only establishes that both values are used together.

## Cross-product UI requirements

These requirements follow from the credential shapes above:

1. Keep three explicit credential profiles: `cloudvps-bearer`,
   `regapi2-password-or-signature`, and `s3-access-key-pair`. Do not offer a
   generic field called only “API key.”
2. Separate **discover/view** from **create/change/register/persist**. The
   latter boundary is where the UI requests confirmation because it creates or
   materially expands ongoing access.
3. State the destination receiving a credential before confirmation—for
   example, “store in the local OS keychain for unattended `regru` use”—and do
   not silently fall back to plaintext files.
4. Make REG.API's IP allowlist and certificate state first-class preflight
   checks. A correct password alone is insufficient, and adding the first
   certificate can break password clients that do not present it.
5. Treat every lifecycle fact not published by REG.RU as unknown. Do not show
   invented expiry dates, scope selectors, rotation grace periods, or
   revocation guarantees.

The confirmation classifications in this note are UI safety requirements, not
claims made by REG.RU. They apply to future interactive automation; this
research performed none of those actions.

## Primary sources

- [CloudVPS API: authentication][cloudvps-auth]
- [REG.API 2 reference][regapi-reference]
- [REG.API 2: authentication parameters][regapi-auth-params]
- [REG.API 2: authentication][regapi-auth]
- [REG.API 2: certificate authentication][regapi-cert]
- [REG.API 2: test and production access][regapi-test]
- [REG.RU help: REG.API IP and rate restrictions][regapi-ip]
- [REG.RU Cloud: S3 access methods][s3-access]
- [REG.RU Cloud: creating an S3 key set for backup software][s3-create]
- [REG.RU Cloud: ordering and managing S3 buckets][s3-buckets]

[cloudvps-auth]: https://developers.cloudvps.reg.ru/getting-started/authentication.html
[regapi-reference]: https://www.reg.ru/reseller/api2doc
[regapi-auth-params]: https://www.reg.ru/reseller/api2doc#common_auth_params
[regapi-request]: https://www.reg.ru/reseller/api2doc#common_query_format
[regapi-auth]: https://www.reg.ru/reseller/api2doc#common_auth
[regapi-cert]: https://www.reg.ru/reseller/api2doc#common_ssl_auth
[regapi-test]: https://www.reg.ru/reseller/api2doc#common_test_and_prod_access
[regapi-ip]: https://help.reg.ru/support/partneram/reg-api/kakiye-ogranicheniya-yest-pri-rabote-s-reg-api
[s3-access]: https://reg.cloud/support/instrukcii/obektnoe-hranilishe-s3/sposoby-dostupa-k-faylam-v-s3
[s3-create]: https://reg.cloud/support/instrukcii/obektnoe-hranilishe-s3/kak-nastroit-backup-dannyh-s-servera-s-ispmanager-v-hranilishche-s3
[s3-buckets]: https://reg.cloud/support/cloud/obyektnoye-khranilishche-s3/zakaz-i-upravlenie-uslugoj-obektnoe-hranilishche-s3/zakaz-i-upravleniye-obyektnym-khranilishchem-s3
