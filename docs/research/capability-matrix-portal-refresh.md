# REG.RU authenticated portal capability matrix refresh

Research date: 2026-07-31

## Scope and evidence boundary

This report refreshes the first-party evidence for the authenticated
REG.RU/REG.Cloud portal boundary needed by
[Research: authenticate and map REG.RU capabilities][ticket-2]. It covers:

- portal session and account/environment switching;
- billing reads and checkout handoff;
- support-ticket operations;
- CloudVPS and S3 credential visibility, including the unknown role boundary.

No authenticated session, secret, cookie, account identifier, or private
response was read. No provider or GitHub state was changed. The current
content-hashed frontend assets cited below were fetched anonymously and still
returned HTTP `200` on the research date.

Evidence is graded as:

- **P — published fact:** customer help or public API documentation;
- **I — implementation fact:** current first-party frontend code, not a
  compatibility promise;
- **X — inference/decision:** the conservative `regru` product consequence;
- **H — human observation required:** per-account runtime behavior cannot be
  established from public material.

## Capability-state model

Contract stability and runtime availability are separate questions. A
capability should carry both dimensions instead of being forced into one
ambiguous enum.

### Contract state

| State | Meaning |
| --- | --- |
| `supported` | REG.RU publishes the user/API workflow and enough of its contract for the proposed use. |
| `experimental-private` | Current first-party code proves that the portal uses the capability, but REG.RU does not publish its authentication, schema, compatibility, or automation contract. It may exist only behind a typed, fail-closed private adapter. |
| `unavailable` | The allowed evidence exposes no callable contract sufficient for the operation. Do not guess routes, IDs, fields, or transitions. |

### Availability state

| State | Meaning |
| --- | --- |
| `available` | The published prerequisite or a runtime probe proves that this selected principal/environment has the capability. |
| `capability-gated` | Availability depends on account type, role, service state, environment, bill state, or provider response. Probe the exact capability and preserve unknown values. |
| `human-observation-required` | Public sources cannot establish the per-account result. A user-authenticated, read-only observation is required before claiming it. |

An operation may therefore be both `experimental-private` and
`capability-gated`, or `supported` as a human workflow while its programmatic
equivalent is `unavailable`.

## Refreshed matrix

