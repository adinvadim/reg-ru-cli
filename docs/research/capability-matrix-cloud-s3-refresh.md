# CloudVPS and REG.RU S3 capability-matrix refresh

Research date: 2026-07-31

## Scope and evidence boundary

This refresh narrows the authorized capability work in
[Research: authenticate and map REG.RU capabilities](https://github.com/adinvadim/reg-ru-cli/issues/2)
to CloudVPS and REG.RU S3. It answers four questions:

1. What credential model does each product use?
2. Can an existing credential be retrieved without creating or rotating it?
3. Which control-plane actions create, rotate, revoke, or activate access?
4. What is the smallest read-only probe set for inventory, balance, catalog,
   servers, S3 buckets, and provider control-plane visibility?

Only current first-party documentation, current public REG.RU frontend
artifacts, and the official Amazon S3 request reference were inspected. The
REG.RU panel shell and the content-hashed frontend bundles cited below were
served by REG.RU on the research date; several carry `Last-Modified` dates of
2026-07-28 or 2026-07-30. Frontend bundles are implementation evidence, not a
published or versioned API contract.

No account was authenticated. No secret, cookie, account identifier, bucket,
balance, or server was read. No authenticated provider API was called. No
token, key pair, object store, bucket, server, quota, or access mode was
created, changed, reset, or deleted.

## Decision

Ticket #2 does **not** need credential or service mutations to characterize an
already-configured account:

- A CloudVPS environment has one opaque bearer token. REG.RU documents that
  the existing token can be viewed and copied in **Settings**, and the current
  panel's read-only `environmentInfo` query requests the token. Reading it does
  not rotate it. The token grants all CloudVPS operations; REG.RU does not
  document a read-only scope.
- REG.RU S3 uses named key sets containing `Access key ID` and
  `Secret access key`. REG.RU documents reopening an existing set's parameters,
  and the current panel has read-only detail/list queries that request both
  fields. Reading an existing pair does not reset it.
- CloudVPS token renewal, S3 service activation, S3 key-set creation, and S3
  key reset are separate mutations. They must not be used as fallbacks when a
  read produces missing, masked, unauthorized, locked, or incompatible data.
- A successful S3 `ListBuckets` and a successful REG.Cloud `objectStore` query
  prove different capabilities. The first proves the signed S3 data-plane
  listing path; the second proves current private provider-control-plane
  visibility such as service status, quota, and panel-visible buckets.

Sources:
[CloudVPS authentication](https://developers.cloudvps.reg.ru/getting-started/authentication.html),
[current environment query bundle](https://cloudvps-static.svc.reg.ru/panel/3539.e74c128bd819e4bb7701.js),
[REG.Cloud S3 access guide](https://reg.cloud/support/instrukcii/obektnoe-hranilishe-s3/sposoby-dostupa-k-faylam-v-s3),
[REG.Cloud S3 key-set guide](https://reg.cloud/support/instrukcii/obektnoe-hranilishe-s3/kak-nastroit-backup-dannyh-s-servera-s-ispmanager-v-hranilishche-s3),
and
[current S3 GraphQL bundle](https://cloudvps-static.svc.reg.ru/s3/584.cc0c4d3c15727ef94da9.js).

## Credential and lifecycle matrix

| Product | Existing credential | Existing credential retrievable without rotation? | Creation/rotation surface | Published authority and lifecycle |
| --- | --- | --- | --- | --- |
| CloudVPS | Opaque bearer token sent as `Authorization: Bearer <token>` | **Yes.** REG.RU says it can be viewed and copied in the Settings tab. The current `environmentInfo` query selects `token`. | Current private panel action posts `type: renew_token` for the selected service, with session CSRF and user context. | The token opens all operations on the user's Cloud Servers service. No expiry, scopes, multiple-token model, grace period, or programmatic public token-management API is documented. |
| REG.RU S3 | Named key set with `Access key ID` and `Secret access key`, used with the panel-provided endpoint | **Yes.** The public guide tells users to reopen **Parameters of the key set** and copy both values. Current `objectStoreKeyPair` and `objectStoreKeyPairs` query documents select both fields. | Current private mutations are `createObjectStore`, `createObjectStoreKeyPair`, `resetObjectStoreKeyPair`, and `deleteObjectStoreKeyPair`. | REG.RU does not document per-key scopes, expiry, bucket binding, maximum active sets, reset grace, or revocation propagation. Bucket policy/access mode is a separate authorization layer. |

The CloudVPS statements are supported by the
[authentication documentation](https://developers.cloudvps.reg.ru/getting-started/authentication.html),
the
[current environment query](https://cloudvps-static.svc.reg.ru/panel/3539.e74c128bd819e4bb7701.js),
the
[current environment-enumeration and renewal implementation](https://cloudvps-static.svc.reg.ru/panel/2720.12363fb271d587598e64.js),
and the current
[Settings UI](https://cloudvps-static.svc.reg.ru/panel/3883.ef1e369f89060d65589d.js).
The S3 statements are supported by the
[access guide](https://reg.cloud/support/instrukcii/obektnoe-hranilishe-s3/sposoby-dostupa-k-faylam-v-s3),
the
[key-set creation guide](https://reg.cloud/support/instrukcii/obektnoe-hranilishe-s3/kak-nastroit-backup-dannyh-s-servera-s-ispmanager-v-hranilishche-s3),
the
[current S3 GraphQL operations](https://cloudvps-static.svc.reg.ru/s3/584.cc0c4d3c15727ef94da9.js),
and the
[current S3 key-set UI](https://cloudvps-static.svc.reg.ru/s3/7667.846a918e4ebc982b28b2.js).

### What the evidence does not establish

- The public sources publish no role-to-secret-visibility table. A selected
  field in a frontend query proves what the panel requests, not that every
  principal receives a non-empty, unmasked value.
- Neither product has a documented least-privilege read-only credential.
- REG.RU does not state that resetting an S3 pair preserves the previous pair
  for any grace period, or that changing a CloudVPS token leaves the previous
  token valid. Ticket #2 must assume existing consumers may break.
- The private GraphQL and SRS operation shapes are current implementation
  evidence only. They require a manifest/operation contract check and must
  fail closed on drift.

## Current read surfaces

### Portal inventory and credential discovery

The current panel's read-only `environments` query returns Cloud environment
`serviceId` values, with `Unauthorized` and `EnvironmentNotFound` alternatives.
For one selected environment, `environmentInfo` returns `isLocked`,
`serviceId`, and `token`, again with `Unauthorized` and
`EnvironmentNotFound` alternatives. This supports:

1. enumerate environments without fetching a CloudVPS token;
2. select one environment;
3. fetch the existing token only after the secret-handling boundary is
   approved.

Sources:
[current environment-enumeration bundle](https://cloudvps-static.svc.reg.ru/panel/2720.12363fb271d587598e64.js)
and
[current environment-info bundle](https://cloudvps-static.svc.reg.ru/panel/3539.e74c128bd819e4bb7701.js).

The current S3 `objectStore` query returns service `status`, `isLocked`,
storage and bucket quotas, bucket count/limit, current bucket metadata, usage
and cost data, key-set metadata, and one `keypair` including `server`,
`accessKey`, and `secretKey`. Its explicit non-success alternatives are
`ObjectStoreNotFound` and `Unauthorized`.
([current S3 GraphQL bundle](https://cloudvps-static.svc.reg.ru/s3/584.cc0c4d3c15727ef94da9.js))

That last fact creates a sharp safety boundary: `objectStore` is read-only in
provider state but **secret-bearing in its current response**. It must not be
logged, recorded as an ordinary metadata fixture, or used before the approved
secret sink is ready. A reduced custom GraphQL selection that omits `keypair`
would expose less data, but REG.RU does not publish such a query as a stable
contract; it must be characterized before relying on it.

### CloudVPS public REST API

The current v1 OpenAPI defines bearer authentication for both:

- `GET /v1/balance_data`, returning `balance_data` with `balance`,
  `bonus_balance`, `days_left`, `hours_left`, hourly/monthly cost,
  `detalization`, and an optional service state of `active`, `suspended`, or
  `stopped`;
- `GET /v1/reglets`, returning the server collection plus active action links.

The current narrative server documentation also describes `GET /v1/reglets`
as the list operation and records server states including `active`, `off`,
`suspended`, and `archive`.
([v1 OpenAPI](https://api.cloudvps.reg.ru/v1/openapi.json),
[server-list documentation](https://developers.cloudvps.reg.ru/reglets/list.html))

The current v2 OpenAPI has only two catalog operations:

- `GET /v2/plans`;
- `GET /v2/images`.

Both require bearer authentication and the query parameters `region`, `page`,
and `items_per_page`; the current schema permits 10–100 items per page.
Responses contain a collection plus pagination metadata. The v2 error
examples distinguish `401 TOKEN_VALIDATION_FAILED` from
`403 ENVIRONMENT_BLOCKED`.
([v2 OpenAPI](https://api.cloudvps.reg.ru/v2/api/swagger.json),
[plan catalogue](https://developers.cloudvps.reg.ru/sizes/index.html),
[image catalogue](https://developers.cloudvps.reg.ru/images/list.html))

### S3 bucket list

REG.RU documents the S3 endpoint as `https://s3.regru.cloud`, an
`Access key ID`/`Secret access key` pair, and path-style object URLs. Its
access-policy catalogue names `ListAllMyBuckets` and `ListBucket`, which is
first-party evidence that account-level bucket listing and per-bucket object
listing are authorization concepts in the service.
([REG.Cloud access methods](https://reg.cloud/support/instrukcii/obektnoe-hranilishe-s3/sposoby-dostupa-k-faylam-v-s3),
[REG.Cloud access-policy catalogue](https://reg.cloud/support/cloud/obyektnoye-khranilishche-s3/politiki-dostupa-v-hranilishe-s3))

For the wire probe, the official S3 `ListBuckets` request is a signed `GET /`
and returns the buckets owned by the authenticated sender. REG.RU does not
publish a separate response schema or complete error catalogue, so ticket #2
must treat XML fields and provider error codes as observed compatibility data,
not import the whole AWS contract.
([Amazon S3 `ListBuckets`](https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListBuckets.html))

The REG.Cloud management guide independently shows the provider panel listing,
creating, changing quota/access mode for, and deleting buckets. This is
provider control-plane evidence, not proof that all those operations are
available through S3 access keys.
([REG.Cloud S3 management](https://reg.cloud/support/cloud/obyektnoye-khranilishche-s3/zakaz-i-upravlenie-uslugoj-obektnoe-hranilishche-s3/zakaz-i-upravleniye-obyektnym-khranilishchem-s3))

## Minimal authorized probe plan for ticket #2

The probe must record presence, shape, status, and counts only. It must never
record a token, access key, secret key, cookie, CSRF value, full account
identifier, provider response body containing secrets, or request-ID value.

### 1. Portal/environment inventory

| Probe | Minimum output | Capability result |
| --- | --- | --- |
| Current `environments` query | Number of environments; result typename | `available`, `not-configured`, `unauthorized`, or `contract-drift` |
| Current `environmentInfo` for each selected environment | `isLocked`; token present/empty/masked; token length only | Credential: `available`, `metadata-only`, `not-configured`, `unauthorized`, or `contract-drift`; service: `active-or-unknown` or `locked` |

Do not call `renew_token` when `token` is missing, masked, or rejected.

### 2. CloudVPS REST capability

Use the already-existing token only after the secret-use boundary is approved.
Send each request once; all are `GET`.

| Capability | Minimal request | Success evidence | Distinct non-success |
| --- | --- | --- | --- |
| Balance | `GET /v1/balance_data` | HTTP 200, expected `balance_data` shape; record service state and field presence, not monetary values unless the ticket explicitly authorizes them | HTTP 401 → `unauthorized`; schema/transport failure → `contract-drift`; `state: suspended/stopped` remains a separate service status |
| Servers | `GET /v1/reglets` | HTTP 200 and a decodable collection; record count and observed status vocabulary only | Empty collection is `available-empty`, not `not-configured`; HTTP 401 → `unauthorized` |
| Plan catalog | `GET /v2/plans?region=<documented-region>&page=1&items_per_page=10` | HTTP 200, `plans` array, pagination shape | 401/token error → `unauthorized`; 403 `ENVIRONMENT_BLOCKED` → `service-locked`; an empty page is `available-empty` |
| Image catalog | `GET /v2/images?region=<same-region>&page=1&items_per_page=10` | HTTP 200, `images` array, pagination shape | Same classification as plans |

A single documented region is the minimum authentication/contract probe.
Enumerating every current region is inventory work and may follow only if
ticket #2 needs a full per-region catalogue matrix.

### 3. S3 data-plane capability

Use one complete existing key pair and the panel-provided endpoint. Do not
create or reset a key set.

| Probe | Minimum output | Capability result |
| --- | --- | --- |
| Signed S3 `ListBuckets` (`GET /`) | HTTP status; decodable S3 XML shape; bucket count only; error code shape without body or request ID | `available`, `available-empty`, `unauthorized-or-invalid`, `forbidden`, or `contract-drift` |

REG.RU does not publish enough S3 error semantics to infer from a single 403
whether a credential is invalid, unscoped, or denied by another provider
policy. Preserve the observed HTTP status and parsed code in redacted evidence;
do not rotate the key to make the probe pass.

### 4. S3 provider-control-plane capability

| Probe | Minimum output | Capability result |
| --- | --- | --- |
| Current private `objectStore` query | Result typename; status/lock state; quota fields present; bucket count; bucket metadata field presence | `available`, `available-empty`, `not-configured`, `service-locked`, `unauthorized`, or `contract-drift` |

Run this only in the isolated authenticated portal adapter with secret-response
redaction already enforced, because the current query also selects a key pair's
secret material. A successful S3 `ListBuckets` does not substitute for this
probe: S3 does not expose provider activation state, panel quota, bucket
access-mode abstraction, or key lifecycle. Conversely, `objectStore.buckets`
does not prove that the imported S3 credentials can sign a bucket-list request.

## Normalized capability statuses

Use the same small status vocabulary for every profile/environment:

| Status | Meaning |
| --- | --- |
| `available` | Expected authenticated shape returned and the capability can be used. |
| `available-empty` | The list contract works but no resources were returned. This is a success, not missing configuration. |
| `metadata-only` | Resource/key metadata exists but required secret material is absent, masked, empty, or unusable. |
| `not-configured` | The provider explicitly reports no Cloud environment/object store, or no key set exists. |
| `service-locked` | Portal `isLocked`, CloudVPS balance state, or v2 `ENVIRONMENT_BLOCKED` shows a service-level restriction. Credential presence remains a separate fact. |
| `unauthorized` | The portal returns `Unauthorized` or CloudVPS returns 401. Reauthenticate once; never infer a provider role name. |
| `forbidden` | Authentication may have succeeded but the requested operation was denied. Preserve the provider code without guessing policy. |
| `contract-drift` | Transport, manifest, operation, or response shape no longer matches the characterized adapter. Fail closed. |
| `unverified` | The probe was intentionally not run, including because consent or a complete existing credential was unavailable. |

For S3 errors that cannot safely distinguish invalid authentication from
authorization, use `unauthorized-or-invalid` in the raw probe result and map
the overall capability to `unverified` until another non-mutating observation
resolves it.

## Mutation and confirmation boundaries

| Action | Provider mutation? | Boundary for ticket #2 |
| --- | --- | --- |
| Enumerate environment IDs or key-set metadata | No | Read-only; allowed after portal login. Do not emit account/service identifiers. |
| Read `environmentInfo` or S3 key-pair detail | No provider-state mutation, but secret-bearing | Require explicit approval of the specific secret use and destination before retrieval/persistence. Record only presence/length/shape. |
| Persist an existing token/key pair in the approved secret store | Local persistent-access change | Confirm the destination and account profile immediately before writing. Never place it in ordinary config, logs, shell history, or fixtures. |
| Run CloudVPS `GET` probes or signed S3 `ListBuckets` | No | Use only an explicitly authorized existing credential. No automatic fallback mutation after failure. |
| CloudVPS `renew_token` | **Yes: rotates a service-wide credential** | Outside ticket #2. Strong confirmation is required; warn that existing clients may stop working. |
| S3 `createObjectStore` | **Yes: activates a service and returns an initial key pair** | Outside ticket #2. Requires confirmation of service activation, possible billing, and new persistent access. |
| S3 `createObjectStoreKeyPair` | **Yes: creates persistent access** | Outside ticket #2. Requires explicit creation confirmation. REG.RU does not document a read-only scope. |
| S3 `resetObjectStoreKeyPair` | **Yes: rotates credentials** | Outside ticket #2. Strong confirmation; warn about breaking every consumer of the old pair. |
| S3 `deleteObjectStoreKeyPair` | **Yes: revokes/deletes access** | Outside ticket #2. Destructive confirmation and exact key-set identity are required. |
| Create/delete a bucket; change quota, public/private mode, policy, or CORS | **Yes** | Outside this read-only ticket. Apply the separate financial, destructive, or public-access confirmation appropriate to the operation. |

The current control-plane mutation names and returned fields are visible in the
[S3 GraphQL bundle](https://cloudvps-static.svc.reg.ru/s3/584.cc0c4d3c15727ef94da9.js);
the activation and key-set creation user journeys are independently described
by the
[S3 management guide](https://reg.cloud/support/cloud/obyektnoye-khranilishche-s3/zakaz-i-upravlenie-uslugoj-obektnoe-hranilishche-s3/zakaz-i-upravleniye-obyektnym-khranilishchem-s3)
and
[S3 key-set guide](https://reg.cloud/support/instrukcii/obektnoe-hranilishe-s3/kak-nastroit-backup-dannyh-s-servera-s-ispmanager-v-hranilishche-s3).

## Redacted result shape

One row per profile/environment and capability is sufficient:

```text
profile_alias
environment_alias
provider_surface
credential_status
service_status
probe_status
http_status_present
provider_code_class
schema_version_or_bundle_hash
resource_count
notes_without_identifiers_or_values
```

Do not retain response bodies from `environmentInfo`, `objectStore`,
`objectStoreKeyPair(s)`, or any error path that may echo authorization
material. For request IDs, record only whether the header/field was present.

## Decision for ticket #2

Proceed as a HITL authorized characterization using **existing** credentials
only. Enumerate Cloud environments first, retrieve each existing CloudVPS token
and one selected existing S3 key pair only after the approved secret boundary,
then run the four CloudVPS GET probes, S3 `ListBuckets`, and the current
`objectStore` visibility probe. Missing, masked, unauthorized, locked, empty,
or drifted results are capability outcomes—not permission to renew a token,
activate S3, create a key set, or reset one.
