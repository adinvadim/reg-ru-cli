# REG.RU S3 compatibility contract

Research date: 2026-07-30

## Scope and evidence standard

This note answers which S3-compatible behavior `regru` can rely on from REG.RU's own documentation and public endpoint. It deliberately does not import the Amazon S3 contract wholesale. A feature is treated as supported only when REG.RU documents it or its public endpoint exposes it without authentication. Generic AWS SDK behavior is marked as an inference or gap.

No credentials were read or used. The only live checks were unauthenticated `GET` requests to `https://s3.regru.cloud/` and to a deliberately nonexistent path.

## Decision summary

The stable, documented connection profile is:

- HTTPS endpoint: `https://s3.regru.cloud`.
- Region: REG.RU tells AWS CLI users to leave it blank, tells Nextcloud users to leave it blank, and tells ispmanager users to auto-detect it. There is no documented REG.RU signing-region string.
- Credentials: an Access Key ID and Secret Access Key generated in the REG.RU cloud panel.
- Addressing: path-style is explicitly documented (`https://s3.regru.cloud/<bucket>/<key>`) and is the only safe API default. REG.RU separately documents virtual-hosted URLs for its website-hosting endpoint; that does not establish `<bucket>.s3.regru.cloud` as an API contract.
- Client compatibility: REG.RU explicitly documents AWS CLI and S3 API usage, but does not publish a versioned compatibility matrix.

The central unresolved item is authentication detail. REG.RU documents AWS access keys and policy condition keys related to signature version and `x-amz-content-sha256`, but does **not** say that SigV4 is required, name the credential-scope region, or describe payload-signing modes. Therefore `regru` cannot yet claim a self-contained SigV4 contract from public documentation alone. An authenticated characterization test with a disposable bucket is required before hard-coding a signing region or promising upload/download interoperability.