| Surface / operation | Evidence | Contract state | Availability state | `regru` boundary |
| --- | --- | --- | --- | --- |
| Interactive portal login | **P:** the cabinet is entered with an account login; the cabinet guide exposes logout. **I:** the account and Cloud clients use credentialed requests to `login.reg.ru`, including authenticate, refresh, and logout flows. ([cabinet guide][cabinet], [account auth client][account-auth], [Cloud auth client][cloud-auth]) | `supported` for provider-hosted human login; the broker protocol is `experimental-private` | `capability-gated` by CAPTCHA, second factor, IP restrictions, and provider session state | Open a dedicated headed browser; never capture the password or export the cookie jar. |
| Reuse of one session across REG.RU origins | **I:** account GraphQL uses credentialed requests and account-specific CSRF at `gql-acc.svc.reg.ru`; Cloud GraphQL separately includes browser credentials. ([account GraphQL client][account-gql], [Cloud GraphQL client][cloud-gql]) | `experimental-private` | `capability-gated` | Preserve the complete isolated browser context; probe refresh immediately before private work. |
| Multiple personal cabinets | **P:** one user may have several personal cabinets and is instructed to inspect each account; the cabinet UI documents logout, not an in-session account selector. ([multiple-cabinet guidance][multiple-cabinets], [cabinet guide][cabinet]) | Account isolation is `supported`; transparent in-session account switching is `unavailable` | `human-observation-required` to map each real account | Use one persistent browser context per REG.RU principal. Switching accounts means switching contexts or explicit logout/login. |
| Cloud environment selection | **I:** the Cloud panel enumerates environment `serviceId` values and adds the selected value as `Service-ID`. ([environment query][cloud-environment], [Cloud GraphQL client][cloud-gql]) | `experimental-private` | `capability-gated` by selected principal and environment | Treat `Service-ID` as an opaque child selector, never as another account credential. Scope caches and credentials to `(portal profile, service ID)`. |
| Account balance and unpaid bills through REG.API 2 | **P:** REG.API documents `user/get_balance` and `bill/get_not_payed`, including bill metadata and pagination. ([REG.API 2][regapi]) | `supported` | `capability-gated` by REG.API credentials, source-IP authorization, and account/API policy | Implement documented reads independently of the portal cookie session. |
| Cabinet bill list/history | **P:** the cabinet guide says the Balance area contains bills and operation history. **I:** current `userBills` GraphQL carries richer private fields including `bill_sid`. ([cabinet guide][cabinet], [Bills chunk][account-bills]) | Human UI is `supported`; machine access is `experimental-private` | `human-observation-required` per account, but not required for the stable billing core | Do not expose `bill_sid`, cabinet-only history, or private GraphQL as stable CLI data. |
| Generic checkout handoff | **P:** REG.RU tells the user to authenticate and continue through **Balance → Bills** to select or change payment method. The help link is the generic cabinet, not a bill-specific URL. ([change payment method][change-bill], [payment flow][payment-flow]) | `supported` | `available` when a browser can reach the cabinet; actual methods remain `capability-gated` | Open only `https://www.reg.ru/user/account/` and explain the remaining navigation. |
| Bill-specific checkout deep link | **P:** documented REG.API bill responses and `bill/change_pay_type` contain no checkout URL. **I:** the current UI constructs private `/billing/payment/*?bill_sid=...` routes. ([REG.API 2][regapi], [account main bundle][account-main]) | Stable external link is `unavailable`; current route is `experimental-private` | Runtime redirect/method behavior is `human-observation-required` with an approved unpaid bill | Do not synthesize, persist, or promise a `bill_id -> checkout URL` mapping. A runtime study would characterize only today's UI, not make it supported. |
| Saved payment methods / method availability | **P:** saved cards and account payment methods exist in the Balance UI. **I:** current private balance code has saved-binding fields and runtime binding URLs, which are a different model from methods available to one bill. ([cabinet guide][cabinet], [Balance chunk][account-balance]) | Human UI is `supported`; private binding schema is `experimental-private` | `capability-gated` and `human-observation-required` | Never infer a bill's available checkout methods from saved bindings. |
| Human support-ticket create/list workflow | **P:** the cabinet stores support requests and can create a new request; authenticated requests do not need the guest email-confirmation flow. ([cabinet guide][cabinet], [support confirmation guide][support-confirmation], [support rules][support-rules]) | `supported` as a human workflow | `capability-gated` by current principal/service ownership | A generic human handoff is safe. This does not authorize or stabilize a CLI ticket API. |
| Support ticket create and temporary upload | **I:** the current support bundle exposes relative, unversioned multipart calls for service discovery, upload/remove, and request creation. It has no published authentication, schema, idempotency, or rate-limit contract. ([support bundle][support-bundle]) | `experimental-private` | `capability-gated`; authenticated mutation behavior is `human-observation-required` | Keep only in the explicitly experimental adapter. Manifest/schema probe first; never blindly retry an ambiguously delivered create/reply. |
| Support list/detail/reply | **P:** the UI has stored requests and human message exchange. **I:** aggregate counts and opaque ticket path locators exist, but the public assets inspected do not establish list, detail, conversation, or reply operation contracts. ([cabinet guide][cabinet], [account support chunk][account-support]) | `unavailable` from public evidence | `human-observation-required` | Do not infer ticket records from counts, use displayed ticket numbers as path IDs, or repurpose create as reply. |
| Support attachments on replies and downloads | No published or current public operation schema establishes reply attachment binding or authenticated download behavior. The create pre-upload `file_id` has unknown scope and lifetime. ([support bundle][support-bundle]) | `unavailable` | `human-observation-required` | Do not construct download URLs or reuse temporary upload handles outside the observed create flow. |
| Support status read/close/reopen | **P:** screenshots/help establish human Open, Closed, and All groupings. No public wire enum or transition graph is published. ([cabinet guide][cabinet]) | Human grouping is `supported`; machine status/lifecycle is `unavailable` | `human-observation-required` | Do not encode UI labels as a stable enum or invent close/reopen operations. |
| Existing CloudVPS token visibility | **P:** the API uses a personal Bearer token available and rotatable in Cloud Settings. **I:** current `environmentInfo` selects `serviceId`, `token`, and `isLocked`. ([CloudVPS authentication][cloudvps-auth], [environment query][cloud-environment]) | Manual retrieval is `supported`; automatic portal retrieval is `experimental-private` | `capability-gated` and `human-observation-required` per principal/environment | Only treat a non-empty, unmasked runtime field as importable. Missing/partial/locked/unauthorized must not trigger rotation. |
| Existing S3 key visibility | **P:** REG.Cloud documents reopening a key set's Parameters and copying endpoint, Access key ID, and Secret access key. **I:** current panel queries select `accessKey` and `secretKey`. ([S3 access guide][s3-access], [S3 operations bundle][s3-ops]) | Manual retrieval is `supported`; automatic portal retrieval is `experimental-private` | `capability-gated` and `human-observation-required` per principal/environment/key set | List metadata first. Import only a complete selected pair after disclosure/persistence confirmation; never reset merely because a field is missing. |
| Role-to-credential visibility rule | **P:** the cabinet acknowledges account profiles, but no first-party source found here maps a role/profile to CloudVPS token or S3 secret visibility. **I:** current clients request the fields without a client-side role predicate; the backend may still authorize or redact them. ([cabinet guide][cabinet], [environment query][cloud-environment], [S3 operations bundle][s3-ops]) | A stable role matrix is `unavailable` | `human-observation-required`; runtime access is `capability-gated` | Detect absent/null/empty/masked/unauthorized fields. Never claim an owner/admin requirement unless REG.RU publishes it or provider-approved test roles prove it. |

