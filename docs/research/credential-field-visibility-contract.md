# REG.RU credential-field visibility contract

Research date: 2026-07-30

## Scope and evidence boundary

This note answers when one-login bootstrap can safely claim that an existing
CloudVPS token or S3 key pair is readable. It uses only current first-party
REG.RU documentation and JavaScript served anonymously by REG.RU. No account
was authenticated, no private response or credential value was read, and no
credential or provider state was created, revealed, rotated, reset, revoked,
or changed.

Published documentation is treated as the product contract. Content-hashed
frontend bundles are current implementation evidence: they identify the
fields, result variants, and UI gates the provider handles today, but they are
not a versioned public API.

## Decision

**REG.RU's public material does not establish a role-to-credential-visibility
matrix.** The current Cloud frontend does not expose a named role or permission
predicate around either credential read. Instead, its private GraphQL
operations return the requested object, `Unauthorized`, or a product-state
not-found result. A frontend field selection proves that the panel requests the
field; it does not prove that every account role receives a non-empty,
unmasked value.

`regru` must therefore capability-detect each selected Cloud environment at
runtime and fail closed. It must not infer visibility from account type,
ownership type, login success, menu visibility, or the existence of a Cloud
environment alone.

Use these credential capability states:

| State | Observable condition | `regru` decision |
| --- | --- | --- |
| `available` | The expected success object is returned and every required credential field is a non-empty value. | Offer import only after explicit confirmation of disclosure and persistence destination. |
| `metadata-only` | The service/key object exists, but a required secret is absent, null, empty, masked, or otherwise not usable as a credential. | Show non-secret metadata only. Offer manual import of an already-held credential or open the provider panel. Never rotate/reset to fill the gap. |
| `not-configured` | Cloud environment is absent, S3 object storage is absent, or the S3 key-pair list is empty. | Explain the missing provider prerequisite. Open the relevant provider page; credential creation remains a separately confirmed mutation, not an automatic fallback. |
| `service-locked` | The environment or object store reports a lock/suspension state. | Report the provider state separately from credential visibility. Do not create, rotate, reset, or test the credential. Direct the user to the panel/billing/support path that can resolve the lock. |
| `unauthorized` | A credential-discovery operation returns `Unauthorized`. | Refresh/re-authenticate the same isolated portal profile once. If it persists, report that the session or current principal is not authorized; do not guess which role is required. |
| `unverified` | Transport/schema error, unexpected result type, partial object, or incompatible/masked field shape. | Report a private-adapter compatibility failure and use the manual-panel/configure-existing-credential fallback. |

Credential readability and service usability are independent. For example, a
locked environment may still return a token in the current implementation, but
the token may not authorize useful service operations. `regru` should retain
both dimensions rather than converting a lock into “credential missing” or a
returned secret into “service ready.”

## Explicit first-party evidence

### CloudVPS token

REG.RU's published CloudVPS API documentation says the personal token can be
viewed, changed, and copied in the panel's **Settings** tab. It also says the
token grants access to every operation on the user's Cloud Servers service.
The documentation does not qualify this statement by account role or publish a
role/permission table for viewing the token.
([CloudVPS authentication documentation][cloudvps-auth])

The current public panel query `environmentInfo` requests `isLocked`,
`serviceId`, and `token` together on `EnvironmentInfo`. Its only declared
top-level alternatives are `Unauthorized` and `EnvironmentNotFound`; there is
no `Forbidden`, `CredentialHidden`, masked-token object, role enum, or
permission object in that operation.
([current environment query bundle][panel-environment])

The separate `environments` discovery query returns environment `serviceId`
values, or `Unauthorized` / `EnvironmentNotFound`. Thus a portal login alone
does not prove that a Cloud environment exists, and an environment ID is only
a selector—not evidence that its token is readable.
([current core operation bundle][panel-core])

In the current app initialization code, a successful `EnvironmentInfo` object
is copied into the selected-environment store and its `token` is assigned to
the API-token store without a client-side role check. The Settings component
then renders that store value as the “Token for API and Terraform” and offers
copy and renewal controls. The component has a test-user check around renewal,
not around token display or copy.
([current panel application bundle][panel-app],
[current Settings UI bundle][panel-settings])

This frontend behavior is evidence of the current primary-account journey. It
does not establish that the backend always returns a non-empty token for every
principal. The panel's GraphQL possible-type table likewise defines
`EnvironmentInfoResult` as only `EnvironmentInfo`, `EnvironmentNotFound`, or
`Unauthorized`; it does not document why `Unauthorized` occurred.
([current GraphQL possible-types bundle][panel-types])