Sources: [access methods and AWS CLI setup](https://reg.cloud/support/instrukcii/obektnoe-hranilishe-s3/sposoby-dostupa-k-faylam-v-s3), [Nextcloud connection settings](https://reg.cloud/support/cloud/obyektnoye-khranilishche-s3/podklyuchenie-hranilisha-s3-k-nextcloud), [ispmanager connection settings](https://reg.cloud/support/instrukcii/obektnoe-hranilishe-s3/kak-nastroit-backup-dannyh-s-servera-s-ispmanager-v-hranilishche-s3), [access-policy action and condition catalogue](https://reg.cloud/support/cloud/obyektnoye-khranilishche-s3/politiki-dostupa-v-hranilishe-s3).

## Connection and authentication

| Concern | Confirmed REG.RU behavior | Contract status |
| --- | --- | --- |
| Endpoint | REG.RU examples use `https://s3.regru.cloud`; the panel also exposes an `S3 API Endpoint` value. | Supported. Default to this value, but permit an explicit endpoint override because REG.RU tells users to copy the panel-provided endpoint. |
| Transport | Documented endpoint is HTTPS. REG.RU has a troubleshooting article for AWS CLI CA validation. | Supported. Certificate verification must remain enabled; a CA-bundle override is reasonable. |
| Region | AWS CLI and Nextcloud instructions say to leave the region empty. ispmanager is told to auto-detect it. | No named region contract. Do not make the user invent one or expose a REG.RU-specific region enum. |
| Credentials | Private access uses Access Key ID plus Secret Access Key. | Supported. |
| Signature version | Policy conditions include `s3:signatureversion`, `s3:signatureAge`, and `s3:x-amz-content-sha256`. | Evidence that signature metadata exists, but neither accepted values nor a required algorithm are documented. SigV4 remains unconfirmed. |
| Session tokens | No REG.RU source found. | Unsupported/unknown. |
| Streaming/chunked signing and `UNSIGNED-PAYLOAD` | No REG.RU source found. | Unsupported/unknown. |

REG.RU's CA troubleshooting example shows an AWS CLI request shaped as `...?list-type=2&prefix=&delimiter=%2F&encoding-type=url`, but the article concerns TLS validation, not successful ListObjectsV2 semantics. It is useful interoperability evidence, not a complete pagination or response-shape promise. Source: [AWS CLI certificate error](https://reg.cloud/support/instrukcii/obektnoe-hranilishe-s3/oshibka-ssl-sertifikata-v-aws-cli).

### Addressing style

REG.RU explicitly labels `https://s3.regru.cloud/<bucket>/<object>` as a **Path-style** link. Its raw multipart abort example is also `DELETE /{bucket}/{key}?uploadId=...`. These are sufficient to make force-path-style the conservative client setting. Sources: [access methods](https://reg.cloud/support/instrukcii/obektnoe-hranilishe-s3/sposoby-dostupa-k-faylam-v-s3), [aborting incomplete multipart uploads](https://reg.cloud/support/instrukcii/obektnoe-hranilishe-s3/udalenie-chastichno-zagruzhennogo-objekta).

REG.RU also documents website-hosting URLs and a CNAME target of `<bucket>.website.regru.cloud`. That is a separate website endpoint, including a warning that custom domains currently support only HTTP, not TLS. It must not be generalized into standard S3 API virtual-host addressing. Sources: [website mode](https://reg.cloud/support/instrukcii/obektnoe-hranilishe-s3/nastrojka-website-dlya-baketa-v-hranilishe-s3), [custom domain for a bucket](https://reg.cloud/support/instrukcii/obektnoe-hranilishe-s3/podklyuchenie-sobstvennogo-domena-k-baketu-v-hranilishe-s3).

## Bucket and object operation surface

REG.RU's access-policy UI publishes the following action names. This is the strongest first-party inventory of service capabilities, but an action name establishes an authorization surface, not exact AWS wire-level equivalence.

| Area | Explicitly named by REG.RU |
| --- | --- |
| Core bucket/object | `ListAllMyBuckets`, `ListBucket`, `GetObject`, `PutObject`, `DeleteObject`, `DeleteBucket` |
| Object versions | `ListBucketVersions`, `GetObjectVersion`, `DeleteObjectVersion` |
| Bucket policy | `GetBucketPolicy`, `PutBucketPolicy`, `DeleteBucketPolicy` |
| ACL | `GetBucketAcl`, `PutBucketAcl`, `GetObjectAcl`, `PutObjectAcl`, `GetObjectVersionAcl`, `PutObjectVersionAcl` |
| Bucket metadata/configuration | `GetBucketLocation`, `GetBucketLogging`, `PutBucketLogging`, `GetBucketTagging`, `PutBucketTagging` |
| Website | `GetBucketWebsite`, `PutBucketWebsite`, `DeleteBucketWebsite` |
| CORS | `GetBucketCORS`, `PutBucketCORS` (and the separate CORS guide explicitly documents deletion) |
| Versioning | `GetBucketVersioning`, `PutBucketVersioning` |
| Lifecycle | `GetLifecycleConfiguration`, `PutLifecycleConfiguration` |
| Multipart | `AbortMultipartUpload`, `ListBucketMultipartUploads`, `ListMultipartUploadParts` |
| Other named capabilities | `GetAccelerateConfiguration`, `PutAccelerateConfiguration`, `PutBucketNotification`, `GetObjectTorrent`, `GetObjectVersionTorrent`, `RestoreObject` |

The same policy catalogue exposes conditions for prefix, delimiter, max keys, version ID, copy source, metadata directive, server-side encryption, storage class, authentication type, signature version/age, secure transport, source IP, referrer, user agent, user identity, and time. These condition names show that the service evaluates those request attributes; they do not prove that every corresponding AWS feature or header variant is accepted. Source: [REG.RU access policies](https://reg.cloud/support/cloud/obyektnoye-khranilishche-s3/politiki-dostupa-v-hranilishe-s3).

The management guide confirms creating and deleting buckets in the REG.RU panel, changing their quota, and choosing public or key-authenticated access. It says a bucket may contain any number of objects, but does not publish a numeric bucket/object-count cap. API-level `CreateBucket` is not explicitly documented, and `CreateBucket` is absent from the policy-action inventory; `regru` should not claim it until authenticated testing or first-party API documentation confirms it. Source: [ordering and managing S3 storage](https://reg.cloud/support/cloud/obyektnoye-khranilishche-s3/zakaz-i-upravlenie-uslugoj-obektnoe-hranilishche-s3/zakaz-i-upravleniye-obyektnym-khranilishchem-s3).

Not established by REG.RU sources: `HeadBucket`, `HeadObject`, batch `DeleteObjects`, copy semantics, conditional requests, byte ranges, object-lock/retention/legal-hold APIs, event-notification read/delete operations, checksums, tagging deletion, select, replication configuration, exact consistency behavior, or ETag meaning. Standard AWS behavior for these must not be assumed.

## Listing and pagination

Confirmed:

- `ListAllMyBuckets`, `ListBucket`, `ListBucketVersions`, `ListBucketMultipartUploads`, and `ListMultipartUploadParts` exist in the policy action catalogue.
- Policy conditions include `s3:prefix`, `s3:delimiter`, and `s3:max-keys`.
- A REG.RU AWS CLI troubleshooting example contains a `list-type=2` request shape.

Not documented:

- whether list v1, list v2, or both are supported;
- response XML fields and ordering;
- default or maximum page size;
- marker, continuation-token, version-marker, upload-id-marker, and part-number-marker behavior;
- whether continuation values are stable or reusable.

Consequently, `regru` may expose prefix, delimiter, and max-keys only as pass-through concepts. It should treat any returned cursor as opaque and should not promise a pagination dialect until an authenticated test captures multi-page responses. Sources: [access policies](https://reg.cloud/support/cloud/obyektnoye-khranilishche-s3/politiki-dostupa-v-hranilishe-s3), [AWS CLI certificate error](https://reg.cloud/support/instrukcii/obektnoe-hranilishe-s3/oshibka-ssl-sertifikata-v-aws-cli).

## Multipart transfers

REG.RU explicitly says its storage supports parallel and multithreaded uploads. It documents listing incomplete multipart uploads and aborting one with both AWS CLI and a raw path-style HTTP request:

```http
DELETE /{bucket}/{key}?uploadId={uploadId} HTTP/2
```

The policy catalogue also names multipart-upload and multipart-part listing permissions. Sources: [incomplete multipart upload cleanup](https://reg.cloud/support/instrukcii/obektnoe-hranilishe-s3/udalenie-chastichno-zagruzhennogo-objekta), [access policies](https://reg.cloud/support/cloud/obyektnoye-khranilishche-s3/politiki-dostupa-v-hranilishe-s3).

REG.RU does not document create/upload-part/complete request shapes, minimum or maximum part sizes, maximum part count, maximum object size, checksum behavior, multipart ETags, retry/idempotency behavior, or the threshold at which a client should switch to multipart. Therefore the safe contract is “multipart is present and incomplete uploads can be listed/aborted,” not “all AWS multipart constraints apply.” Upload implementation needs authenticated interoperability tests before release.

## Presigned URLs

REG.RU explicitly documents object presigning through:

```text
aws s3 presign s3://<bucket>/<object>
aws s3 presign s3://<bucket>/<object> --expires-in <seconds>
```

It states that the default lifetime is 3,600 seconds and that a custom lifetime can be supplied. This supports presigned object-download URLs. It does not document a maximum lifetime, presigned PUT/POST uploads, signed headers, response-header overrides, or whether presigning must use SigV4. Source: [access methods](https://reg.cloud/support/instrukcii/obektnoe-hranilishe-s3/sposoby-dostupa-k-faylam-v-s3).

## ACL, CORS, lifecycle, and versioning

### ACL

REG.RU documents `put-object-acl --acl public-read` and publishes bucket, object, and object-version ACL read/write action names. This confirms ACL support at those scopes. The article delegates the full canned-ACL list to AWS documentation, so only `public-read` is directly confirmed by REG.RU; other canned ACLs and grant-header/XML details remain unconfirmed. Sources: [access methods](https://reg.cloud/support/instrukcii/obektnoe-hranilishe-s3/sposoby-dostupa-k-faylam-v-s3), [access policies](https://reg.cloud/support/cloud/obyektnoye-khranilishche-s3/politiki-dostupa-v-hranilishe-s3).

### CORS

REG.RU documents get/put/delete CORS workflows through AWS CLI and says CORS rules support `GET`, `PUT`, `POST`, `DELETE`, `OPTIONS`, and `HEAD`. A newly created bucket receives a rule allowing `GET`, `PUT`, `DELETE`, and `POST` from `https://cloud.reg.ru` with `AllowedHeaders: ["*"]` and `MaxAgeSeconds: 3000`. REG.RU warns that removing this rule prevents the cloud panel from managing objects. A client that replaces CORS configuration must preserve that rule unless the user explicitly accepts loss of panel access. Sources: [CORS management](https://reg.cloud/support/cloud/obyektnoye-khranilishche-s3/zakaz-i-upravlenie-uslugoj-obektnoe-hranilishche-s3/upravlenie-cors-policy-v-hranilishe-s3), [CORS setup](https://reg.cloud/support/cloud/obyektnoye-khranilishche-s3/nastrojka-cors-dlya-dostupa-k-obektam-s3).

### Lifecycle

REG.RU's policy catalogue names lifecycle get/put operations, and its first-party backup product page says the S3 service supports lifecycle policies for archiving and deleting stale data. No first-party source found in this research specifies the lifecycle document schema, supported filters/actions, transition storage classes, timing granularity, noncurrent-version handling, abort-incomplete-multipart rules, or lifecycle deletion API. Treat lifecycle as a confirmed capability family with an unconfirmed detailed contract. Sources: [access policies](https://reg.cloud/support/cloud/obyektnoye-khranilishche-s3/politiki-dostupa-v-hranilishe-s3), [REG.RU backup solutions](https://reg.cloud/dedicated/backup).

### Versioning

REG.RU's public documentation index includes a dedicated versioning guide. More concretely, its policy catalogue names get/put bucket versioning, list versions, get/delete a specific version, and get/put ACL for a specific version. This confirms version-aware operations. It does not establish enable/suspend response semantics, delete markers, `null` versions, version-ID format, pagination markers, or how version storage is billed. Do not import those details from AWS without testing. Sources: [S3 documentation index](https://reg.cloud/support/cloud/obyektnoye-khranilishche-s3/), [access policies](https://reg.cloud/support/cloud/obyektnoye-khranilishche-s3/politiki-dostupa-v-hranilishe-s3).

## Error contract and retry behavior

REG.RU publishes one client-side failure mode: AWS CLI may report `SSL validation failed` because its bundled CA set is stale or the root CA path is wrong. REG.RU recommends supplying a current CA bundle. This is not a service error and must not be “fixed” by disabling TLS verification. Source: [AWS CLI certificate error](https://reg.cloud/support/instrukcii/obektnoe-hranilishe-s3/oshibka-ssl-sertifikata-v-aws-cli).

An unauthenticated observation on 2026-07-30 against a deliberately nonexistent path returned:

```http
HTTP/1.1 404 Not Found
Content-Type: application/xml
x-amz-request-id: tx...

<Error>
  <Code>NoSuchBucket</Code>
  <Message></Message>
  <BucketName>wayfinder-research-nonexistent-bucket-20260730</BucketName>
  <RequestId>tx...</RequestId>
  <HostId>...</HostId>
</Error>
```

The public root endpoint returned HTTP 200 with an XML `ListAllMyBucketsResult` whose owner was `anonymous` and whose bucket list was empty. These observations establish S3-shaped XML envelopes and request IDs for those two unauthenticated cases only; they are not a versioned provider guarantee. Endpoint: [s3.regru.cloud](https://s3.regru.cloud/).

No first-party error-code catalogue, throttling status/code, retry-after behavior, retryable-code list, or idempotency guarantee was found. `regru` should preserve HTTP status, `x-amz-request-id`, parsed XML `Code`, optional `Message`, and raw provider details. Retry policy must remain conservative: retry transport failures and standard transient HTTP statuses only where the operation itself is safely retryable; do not map every AWS S3 error code as if REG.RU had promised it.

## Published service limits

| Limit | Published value |
| --- | --- |
| Read operations per bucket | 1,000 operations/second |
| Write operations per bucket | 500 operations/second |
| Read operations per user | 10,000 operations/second |
| Write operations per user | 5,000 operations/second |
| Initial storage quota | 10 GB |
| Self-service storage quota ceiling | 20 TB as of 2026-03-06, raised from 500 GB |
| Per-bucket quota | User-configurable, bounded by the storage quota |

Sources: [read/write operation limits](https://reg.cloud/support/cloud/obyektnoye-khranilishche-s3/ogranicheniya-na-kolichestvo-operacij-chteniya-i-zapisi-v-hranilishe-s3), [ordering and quota management](https://reg.cloud/support/cloud/obyektnoye-khranilishche-s3/zakaz-i-upravlenie-uslugoj-obektnoe-hranilishche-s3/zakaz-i-upravleniye-obyektnym-khranilishchem-s3), [20 TB quota announcement, 2026-03-06](https://www.reg.ru/company/news/12891).

The docs do not state whether “read” and “write” include list, head, delete, multipart-part, or configuration requests, nor the response when a rate is exceeded. They also do not publish maximum object size, single-PUT size, part size/count, number of buckets, key length, metadata size, policy size, CORS rule count, lifecycle rule count, presigned-URL lifetime, concurrent connections, bandwidth, or egress/request quotas. AWS limits are not substitutes.

## Recommended contract for `regru`

1. Model the endpoint as configurable, defaulting to `https://s3.regru.cloud`.
2. Force path-style addressing for the API. Keep website endpoints separate.
3. Do not require a user-visible region. Internally, do not choose a signing-region string until an authenticated characterization test identifies one.
4. Treat Access Key ID and Secret Access Key as the only documented credential form.
5. Limit any documentation claim to the operation families REG.RU names. Label untested AWS SDK calls experimental rather than “S3 compatible.”
6. Preserve opaque pagination tokens and provider error fields.
7. Support presigned downloads only after signing is characterized; default expiry may be 3,600 seconds. Do not claim presigned upload support or a maximum expiry.
8. Preserve the mandatory REG.RU panel CORS origin rule on edits by default.
9. Enforce or at least surface the documented per-bucket and per-user operation rates, while allowing the service to remain authoritative.
10. Gate upload, multipart, pagination, lifecycle, versioning, and SigV4 claims on a small authenticated compatibility suite using disposable resources.

## Required authenticated characterization before implementation claims

The public contract leaves a compact but important test matrix:

- accepted SigV4 credential scope, especially the region string; header-signed versus query-signed requests; payload hash modes;
- path-style list/get/put/delete and whether head/copy/batch delete work;
- multi-page ListObjectsV2, versions, and multipart lists with opaque cursor round trips;
- single-part and multipart upload thresholds, part constraints, checksums, completion, abort, and retry behavior;
- presigned GET expiry bounds and any PUT/POST support;
- ACL/CORS/lifecycle/versioning request and response shapes;
- rate-limit and representative authentication/authorization/not-found errors.

Until those tests run, the documented contract is sufficient to configure an AWS-compatible client but not sufficient to reimplement or promise the complete AWS S3 protocol.
