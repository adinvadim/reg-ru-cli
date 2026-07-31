# Private REG.RU portal resilience contract

Research date: 2026-07-30

## Scope and evidence standard

This note characterizes failure handling for the private REG.RU support
website and REG.Cloud control plane previously identified by the Wayfinder
map. It uses only current first-party REG.RU documentation, anonymous response
headers, anonymously accessible GraphQL schema metadata, and publicly served
first-party frontend source.

No account was authenticated. No private account or resource data was read,
no captured request was replayed, and no provider mutation was sent. GraphQL
inspection was limited to schema introspection; it did not query a resource.

The labels below are deliberate:

- **Evidence** is directly visible in a cited first-party source.
- **Inference / CLI policy** is the conservative behavior required because the
  private interface lacks a published guarantee.

## Decision

Neither surface provides a published retry or idempotency contract. A
successful response may contain a provider identifier, but neither support
ticket creation nor the REG.Cloud control-plane mutations accept a
client-supplied idempotency key. A connection loss, local timeout, or `5xx`
after a mutation was sent therefore means **outcome unknown**, not “failed.”

The safe contract for `regru` is:

1. Compatibility-probe the current frontend/schema before enabling a private
   operation. Unknown operation arguments, result types, auth behavior, or
   selected environment must fail closed.
2. Retry read-only queries only, with a small finite backoff and one in-flight
   query per profile/environment. No first-party source specifies a permitted
   polling rate.
3. Do not automatically retry a mutation after any ambiguous transport
   failure, timeout, `429`, or `5xx`. `Retry-After`, if REG.RU adds it, tells a
   client when it may send another request; it does not make an earlier
   mutation safe to duplicate.
4. Re-read provider state using an independent query and compare it with the
   exact intended postcondition. If that cannot prove the outcome, stop and
   require human verification in the provider UI.
5. Serialize mutations by `(portal profile, Cloud service ID, resource)`.
   There is no public revision, ETag precondition, or compare-and-swap
   mechanism protecting private control-plane state from concurrent writers.
6. Keep the support adapter unavailable by default. Its current public source
   is enough to prove ambiguity, not enough to provide a stable ticket read
   surface that can reconcile a lost create/reply response.

## What can be probed before a mutation

### REG.Cloud version and schema drift

**Evidence.** REG.Cloud publishes module-federation manifests with
`Cache-Control: no-cache`, `ETag`, `Last-Modified`, application identity,
`metaData.buildInfo`, and content-hashed asset names. On the research date:

| Manifest | Current identity | Build version |
| --- | --- | --- |
| [Cloud panel manifest][panel-manifest] | `@cloudvps/panel` / `cloudVpsPanel` | `2.69.3` |
| [S3 manifest][s3-manifest] | `@cloudvps/s3` / `cloudVpsS3` | `1.0.0` |
| [S3 order manifest][order-s3-manifest] | `@cloudvps/order-s3` / `cloudVpsOrderS3` | `1.0.0` |

The public Cloud GraphQL URL currently serves GraphiQL on `GET` and permits
anonymous schema introspection. Its schema exposes 92 mutations and 109
queries on the research date. None of the 92 mutation argument lists contains
an argument whose name matches `idempot*`, `operation*`, `request*`, or
`client*`. ([Current Cloud GraphQL schema endpoint][cloud-graphql])

The S3/control-plane subset currently has these mutation arguments:

| Mutation | Arguments |
| --- | --- |
| `createObjectStore` | `region` |
| `createBucket` | `name`, `objectStoreId`, `isPublic`, `quotaGb` |
| `deleteBucket` | `name`, `objectStoreId`, `force` |
| `manageBucketPrivacy` | `name`, `objectStoreId`, `isPublic` |
| `manageBucketQuota` | `name`, `objectStoreId`, `quotaGb` |
| `manageObjectStoreQuota` | `objectStoreId`, `quotaGb` |
| `createObjectStoreKeyPair` | `name` |
| `resetObjectStoreKeyPair` | `keyPairId` |
| `deleteObjectStoreKeyPair` | `keyPairId` |
| `copyBucketObjects`, `moveBucketObjects`, `renameBucketObject`, `deleteBucketObjects` | `params` |