## Session and selector state

### Facts

- **P:** REG.RU documents multiple personal cabinets and an explicit logout
  action, but no supported switcher that changes the current principal inside
  one session. ([multiple-cabinet guidance][multiple-cabinets],
  [cabinet guide][cabinet])
- **I:** the current auth helper caches one current `screenName`, refreshes the
  browser session, and emits login/logout changes. The account and Cloud
  frontends use separate browser origins and private backends.
  ([account auth client][account-auth], [Cloud auth client][cloud-auth])
- **I:** a Cloud `serviceId` is added only as the current environment selector
  on Cloud GraphQL requests. It is not used by the account GraphQL client as a
  principal selector. ([Cloud GraphQL client][cloud-gql],
  [account GraphQL client][account-gql])

### Product inference

The safe model is:

```text
PortalProfile
  principal fingerprint
  isolated browser context
  session: unknown | active | reauth-required
  CloudEnvironment[]
    opaque service ID
    environment-scoped capabilities and credentials
```

A profile becomes `active` only after a same-context refresh succeeds and the
principal matches the profile fingerprint. Provider rejection, logout, or
identity mismatch moves it to `reauth-required`. This is a fail-closed product
decision derived from the current cross-origin implementation; REG.RU does not
publish a portable session-lifetime contract.

## Billing conclusion

### Facts

- **P:** REG.API provides bill identifiers and metadata but no checkout URL.
  Its change-payment-type method also returns no redirect metadata.
  ([REG.API 2][regapi])
- **P:** REG.RU's supported existing-bill workflow is an authenticated cabinet
  journey through Balance and Bills. ([change payment method][change-bill])
- **I:** the current cabinet uses private `bill_sid` values and constructs
  session-bound intermediate payment routes. ([Bills chunk][account-bills],
  [account main bundle][account-main])

### Product inference

Mark the generic cabinet handoff `supported`. Mark documented REG.API reads
`supported` but `capability-gated` on separate REG.API authorization. Mark a
stable bill-specific checkout link `unavailable`; the current private route is
implementation evidence only. Human observation with an unpaid bill is needed
only if the project chooses to characterize today's experimental route, not to
justify the safe stable handoff.

## Support conclusion

### Facts

- **P:** REG.RU supports creating and reading requests in the website account
  flow. REG.API 2 publishes no support-ticket family. ([cabinet guide][cabinet],
  [REG.API 2][regapi])
- **I:** current public code reveals the create/pre-upload wrappers and
  aggregate counts, but not stable list, detail, reply, status-transition,
  reply-attachment, or download contracts. ([support bundle][support-bundle],
  [account support chunk][account-support])

### Product inference

The accepted support adapter may be labelled `experimental-private`, but that
label is not permission to pretend unknown operations exist. Every operation
must be independently capability-gated. From public evidence, create/upload
has an observable private shape; list/detail/reply/status/download remain
`unavailable` until human-authenticated observation supplies the missing
runtime shapes. Mutations must still fail closed on drift and ambiguous
delivery.

## Credential-visibility conclusion

### Facts

- **P:** CloudVPS tokens and S3 key pairs are retrievable in their respective
  human panels. ([CloudVPS authentication][cloudvps-auth],
  [S3 access guide][s3-access])
- **I:** the current frontend asks for the corresponding secret-bearing fields
  directly. ([environment query][cloud-environment],
  [S3 operations bundle][s3-ops])
- No first-party source found in this refresh specifies role names,
  inheritance, field masking, nullability, or which account profiles may view
  those values.

### Product inference

Do not encode roles. Model the result per `(portal profile, service ID,
credential object)` as:

```text
available
metadata-only
not-configured
service-locked
unauthorized
unverified
```

Only `available` permits an import offer. Every other state has a non-mutating
fallback: reauthenticate, choose another profile/environment, open the
provider panel, configure an already-held credential, or contact support.
Automatic creation, reset, or rotation is never a read fallback.

## Human-authenticated observation still required

The remaining authorized study should record only non-secret shape and state:

1. For every approved REG.RU account, establish its own isolated browser
   context and verify only a principal fingerprint, never the raw login or
   contract number.