### S3 key metadata and secret material

REG.RU's current S3 guide instructs an authenticated user to open **My
resources → S3 storage → Access keys**, open a key set's parameters, and copy
`S3 API Endpoint`, `Access key ID`, and `Secret access key`. It documents
later retrieval from an existing key set, but does not identify which account
roles may perform it or promise a masking/redaction contract.
([S3 access guide][s3-access])

The current private `objectStore` query requests:

- object-store `status` and `isLocked`;
- one `keypair` including `server`, `accessKey`, and `secretKey`;
- a `keypairs` list containing only non-secret metadata.

Its result alternatives are `ObjectStore`, `ObjectStoreNotFound`, and
`Unauthorized`.
([current S3 GraphQL bundle][s3-graphql])

The dedicated `objectStoreKeyPairs` list query requests `id`, `name`,
`instanceId`, `accessKey`, `secretKey`, and `createdAt` for every item. The
dedicated `objectStoreKeyPair` detail query requests the same secret fields for
one item. Their explicitly selected alternatives are `Unauthorized` and
`ObjectStoreNotFound`; the generated possible-type table additionally allows
`ObjectStoreKeyPairNotFound` for the single-item result. No role, permission,
masked-secret, or field-level-denial variant is present.
([current S3 GraphQL bundle][s3-graphql],
[current GraphQL possible-types bundle][panel-types])

The current S3 UI deliberately separates list metadata from reveal:

- the table displays name, project/key-set ID, and creation date;
- opening a key-set settings drawer performs the detail query;
- `Access key ID` is rendered directly and `Secret access key` through a
  secret-display control;
- when the S3 surface is locked, key-set names cease to be links and row
  actions are locked;
- an empty list renders “Access keys have not been created yet” and offers a
  separate create action;
- an absent object store renders “You do not have S3 storage yet” and offers a
  separate activation action.

These are UI gates, not a backend authorization guarantee. In particular, the
list GraphQL document currently asks for secret fields even though the table
uses only metadata, so `regru` must not equate “list works” with “secret is
safe and usable.”
([current S3 UI bundle][s3-ui],
[current S3 application bundle][s3-app])

## What can and cannot be concluded

### Evidence-backed conclusions

1. A missing Cloud environment, missing S3 object store, and empty S3 key list
   are distinct setup states, not role failures.
2. `Unauthorized` is the only explicit authorization failure handled by the
   relevant private operations. The public source does not split it into
   unauthenticated, expired-session, wrong-environment, or insufficient-role
   variants.
3. Cloud and S3 expose service lock state independently of credential fields.
   The current S3 UI disables key-detail navigation while locked.
4. Existing S3 key details are designed to be revisited and can contain both
   access and secret values in the current implementation.
5. Creating/resetting an S3 key pair and renewing a CloudVPS token are separate
   mutations. They are not valid read fallbacks.

### Inferences and unknowns

The following are **not established** by the first-party public evidence:

- that every owner, employee, partner, reseller, organization user, or other
  possible REG.RU principal receives the same credential fields;
- that `ownerType` or Cloud `accountType` is an authorization role;
- whether an unauthorized result means expired login, missing service access,
  a restricted role, an invalid selected environment, or another provider
  policy;
- whether a hidden secret is returned as null, empty, masked text, a GraphQL
  error, or `Unauthorized`;
- whether a locked Cloud environment still returns a token for every account,
  or whether that token can perform reads while locked;
- whether S3 list results always include unmasked secrets, despite the current
  selection set requesting them;
- any stable field nullability, field-level authorization, role name, role
  inheritance, or least-privilege contract.

The absence of a role predicate in public frontend code is evidence that the
current client does not make that decision locally. It is **not** evidence
that the backend has no role-dependent policy.

## Product-specific capability decisions

### CloudVPS

Evaluate one selected `serviceId` at a time:

| Observation | Capability | Actionable fallback |
| --- | --- | --- |
| No environment / `EnvironmentNotFound` | `not-configured` | Explain that this portal principal has no discoverable CloudVPS environment; open the Cloud panel/service-order journey. |
| `Unauthorized` | `unauthorized` | Refresh/re-authenticate the same portal profile once; then ask the user to verify the intended REG.RU account/environment in the panel. |
| `EnvironmentInfo.token` is non-empty | `available` | Show an import plan and require confirmation before disclosing/persisting it. Preserve `isLocked` as a separate service state. |
| `EnvironmentInfo` exists but token is unusable/missing/masked | `metadata-only` | Open **API and Terraform / Settings** and allow secure manual import of an existing token. Do not call `renew_token`. |
| Schema/transport mismatch | `unverified` | Report that automatic portal bootstrap is incompatible; accept an existing token through the normal secure configuration path. |