The public panel source independently embeds those operation documents and
their result unions. The current `createBucket` union, for example, contains
`Bucket`, `ObjectStoreNotFound`, `InvalidBucketName`,
`BucketNameConflict`, `BucketLimitExceeded`, `InvalidQuota`, and
`Unauthorized`; the key and quota operations similarly return named domain
results rather than a generic success boolean. ([Current S3 common
bundle][panel-s3-common], [current S3 operation bundle][panel-s3-operations],
[current S3 order bundle][panel-s3-order])

**Inference / CLI policy.** Before a private Cloud mutation:

1. Fetch each relevant manifest without accepting a stale cached copy.
2. Verify the application/build identity and locate the current hashed asset;
   do not pin a hashed filename forever.
3. Introspect only schema metadata and verify the required operation name,
   argument names/types, success type, and known domain-error union members.
4. Preflight session identity and explicit `Service-ID`, then run the
   operation with `__typename` selected on every union result.
5. Treat a missing manifest/schema, a changed argument, a removed result type,
   or an unknown returned `__typename` as incompatible. Do not “best effort”
   a mutation against a guessed shape.

The build version and ETag are drift alarms, not semantic-version promises.
The S3 and order-S3 packages both report `1.0.0` while their asset hashes can
change; conversely, an unrelated panel release need not invalidate a
previously verified operation. The schema allowlist is the decisive probe.

### Support frontend drift

**Evidence.** The public support page currently loads
`knowledge-base-main.1d65b0a5ec7920d5b7e6.js`. The page and bundle expose
`ETag`/`Last-Modified`; the asset name is content-hashed. The bundle calls
unversioned relative routes, including:

- `POST /support/upload_file`;
- `POST /support/remove_file`;
- `POST /support/send_universal_support_message`;
- `POST /support/tickets/toggle_notify`;
- `GET /support/get_services_for_user`;
- `GET /support/get_user_phone_data`;
- `GET /support/get_alert`.

The create flow returns a `file_id` for a successful temporary upload and,
after a successful ticket creation, `response_data.Token` and
`response_data.TicketNumber`. The request arguments contain no client request
ID or idempotency key. The call site sets no per-request timeout, abort signal,
or retry policy, and the bundle contains no `Retry-After`, `operationId`,
`clientMutationId`, or idempotency handling. On a transport exception it shows
an error screen and lets the user return to the populated form; it cannot tell
whether the first submission committed. ([Current support page][support-home],
[current support-request bundle][support-bundle])

The official support article and rules define the supported channel as the
electronic form in the personal cabinet, not as a versioned API.
([How to contact REG.RU support][support-help], [support-service
rules][support-rules])

**Inference / CLI policy.** A bundle hash and route-marker check can detect
that today's implementation changed, but cannot authorize it or establish
backward compatibility. Any support bundle/route/field drift disables the
experimental capability. Even with no detected drift, ticket mutations remain
fail-closed until REG.RU publishes a contract or a separately authorized
adapter gains a reliable, independent ticket read/reconciliation path.

## Rate limits and timeouts

### What REG.RU currently exposes

**Evidence.** Anonymous `GET`/`OPTIONS` observations of the Cloud GraphQL URL,
account GraphQL URL, and the three public support GET routes above exposed no
`RateLimit`, `X-RateLimit-*`, or `Retry-After` headers. The anonymous Cloud
introspection response also exposed none. The manifests publish cache
validators but no request quota. ([Cloud GraphQL endpoint][cloud-graphql],
[account GraphQL endpoint][account-graphql], [support service-list
route][support-services], [support phone route][support-phone], [support alert
route][support-alert])

