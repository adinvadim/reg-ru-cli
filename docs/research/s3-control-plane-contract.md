# REG.RU S3 control-plane contract

Research date: 2026-07-30; authenticated drift refresh: 2026-08-01

## Question and evidence standard

This note separates the S3-compatible protocol exposed at `s3.regru.cloud`
from REG.Cloud's provider-specific control plane. Here, “S3 protocol” means
the bucket/object REST operations represented by the official Amazon S3 API;
it does not mean Amazon's separate “S3 Control” product.

Only first-party evidence was used:

- current REG.Cloud documentation;
- current public REG.Cloud frontend manifests and JavaScript bundles;
- the official Amazon S3 API reference as the protocol baseline.

The initial research did not authenticate or read account state. The
2026-08-01 drift refresh used the explicitly authorized existing profile for
one reduced, read-only inventory capture. It retained only response types,
field names, scalar kinds, array counts, and invariant results. It did not
select secret fields or retain identifiers, bucket names, quota values, usage
values, cookies, session values, request IDs, or response bodies. No bucket,
key, policy, quota, access mode, or service state was created, changed, or
deleted.

## Decision

REG.RU has two overlapping management surfaces, but they are not
interchangeable:

1. The S3-compatible endpoint is the supported route for object operations and
   documented S3 configuration operations such as bucket policy and ACL. It
   also advertises `DeleteBucket` as a policy action.
2. The authenticated REG.Cloud GraphQL control plane owns service activation,
   the supported bucket-creation workflow, provider quotas, the panel's
   public/private abstraction, endpoint discovery, and access-key lifecycle.
3. Bucket deletion is exposed by both surfaces at different levels: the panel
   uses a private GraphQL mutation, while REG.RU advertises the S3
   `DeleteBucket` permission. Direct S3 deletion is plausible and provider-
   advertised, but its wire behavior has not been characterized.
4. `CreateBucket` must be treated as **unsupported at the REG.RU S3
   endpoint**. REG.RU's current frontend explicitly lists `CreateBucket` among
   actions unsupported by the current S3 implementation, while its order
   frontend creates buckets through a private GraphQL mutation.

The practical result for `regru` is a hard adapter boundary: S3 credentials
alone are not sufficient for the complete bucket lifecycle.

## Authenticated drift refresh: string-valued size fields