### S3

Discover the object store before key pairs:

| Observation | Capability | Actionable fallback |
| --- | --- | --- |
| `ObjectStoreNotFound` | `not-configured` | Open the S3 panel. Offer activation only as a distinct, explicitly confirmed provider mutation. |
| Object store exists but key-pair list is empty | `not-configured` | Explain that no access key set exists. Offer **Create key set** only as a distinct confirmed action; never create silently. |
| Key metadata is readable but either credential field is unavailable | `metadata-only` | Open that key set's **Parameters** page or accept an already-held pair through secure manual configuration. Never reset it automatically. |
| Complete `accessKey` + `secretKey` returned | `available` | Confirm disclosure and destination persistence, then import. Do not print either value in normal output. |
| Object store/environment is locked or suspended | `service-locked` | Explain that S3 is unavailable/limited in the panel and direct the user to billing or support. Do not create, reset, delete, or validate keys. |
| Persistent `Unauthorized` | `unauthorized` | Ask the user to re-authenticate and verify the intended account/environment. If the panel also denies access, direct them to REG.RU support; do not invent a required role name. |

## Required `regru` behavior

1. Probe capability only inside the selected, isolated portal profile and
   Cloud environment. Never cache visibility across accounts or `serviceId`
   values.
2. Request the least secret-bearing operation practical. For S3, list and show
   metadata before fetching one selected key pair; do not copy the current
   frontend's secret-bearing bulk list into CLI logs, caches, or diagnostics.
3. Treat null, empty, masked, partial, and unexpected credential shapes as
   `metadata-only` or `unverified`, never as an invitation to rotate.
4. Redact response bodies from errors and telemetry because even list
   operations may contain credential material.
5. Separate “credential can be imported” from “service is operational.”
6. Make every fallback explicit:
   **reauthenticate**, **choose another environment**, **open provider panel**,
   **configure an existing credential**, **create a new credential with
   confirmation**, or **contact support**.
7. Never say “only owners/admins can view this” unless REG.RU later publishes
   that role contract or an authorized characterization produces a stable,
   provider-confirmed rule.

## Remaining authorized characterization

An authenticated read-only study could determine actual runtime behavior for
multiple provider-approved test roles and service states, but it must record
only whether each sensitive field is absent, masked, or present—not its value.
Until then, the capability-detected contract above is the strongest conclusion
supported by public first-party evidence.

## Primary sources

- [CloudVPS API authentication and token location][cloudvps-auth]
- [REG.Cloud S3 access and existing key-set parameters][s3-access]
- [REG.Cloud panel shell][panel-shell]
- [Current environment query bundle][panel-environment]
- [Current core operation bundle][panel-core]
- [Current panel application bundle][panel-app]
- [Current CloudVPS Settings UI bundle][panel-settings]
- [Current GraphQL possible-types bundle][panel-types]
- [Current S3 GraphQL bundle][s3-graphql]
- [Current S3 UI bundle][s3-ui]
- [Current S3 application bundle][s3-app]

[cloudvps-auth]: https://developers.cloudvps.reg.ru/getting-started/authentication.html
[s3-access]: https://reg.cloud/support/instrukcii/obektnoe-hranilishe-s3/sposoby-dostupa-k-faylam-v-s3
[panel-shell]: https://cloud.reg.ru/panel
[panel-environment]: https://cloudvps-static.svc.reg.ru/panel/3539.e74c128bd819e4bb7701.js
[panel-core]: https://cloudvps-static.svc.reg.ru/panel/2720.12363fb271d587598e64.js
[panel-app]: https://cloudvps-static.svc.reg.ru/panel/1869.03b5ef37474f826de69b.js
[panel-settings]: https://cloudvps-static.svc.reg.ru/panel/3883.ef1e369f89060d65589d.js
[panel-types]: https://cloudvps-static.svc.reg.ru/panel/4884.d1396a4e86a10fe50676.js
[s3-graphql]: https://cloudvps-static.svc.reg.ru/s3/584.cc0c4d3c15727ef94da9.js
[s3-ui]: https://cloudvps-static.svc.reg.ru/s3/7667.846a918e4ebc982b28b2.js
[s3-app]: https://cloudvps-static.svc.reg.ru/s3/__federation_expose_s3.78c362b7cb0417199a8a.js