The frontend source publishes no private support or Cloud GraphQL request
deadline and no retry budget. A bundled Axios default of `timeout: 0` is
library machinery, not evidence of a provider promise. The account/Cloud
session client's 150-second refresh-staleness threshold is likewise an auth
refresh decision, not an API timeout or rate limit. ([Current panel auth
bundle][panel-auth], [current account auth bundle][account-auth])

`Site.Config.Recaptcha.MaxAttemts: 10` and `BanMinutes: 10` on the public
support page concern login/CAPTCHA brute-force behavior. They must not be
misreported as support-ticket or Cloud API rate limits.
([Current support page][support-home])

### CLI rule

**Inference / CLI policy.**

- There is no evidence-backed requests-per-second value, concurrency allowance,
  server deadline, or polling interval to encode.
- Use one in-flight mutation per scoped resource and a small, finite,
  jittered backoff only for read-only queries. Honor a syntactically valid
  `Retry-After` if one appears, but retain a finite local retry budget.
- Give every network call a configurable local deadline so the CLI cannot
  hang forever. A read timeout may be retried; a mutation timeout changes the
  operation to `outcome-unknown`.
- Do not infer overload solely from GraphQL HTTP status. The current schema
  represents many failures as union members inside an HTTP-successful GraphQL
  response. Parse transport errors, GraphQL errors, and domain `__typename`
  separately.
- Do not retry authentication, CSRF, validation, `Unauthorized`, not-found,
  conflict, quota, limit, insufficient-funds, or blocked-environment results.
  Those require reauthentication, refreshed state, corrected input, or user
  action, not backoff.

## Retry-safety matrix

| Operation class | Evidence | Automatic retry | Required handling |
| --- | --- | --- | --- |
| Read-only GraphQL query / support GET | Query/GET shape is current implementation evidence; no rate contract | Only on a clearly read-only call and transient transport failure, with finite jittered backoff | Revalidate profile identity and Cloud `Service-ID`; stop on auth, schema, or domain errors |
| Temporary support upload | Success creates a server-side `file_id`; no client key or lifetime contract | **No** after an ambiguous response | Preserve a returned `file_id`; otherwise report outcome unknown and let the provider clean up or the user inspect the form |
| Support ticket creation | Success returns `Token` and `TicketNumber` only after commit; no client key | **No** | On lost response, instruct the user to inspect **Заявки в поддержку** before submitting again |
| Support reply, close/reopen, attachment-to-reply | No public schema or stable read-after-write path was found | **Never enabled** | Capability error and human handoff |
| Support `toggle_notify` | The current route is explicitly a toggle, not a desired-state setter | **No** | Requires a readable current/after state; absent that, fail closed |
| Cloud create/delete/set mutation | Schema has natural IDs/names and independent read queries, but no idempotency/revision argument | **No** after send | Reconcile the exact postcondition; require confirmation before any new attempt |
| Cloud key reset / token renewal | Operation rotates secret material; no client key | **Never** | Re-read and compare only secret fingerprints in memory; if the outcome remains unclear, require manual recovery/another explicit rotation |
| S3 object copy/move/rename/delete operation | Accepted work has provider active-operation metadata including UUID `operationId` | **No** for submission | Track accepted work; then query active operations and final object state |

Failures known to occur **before any request bytes are sent**, such as local
input validation or failure to acquire a browser context, are not ambiguous
provider outcomes and may be attempted again. A generic “connection error”
does not prove this; the client must know the request was not written.

## Operation IDs are tracking IDs, not idempotency keys

**Evidence.** The Cloud schema defines `BucketObjectActiveOperation` with a
non-null UUID `operationId`, operation/status fields, source/destination
bucket and object keys. The query
`bucketObjectsActiveOperations(bucketName)` lists these records, and the
panel subscription also carries them. The copy/move/rename/delete mutation
arguments do not accept an `operationId`; their immediate
`BucketObjectOperation` result contains status/details but no client-selected
deduplication token. ([Cloud GraphQL schema endpoint][cloud-graphql],
[current S3 common bundle][panel-s3-common], [current panel event
bundle][panel-core])