The failure reported by
[Research: refresh the REG.Cloud S3 contract after drift](https://github.com/adinvadim/reg-ru-cli/issues/31)
is an implementation defect, not evidence that the current provider operation
or response field set drifted.

On 2026-08-01, after a fresh login committed the authorized profile, the
production `s3 service show` read reached the S3 control-plane adapter and
returned `private_contract_drift`. A separately reduced execution of the exact
production `regruS3Inventory` selection against the same first-party GraphQL
route returned HTTP 200, a data envelope, zero GraphQL errors, and
`ObjectStore`. The current S3 and order-S3 manifests still referenced the same
content-hashed operation bundles characterized by this note: `584...`,
`144...`, and `170...`. ([current S3 manifest][panel-s3-manifest], [current
S3 common bundle][panel-s3-common-bundle], [current S3 operation
bundle][panel-s3-operation-bundle], [current order-S3
manifest][panel-order-s3-manifest], [current order-S3
bundle][panel-order-s3-bundle])

The authorized result was reduced before inspection. Its retained shape was:

| Boundary | Value-redacted invariant |
| --- | --- |
| Principal selection | `Environments`; exactly one environment selected; no service ID retained |
| Envelope | HTTP 200; `data` present; zero GraphQL errors; no rejected selected field |
| Object store | `__typename: ObjectStore`; all 15 selected keys present; `buckets` and `keypairs` are arrays |
| Lifecycle identifiers and counters | Object-store ID and key-pair ID are positive integer JSON numbers; bucket count/limit, object count, and quota fields retain their expected number-or-null shapes |
| Other lifecycle fields | Status and timestamps are strings; lock and versioning flags are booleans; bucket name is a string; no values retained |
| Size fields | Both `ObjectStore.size` and `Bucket.size` are JSON strings; no size value retained |

That last row violates the local decoder rather than the provider selection.
The inventory decoder declares its raw object-store `Size` as `int64`, while
the shared `Bucket.Size` and `ObjectStore.Size` fields are also `int64`.
`encoding/json` rejects a JSON string for those destinations; the adapter then
wraps that decode failure as `PortalContract`, which the executor publishes as
`private_contract_drift`. ([control-plane decoder](../../internal/provider/s3/control_plane.go),
[S3 types](../../internal/provider/s3/types.go), [S3 error
translation](../../internal/provider/s3/executor.go))

The safe minimal typed contract change is therefore narrow: model both
provider `size` fields as an opaque string-backed type (or plain `string`) in
the raw and public inventory structs. Do not coerce them to `int64` and do not
weaken the existing positive-ID, array, typename, count, quota, lock, or
lifecycle-field checks. Current lifecycle reconciliation does not compare
size, so preserving the provider text restores decoding without changing a
mutation postcondition. If numeric size arithmetic becomes a product
requirement, characterize the string grammar and units separately before
adding a lossless parser.

This refresh establishes the observed scalar kind, not a versioned public API
promise. A future non-string size, missing required lifecycle field, unknown
typename, GraphQL error, or malformed array remains provider contract drift
and must continue to fail closed.

## Operation matrix

| Operation | S3-compatible surface | REG.Cloud control plane | Contract for `regru` |
| --- | --- | --- | --- |
| Activate object storage | No standard S3 equivalent | `createObjectStore` | Portal-only; do not attempt with S3 keys |
| Create bucket | Explicitly unsupported by REG.RU's current S3 implementation | `createBucket(name, objectStoreId, isPublic, quotaGb)` | Portal-only supported path |
| Delete bucket | REG.RU advertises `DeleteBucket`; exact wire behavior untested | `deleteBucket(name, objectStoreId, force)` | Portal path confirmed; direct S3 path experimental |
| Set simple public/private mode | Raw S3 policy/ACL can affect access, but no REG.RU S3 API contract for this provider-level toggle | `manageBucketPrivacy(name, objectStoreId, isPublic)` | Keep as a portal abstraction |
| Manage bucket policy | REG.RU documents `PutBucketPolicy`; policy action catalogue includes get/put/delete | Panel also has statement-level GraphQL queries and mutations | Prefer S3 for raw policy; do not pretend the panel's named statements are the same interface |
| Manage ACL | REG.RU documents object ACL and advertises bucket/object ACL actions | No provider lifecycle dependency established | S3 surface |
| Set per-bucket quota | No operation in the S3 API catalogue | `manageBucketQuota(name, objectStoreId, quotaGb)` | Portal-only |
| Set storage-wide quota | No operation in the S3 API catalogue | `manageObjectStoreQuota(objectStoreId, quotaGb)` | Portal-only |
| Discover endpoint and provider links | S3 has no provider bootstrap/discovery call | `objectStore` returns server/link metadata | Portal or explicit configuration |
| List/reveal key pairs | S3 uses keys as request credentials; it does not provision them | `objectStoreKeyPairs` / `objectStoreKeyPair` | Portal-only, secret-bearing |
| Create/reset/delete key pairs | Not S3 operations | `createObjectStoreKeyPair`, `resetObjectStoreKeyPair`, `deleteObjectStoreKeyPair` | Portal-only, security-sensitive |

The official S3 action catalogue contains bucket creation, deletion, policy,
ACL, and object operations, but no provider service activation, capacity
quota, endpoint discovery, or credential-provisioning operation. That
comparison establishes the protocol boundary, not REG.RU compatibility by
itself. ([Amazon S3 action catalogue][aws-s3-actions])

## Bucket creation: private control plane, not S3

Amazon S3 defines `CreateBucket` as an authenticated REST operation, so an AWS
SDK will naturally offer it. ([Amazon `CreateBucket` API][aws-create-bucket])
That generic client capability is not evidence that REG.RU implements it.

REG.RU's current S3 frontend contains an explicit unsupported-action list:
`CreateBucket`, `GetBucketNotification`, `GetBucketRequestPayment`,
`GetReplicationConfiguration`, `PutBucketRequestPayment`,
`PutReplicationConfiguration`, and `DeleteReplicationConfiguration`. The
adjacent UI copy says such an action is unsupported by the current S3
implementation and will be ignored. ([current REG.Cloud S3 operation
bundle][panel-s3-operation-bundle])

The supported panel workflow instead loads a separate order-S3 application.
That application sends this private GraphQL operation:

```graphql
mutation createBucket(
  $name: String!
  $objectStoreId: Int!
  $isPublic: Boolean!
  $quotaGb: Int = null
)
```

Its result is a provider `Bucket` or one of
`ObjectStoreNotFound`, `InvalidBucketName`, `BucketNameConflict`,
`BucketLimitExceeded`, or `Unauthorized`. The same bundle builds the Apollo
client with `credentials: "include"`, making this an authenticated panel
operation rather than an S3 request signed with an access key. ([current
REG.Cloud order-S3 bundle][panel-order-s3-bundle])

The official REG.Cloud guide matches the code: the user logs into the cloud
panel, supplies a name, optional maximum size, and access type, then presses
**Create bucket**. It does not show `aws s3api create-bucket` or another direct
S3 call. ([REG.Cloud storage ordering and management guide][reg-management])

**Supported boundary:** `regru` may create a bucket only through an explicitly
enabled portal-control-plane adapter, or direct the user to the panel.

**Unsupported boundary:** it must not expose access-key-only bucket creation,
silently try `CreateBucket`, or infer AWS location, ACL, ownership, and public-
access-block semantics.

## Bucket deletion: dual evidence, unequal confidence

The current panel deletes a bucket with:

```graphql
mutation deleteBucket(
  $name: String!
  $objectStoreId: Int!
  $force: Boolean! = false
)
```

The result distinguishes `BucketIsNotEmpty`, `BucketNotFound`,
`InvalidBucketName`, `ObjectStoreNotFound`, and `Unauthorized`. This proves
the panel's supported private path and shows that ordinary deletion defaults
to non-forced behavior. It does not establish what `force: true` removes or
whether it is recoverable. ([current REG.Cloud S3 operation
bundle][panel-s3-operation-bundle])

Separately, REG.RU's access-policy catalogue names `DeleteBucket` among its S3
actions. The official S3 protocol defines `DeleteBucket` and requires an empty
bucket. ([REG.Cloud access policies][reg-policies], [Amazon `DeleteBucket`
API][aws-delete-bucket]) The REG.Cloud management guide also documents panel
deletion. ([REG.Cloud storage ordering and management guide][reg-management])

This is enough to call direct S3 deletion **provider-advertised**, but not
enough to promise exact request signing, error XML, version/delete-marker
handling, multipart handling, or idempotency. No authenticated request was
made in this research.

## Access mode and policy are related but distinct

REG.RU documents direct S3 policy management. Its authenticated-access guide
uses `aws s3api put-bucket-policy`, and its policy catalogue names
`GetBucketPolicy`, `PutBucketPolicy`, and `DeleteBucketPolicy`. It also names
bucket, object, and object-version ACL get/put actions. These are supported
S3-level authorization capabilities. ([REG.Cloud access methods][reg-access],
[REG.Cloud access policies][reg-policies])

The panel's simplified access selector is a separate provider abstraction:

```graphql
mutation manageBucketPrivacy(
  $name: String!
  $objectStoreId: Int!
  $isPublic: Boolean!
)
```

The current UI describes three states: private “by keys,” public read-only,
and custom. “Custom” appears when user policy rules do not fit the simple
toggle. The panel also exposes statement-level GraphQL operations to query,
validate, create, update, and delete named policy statements. ([current
REG.Cloud S3 common bundle][panel-s3-common-bundle], [current REG.Cloud S3
operation bundle][panel-s3-operation-bundle])

The implementation mapping is intentionally unknown. The public bundle does
not prove whether `manageBucketPrivacy` writes a bucket policy, ACL, both, or
other provider metadata. Therefore:

- raw S3 policy and ACL commands belong to the S3 adapter;
- the public/private convenience toggle belongs to the portal adapter;
- changing either surface should refresh/re-read access state before assuming
  the other still has the previous value;
- a client must not translate the provider's “public” mode into an arbitrary
  AWS canned ACL without evidence.

## Quota and service activation are provider-only

The current panel exposes two quota mutations:

```graphql
manageBucketQuota(name, objectStoreId, quotaGb)
manageObjectStoreQuota(objectStoreId, quotaGb)
```

The bucket value may be null; the storage-wide value is required. The result
types include provider-specific errors such as `InvalidQuota` and
`UnavailableQuota`. ([current REG.Cloud S3 operation
bundle][panel-s3-operation-bundle])

REG.Cloud's guide likewise manages both values in the panel: storage starts
with a provider quota, and each bucket can have its own maximum bounded by
the storage quota. ([REG.Cloud storage ordering and management
guide][reg-management]) The official S3 operation catalogue has no equivalent
capacity-control operation. ([Amazon S3 action catalogue][aws-s3-actions])

Service activation is also private. The panel's `createObjectStore` mutation
returns an `ObjectStore`, its first key pair, quotas, limits, buckets, and
consumption data, or provider errors such as `NoResourcesAvailable`. The
corresponding `objectStore` query returns status, lock state, quota, maximum
quota, bucket limit, and current buckets. ([current REG.Cloud S3 common
bundle][panel-s3-common-bundle])

The public user guide describes activation through **New resource → S3
storage**, not through the S3 endpoint. ([REG.Cloud ispmanager/S3
guide][reg-key-guide])

Unknowns that remain material:

- when activation becomes billable;
- whether `createObjectStore` is idempotent;
- suspension/resumption and deactivation mutations;
- quota rounding, minimums, asynchronous completion, and concurrency;
- the exact authorization and error behavior for locked environments.

## Endpoint discovery is control-plane metadata

REG.RU tells users to copy `S3 API Endpoint`, `Access key ID`, and
`Secret access key` from a key set in the panel. It currently documents
`https://s3.regru.cloud` and path-style object links. ([REG.Cloud access
methods][reg-access])

The `objectStore` GraphQL query returns provider metadata including a key
pair's `server` and each bucket's `pathStyleLink` and
`virtualHostedStyleLink`; the activation mutation returns the same server/link
family. ([current REG.Cloud S3 common bundle][panel-s3-common-bundle])

There is no endpoint-discovery operation in the S3 REST action catalogue.
([Amazon S3 action catalogue][aws-s3-actions]) The CLI should therefore accept
an explicit endpoint and default to the currently documented one. Portal
discovery may improve setup, but it must be treated as a private, changeable
adapter rather than an S3 guarantee.

## Access-key lifecycle is control-plane-only

The current panel frontend defines:

- `objectStoreKeyPairs(page, pageSize)` and
  `objectStoreKeyPair(keyPairId)`;
- `createObjectStoreKeyPair(name)`;
- `resetObjectStoreKeyPair(keyPairId)`;
- `deleteObjectStoreKeyPair(keyPairId)`.

The query and mutation selections include key-pair metadata plus
`accessKey` and `secretKey`. This proves that the current panel can retrieve
existing secret material and rotate or revoke a named pair; it does not make
those operations part of the S3 protocol. ([current REG.Cloud S3 common
bundle][panel-s3-common-bundle])

The user guide independently shows named key-set creation and later opening a
set to view its parameters. ([REG.Cloud ispmanager/S3 guide][reg-key-guide])
In the S3 protocol, these values authenticate requests; the official S3 action
catalogue does not provision them. ([Amazon S3 action
catalogue][aws-s3-actions])

Still unknown:

- whether keys are account-, object-store-, or environment-wide in every
  account configuration;
- per-key policy/scopes, expiry, maximum active sets, and rotation grace;
- how quickly delete/reset revocation propagates;
- whether every user role may retrieve unmasked existing secrets;
- stable private-API authentication, CSRF, rate-limit, and error contracts.

## Private API stability boundary

The GraphQL operation documents are first-party evidence of what the current
panel does, not a published API commitment. The panel client uses cookies and
an environment `Service-ID` header, and its endpoint/schema may change without
the compatibility guarantees expected from a public API. ([current REG.Cloud
panel GraphQL client bundle][panel-graphql-client])

Accordingly, a portal adapter should:

1. be capability-detected and version-tolerant;
2. preserve provider `__typename` errors rather than flattening them;
3. require explicit confirmation for activation, bucket creation/deletion,
   quota/access changes, and key creation/reset/deletion;
4. never log key material or GraphQL response bodies that may contain it;
5. fail closed when a mutation disappears or changes shape;
6. provide a manual-panel fallback.

## Recommended `regru` module boundary

`regru` should model two clients rather than one oversized “S3 client”:

- `S3DataPlane`: signed S3 requests for supported bucket/object/configuration
  operations. Raw policy and ACL live here. Direct `DeleteBucket` remains
  experimental until characterized.
- `RegCloudS3ControlPlane`: authenticated portal operations for activation,
  supported bucket creation, quotas, simplified privacy state, endpoint
  discovery, key lifecycle, and the panel deletion workflow.

The bucket-create command should report a clear capability error when only S3
keys are configured: REG.RU's supported creation path requires a portal
session. It must not hide that distinction behind an AWS SDK retry or a
fallback that mutates policy after a failed `CreateBucket`.

## Required characterization before stronger claims

The following read/write tests require separately authorized disposable
resources and were deliberately not performed:

- direct signed `CreateBucket` should only be tested to confirm the documented
  unsupported result, never used as a production fallback;
- direct signed `DeleteBucket` on empty and non-empty disposable buckets;
- how private/public/custom states map to S3 policy and ACL documents;
- interaction between panel statement mutations and raw `PutBucketPolicy`;
- quota boundary, asynchronous, and concurrent-update behavior;
- activation/deactivation billing and lifecycle;
- key reset/delete revocation timing and scope;
- private GraphQL auth, CSRF, pagination, error, and compatibility behavior
  using redacted captures.

Until then, the supported boundary is asymmetric: use S3 for the documented
data/configuration plane and use the REG.Cloud portal control plane for
provider lifecycle.

## Primary sources

- [REG.Cloud storage ordering and management guide][reg-management]
- [REG.Cloud access methods][reg-access]
- [REG.Cloud access policies][reg-policies]
- [REG.Cloud ispmanager/S3 key and activation guide][reg-key-guide]
- [Current REG.Cloud S3 federation manifest][panel-s3-manifest]
- [Current REG.Cloud S3 common GraphQL bundle][panel-s3-common-bundle]
- [Current REG.Cloud S3 operation GraphQL bundle][panel-s3-operation-bundle]
- [Current REG.Cloud order-S3 federation manifest][panel-order-s3-manifest]
- [Current REG.Cloud order-S3 create-bucket bundle][panel-order-s3-bundle]
- [Current REG.Cloud panel GraphQL client bundle][panel-graphql-client]
- [Amazon S3 action catalogue][aws-s3-actions]
- [Amazon `CreateBucket` API][aws-create-bucket]
- [Amazon `DeleteBucket` API][aws-delete-bucket]

[reg-management]: https://reg.cloud/support/cloud/obyektnoye-khranilishche-s3/zakaz-i-upravlenie-uslugoj-obektnoe-hranilishche-s3/zakaz-i-upravleniye-obyektnym-khranilishchem-s3
[reg-access]: https://reg.cloud/support/instrukcii/obektnoe-hranilishe-s3/sposoby-dostupa-k-faylam-v-s3
[reg-policies]: https://reg.cloud/support/cloud/obyektnoye-khranilishche-s3/politiki-dostupa-v-hranilishe-s3
[reg-key-guide]: https://reg.cloud/support/instrukcii/obektnoe-hranilishe-s3/kak-nastroit-backup-dannyh-s-servera-s-ispmanager-v-hranilishche-s3
[panel-s3-manifest]: https://cloudvps-static.svc.reg.ru/s3/mf-manifest.json
[panel-s3-common-bundle]: https://cloudvps-static.svc.reg.ru/s3/584.cc0c4d3c15727ef94da9.js
[panel-s3-operation-bundle]: https://cloudvps-static.svc.reg.ru/s3/144.164eaa226a1357642e37.js
[panel-order-s3-manifest]: https://cloudvps-static.svc.reg.ru/order-s3/mf-manifest.json
[panel-order-s3-bundle]: https://cloudvps-static.svc.reg.ru/order-s3/170.1dcfbda891d9cc726a6a.js
[panel-graphql-client]: https://cloudvps-static.svc.reg.ru/panel/__federation_expose_panel.230377f66688d4eeb56c.js
[aws-s3-actions]: https://docs.aws.amazon.com/AmazonS3/latest/API/API_Operations.html
[aws-create-bucket]: https://docs.aws.amazon.com/AmazonS3/latest/API/API_CreateBucket.html
[aws-delete-bucket]: https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteBucket.html