2. Enumerate Cloud environments and record opaque local aliases plus service
   presence; do not publish raw provider IDs.
3. For each environment, record CloudVPS token and S3 key fields only as
   absent, null, empty, masked, or present; never retain values.
4. Record private adapter outcomes as known success type, known provider error
   type, unauthorized, schema drift, or transport ambiguity.
5. Characterize billing checkout only with an explicitly approved unpaid or
   disposable bill, and stop before payment.
6. Characterize support list/detail/reply/status/attachment shapes read-only
   where possible. Any create, reply, upload, close, reopen, or notification
   change remains a separate approved mutation probe.
7. Repeat field-visibility checks with provider-approved roles only if such
   roles exist; do not infer a global role policy from one account.

## Decision for ticket #2

Do **not** close [Research: authenticate and map REG.RU capabilities][ticket-2]
yet. The contract boundary is now sufficiently decided for implementation:
published REG.API reads and generic human handoffs are supported; portal
session brokerage and support creation are experimental/private and
capability-gated; stable checkout deep links and the unobserved support
lifecycle are unavailable. The ticket's remaining work is narrowly the
authorized per-account/per-environment matrix and role-dependent field-shape
observation described above.

## Primary sources

- [REG.RU personal-cabinet guide][cabinet]
- [REG.RU multiple-cabinet guidance][multiple-cabinets]
- [REG.RU support confirmation guide][support-confirmation]
- [REG.RU support-service rules][support-rules]
- [REG.RU existing-bill payment-method workflow][change-bill]
- [REG.RU payment workflow][payment-flow]
- [REG.API 2 reference][regapi]
- [CloudVPS API authentication][cloudvps-auth]
- [REG.Cloud S3 access guide][s3-access]
- [Current account authentication client][account-auth]
- [Current account GraphQL client][account-gql]
- [Current account billing assets][account-bills]
- [Current account support asset][account-support]
- [Current Cloud authentication and GraphQL clients][cloud-auth]
- [Current Cloud environment query][cloud-environment]
- [Current S3 operation bundle][s3-ops]

[ticket-2]: https://github.com/adinvadim/reg-ru-cli/issues/2
[cabinet]: https://help.reg.ru/support/lichnyy-kabinet/registratsiya-i-kontaktnyye-dannyye/znakomstvo-s-lichnym-kabinetom-reg-ru
[multiple-cabinets]: https://help.reg.ru/support/domains/problema-s-domenom/pochemu-moy-domen-nedostupen-v-lichnom-kabinete
[support-confirmation]: https://help.reg.ru/support/lichnyy-kabinet/registratsiya-i-kontaktnyye-dannyye/kak-podtverdit-zayavku-s-kontaktnogo-e-mail-uslugi
[support-rules]: https://img.reg.ru/faq/pravila_obsluguvania_rules-of-service-support_11082025.pdf
[change-bill]: https://help.reg.ru/support/finansovyye-voprosy/oplata-schetov-i-uslug/kak-izmenit-sposob-oplaty-ili-udalit-schet
[payment-flow]: https://help.reg.ru/support/finansovyye-voprosy/oplata-schetov-i-uslug/sposoby-oplaty-kak-vystavit-i-oplatit-schet
[regapi]: https://www.reg.ru/reseller/api2doc
[cloudvps-auth]: https://developers.cloudvps.reg.ru/getting-started/authentication.html
[s3-access]: https://reg.cloud/support/instrukcii/obektnoe-hranilishe-s3/sposoby-dostupa-k-faylam-v-s3
[account-auth]: https://www.reg.ru/user/account/1508.40f765beebd5cfa3df2d.js
[account-gql]: https://www.reg.ru/user/account/4229.37275d176a2e742ac00c.js
[account-main]: https://www.reg.ru/user/account/index.82f5f8db7d99ba5ed418.js
[account-bills]: https://www.reg.ru/user/account/4684.7fd0dca789c45f9a8482.js
[account-balance]: https://www.reg.ru/user/account/2442.13eef45f06e5eacfadae.js
[account-support]: https://www.reg.ru/user/account/1144.0ec38cc04acd1ff94414.js
[support-bundle]: https://help.reg.ru/dist/knowledge-base-main.1d65b0a5ec7920d5b7e6.js
[cloud-auth]: https://cloudvps-static.svc.reg.ru/panel/107.7ba232fd9b902061aea1.js
[cloud-gql]: https://cloudvps-static.svc.reg.ru/panel/__federation_expose_panel.230377f66688d4eeb56c.js
[cloud-environment]: https://cloudvps-static.svc.reg.ru/panel/3539.e74c128bd819e4bb7701.js
[s3-ops]: https://cloudvps-static.svc.reg.ru/s3/584.cc0c4d3c15727ef94da9.js