**Inference / CLI policy.** Once an operation is known to have been accepted,
store its provider UUID only as an opaque, environment-scoped monitoring
handle. It can correlate subscription/query events and assist final
reconciliation. It cannot justify resending the mutation after a lost
response, because the second request cannot present the same UUID.

Support's returned `TicketNumber`, opaque `Token`, and upload `file_id` have
the same timing problem: they identify a result only after a successful
response. They do not deduplicate a response that never reached the client.

## Read-after-write reconciliation

Every reconciliation must use a fresh provider read, bypass a stale Apollo/UI
cache, remain scoped to the same portal identity and `Service-ID`, and compare
the full intended postcondition. “The resource exists” is insufficient when
another actor could have created or changed it.

| Ambiguous Cloud mutation | Independent current read | Reconciled success condition |
| --- | --- | --- |
| `createObjectStore(region)` | `objectStore` | The read can prove that a singleton store now exists, but the current `ObjectStore` type does not expose `region`; a lost response therefore cannot be fully reconciled and requires a manual check |
| `createBucket(name, objectStoreId, isPublic, quotaGb)` | `bucket(name)` or `buckets(names)` | Same store, exact name, requested quota and access state |
| `deleteBucket(name, objectStoreId, force)` | `bucket(name)` / `buckets(names)` | The preflight target is absent; do not confuse later same-name recreation with the deleted object |
| `manageBucketPrivacy` | `bucket(name)` plus policy/access reads where available | Provider access state exactly matches the requested state; raw S3 policy/ACL drift is still a separate concern |
| `manageBucketQuota` | `bucket(name)` | Exact requested quota |
| `manageObjectStoreQuota` | `objectStore` | Exact requested storage quota |
| `createObjectStoreKeyPair(name)` | `objectStoreKeyPairs` then `objectStoreKeyPair(id)` | Exactly one pair with that name exists; secret material is handled without logging |
| `deleteObjectStoreKeyPair(keyPairId)` | key-pair list/detail | The preflight key ID is absent |
| object copy/move/rename/delete | `bucketObjectsActiveOperations` then `bucketObjects`/`bucket` | No relevant operation remains active and exact source/destination object postconditions hold |

The current schema makes these reads possible, but does not publish
consistency timing, conditional reads, revision fields, or a maximum operation
duration. Therefore:

- poll with finite backoff and stop as `still-pending` rather than declaring
  failure at an invented deadline;
- do not automatically resubmit when a read temporarily shows the old state;
- if the desired state is present but ownership is ambiguous, report
  `reconciled` rather than claiming the original response succeeded;
- if state is mixed or cannot be read, report `outcome-unknown` and open the
  relevant REG.Cloud panel page for human inspection.

`resetObjectStoreKeyPair` and the SRS `renew_token` action are special.
Their desired output is newly generated secret material, so existence alone
cannot prove which attempt produced it. The panel's token action posts
`type: renew_token` with CSRF and account context, but no idempotency or
operation ID. An ambiguous rotation must never be repeated automatically.
([Current panel core bundle][panel-core])

## Fail-closed state machine

```text
probe
  -> incompatible       manifest/schema/identity/selector mismatch
  -> ready              all required read and result shapes recognized

ready + read
  -> retryable-read     transient transport failure within finite budget
  -> ready              recognized result
  -> reauth-required    401 / Unauthorized / session or identity mismatch

ready + mutation
  -> committed          recognized success response
  -> rejected           recognized domain error; do not retry
  -> outcome-unknown    timeout, disconnect, 429, 5xx, malformed/unknown result

outcome-unknown
  -> reconciled         independent read proves exact postcondition
  -> not-reconciled     read proves old state; user confirms any new attempt
  -> manual-check       no stable read, mixed state, or consistency deadline hit
```

For support, `probe -> ready` is intentionally unavailable for ticket
mutations under the public evidence collected here. The safe outcome is a
manual handoff to the official support form. For REG.Cloud, a private adapter
may reach `ready` only for an explicitly allowlisted schema and an isolated,
identity-checked browser session.

## Evidence-backed implementation boundary

The public sources support a useful resilience layer, but not transparent
HTTP emulation:

- manifests, schema shapes, `__typename`, session identity, and Cloud
  environment selection are pre-mutation gates;
- query retry is conservative and bounded;
- mutation retry is off by default and remains off for ambiguous outcomes;
- operation IDs monitor accepted S3 object work but do not deduplicate;
- natural identifiers enable exact read-after-write reconciliation for much
  of the Cloud control plane;
- secret rotations and the support surface require the strictest fail-closed
  behavior.

No public evidence establishes a private-interface SLA, semantic-versioning
promise, request quota, timeout, idempotency window, webhook, or linearizable
read-after-write guarantee. Those values must remain unknown until REG.RU
publishes them or explicitly supports the integration.

## Primary sources

- [Current public REG.Cloud panel manifest][panel-manifest]
- [Current public REG.Cloud S3 manifest][s3-manifest]
- [Current public REG.Cloud order-S3 manifest][order-s3-manifest]
- [Current public Cloud GraphQL/GraphiQL endpoint][cloud-graphql]
- [Current REG.Cloud GraphQL client bundle][panel-graphql-client]
- [Current REG.Cloud S3 common operation bundle][panel-s3-common]
- [Current REG.Cloud S3 resource-operation bundle][panel-s3-operations]
- [Current REG.Cloud S3 order bundle][panel-s3-order]
- [Current REG.Cloud core/event operation bundle][panel-core]
- [Current public REG.RU support page][support-home]
- [Current support-request frontend bundle][support-bundle]
- [Official REG.RU support contact guidance][support-help]
- [Official support-service rules][support-rules]

[panel-manifest]: https://cloudvps-static.svc.reg.ru/panel/mf-manifest.json
[s3-manifest]: https://cloudvps-static.svc.reg.ru/s3/mf-manifest.json
[order-s3-manifest]: https://cloudvps-static.svc.reg.ru/order-s3/mf-manifest.json
[cloud-graphql]: https://cloudvps-graphql-server.svc.reg.ru/api
[account-graphql]: https://gql-acc.svc.reg.ru/
[panel-graphql-client]: https://cloudvps-static.svc.reg.ru/panel/__federation_expose_panel.230377f66688d4eeb56c.js
[panel-auth]: https://cloudvps-static.svc.reg.ru/panel/107.7ba232fd9b902061aea1.js
[account-auth]: https://www.reg.ru/user/account/1508.40f765beebd5cfa3df2d.js
[panel-s3-common]: https://cloudvps-static.svc.reg.ru/panel/3619.65a559365d90541d575b.js
[panel-s3-operations]: https://cloudvps-static.svc.reg.ru/s3/144.164eaa226a1357642e37.js
[panel-s3-order]: https://cloudvps-static.svc.reg.ru/order-s3/170.1dcfbda891d9cc726a6a.js
[panel-core]: https://cloudvps-static.svc.reg.ru/panel/2720.12363fb271d587598e64.js
[support-home]: https://help.reg.ru/support/
[support-bundle]: https://help.reg.ru/dist/knowledge-base-main.1d65b0a5ec7920d5b7e6.js
[support-help]: https://help.reg.ru/support/lichnyy-kabinet/registratsiya-i-kontaktnyye-dannyye/kak-svyazatsya-so-sluzhboy-podderzhki
[support-rules]: https://img.reg.ru/faq/pravila_obsluguvania_rules-of-service-support_11082025.pdf
[support-services]: https://help.reg.ru/support/get_services_for_user?page=0
[support-phone]: https://help.reg.ru/support/get_user_phone_data
[support-alert]: https://help.reg.ru/support/get_alert
