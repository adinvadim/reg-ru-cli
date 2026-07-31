# CloudVPS API implementation contract for VPS commands

Research date: 2026-07-31

This note turns the first-party REG.RU CloudVPS contract into implementation
rules for
[Task: implement CloudVPS and VPS commands](https://github.com/adinvadim/reg-ru-cli/issues/5).
It covers only that ticket's public REST surface. The repository currently
exposes only placeholder VPS commands
([CLI contract](../cli-contract.md), [command tree](../../internal/cli/commands.go));
there is no existing CloudVPS transport implementation to preserve.

The authoritative wire snapshots inspected for this note were:

- [v1 OpenAPI](https://api.cloudvps.reg.ru/v1/openapi.json), SHA-256
  `f1acbebaee7de77f372826125de175866e60688b1d5ed143c18556f0b1a6d1e3`;
- [v2 OpenAPI](https://api.cloudvps.reg.ru/v2/api/swagger.json), SHA-256
  `83271d469c10cc6356c4670e9820c1415d9bd6ac0ecddf71114dc1e9a307ed95`.

The hashes anchor the exact schemas read on the research date; the URLs are
live and can change.

## Evidence and implementation rule

**Verified** below means the current OpenAPI, first-party HTML documentation,
or an unauthenticated response from the official endpoint states or exhibits
the fact. **Inference** means a client policy derived from those facts, not a
REG.RU guarantee. **Gap / fail closed** means the public contract is
insufficient to claim support safely.

The current OpenAPI is the strict source for outgoing JSON types and declared
status codes. The first-party HTML pages supply action-specific required fields
that the generic OpenAPI action schema omits. Incoming decoders must accept
only the explicitly documented envelope/type variants recorded here, then
normalize them into strict internal types. A missing identity, an unknown
success envelope, or an unknown terminal state must never be treated as
success.

## Transport and authentication

### Verified

- Origin: `https://api.cloudvps.reg.ru`.
- Resource management uses `/v1`; the current catalog uses `/v2`.
  The v1 OpenAPI declares server URL `/v1` with relative paths. The v2
  OpenAPI declares full paths `/v2/plans` and `/v2/images`.
- Send `Authorization: Bearer <token>`. REG.RU documents bearer token
  authentication as the only current method, says the token authorizes every
  operation on the Cloud Servers service, and exposes token viewing/rotation
  in the service's Settings tab
  ([authentication](https://developers.cloudvps.reg.ru/getting-started/authentication.html)).
- Both catalog endpoints and all ticket-relevant v1 endpoints declare
  `bearerAuth`. JSON mutations use `Content-Type: application/json`.
- No token format, expiry, scopes, multiple-token model, or public
  token-management endpoint is documented.

### Implementation inference

Use separate typed `v1` and `v2` clients sharing one origin, bearer transport,
timeout, and error decoder. Set `Accept: application/json`; set
`Content-Type` only when a body exists. Build query strings with `url.Values`
and escape every path segment rather than interpolating raw IPs, fingerprints,
or action IDs. Never put the token in argv, URLs, logs, plans, or result
envelopes.

An endpoint override, if the CLI eventually has one, must be an explicit
test/development dependency and not account-controlled project configuration.

## Endpoint and success-status matrix

The following is the complete public surface needed by the ticket.

| Capability | Method and path | Canonical success |
| --- | --- | --- |
| VPS list | `GET /v1/reglets` | `200` JSON |
| VPS show | `GET /v1/reglets/{resource_id}` | `200` JSON |
| VPS create/deploy | `POST /v1/reglets` | `201` JSON |
| VPS rename | `PUT /v1/reglets/{resource_id}` | `201` JSON |
| VPS destroy | `DELETE /v1/reglets/{resource_id}` | `204`, empty |
| Start/stop/reboot/rebuild/resize/password reset/clone/snapshot/backup switch/restore | `POST /v1/reglets/{resource_id}/actions` | `201` JSON |
| Action show/wait | `GET /v1/actions/{action_id}` | `200` JSON |
| Additional IP list | `GET /v1/ips[?reglet_id=ID]` | `200` JSON |
| Additional IP add | `POST /v1/ips` | `201` JSON |
| PTR update | `PUT /v1/ips/{ip}` | `201` JSON |
| Additional IP remove | `DELETE /v1/ips/{ip}` | OpenAPI: `200` JSON; HTML: `204`, empty |
| SSH key list/add | `GET`, `POST /v1/account/keys` | `200`, `201` JSON |
| SSH key rename/remove | `PUT`, `DELETE /v1/account/keys/{key_id}` | `200` JSON, `204` empty |
| Snapshot list/remove | `GET /v1/snapshots[?region=REGION]`; `DELETE /v1/snapshots/{image_id}` | `200` JSON, `204` empty |
| Servers using snapshot | `GET /v1/reglets_for_snapshot/{snapshot_id}` | `200` JSON |
| Plan discovery | `GET /v2/plans` | `200` JSON |
| Image/backup discovery | `GET /v2/images` | `200` JSON |

The HTML additional-IP page describes `GET /v1/ips/{ip}`, but that operation
does not exist in the current v1 OpenAPI
([IP documentation](https://developers.cloudvps.reg.ru/add-ip/list.html)).
An `ip show` command can be implemented against the documented list endpoint
and an exact local address match. Calling the undocumented single-IP route
must remain unavailable until an authorized fixture/live-contract test proves
it.

## VPS inventory, create, rename, and destroy

### Create request

`POST /v1/reglets` takes this JSON object
([OpenAPI](https://api.cloudvps.reg.ru/v1/openapi.json),
[server creation](https://developers.cloudvps.reg.ru/reglets/add.html)):

| Field | Wire type | Rule |
| --- | --- | --- |
| `size` | string | Required plan slug from `/v2/plans`. |
| `image` | string or integer | Required image slug, or integer snapshot/image ID. |
| `name` | string | Optional; current `NameString` permits 1–60 characters from a provider-defined allowlist. |
| `ssh_keys` | array of integer or string | Optional key IDs or fingerprints; default `[]`. |
| `backups` | boolean or integer `0`/`1` | Optional. Emit JSON boolean. The legacy HTML string `"true"` example is not in the current schema. |
| `isp_license_size` | string enum | Optional: `isp_lite6`, `isp_pro6`, or `isp_host6`. |
| `region_slug` | string enum | Optional in v1: `msk1`, `openstack-msk1`, `openstack-msk2`, `openstack-msk3`, `openstack-spb1`, `openstack-sam1`, `openstack-fz1`. |
| `floating_ip` | boolean | Optional, OpenAPI default `true`. |
| `promocode` | string | Optional; no format or validation semantics are documented. |

REG.RU explicitly requires catalog discovery through v2 but server creation
through v1. **Inference:** make region explicit in the CLI, fetch the plan and
image for the same region, verify their returned `region_slug`/catalog
membership, and send `region_slug`. Otherwise an optional provider default can
place or reject a billable resource in a way the command did not make clear.

Do not send a catalog `id` as `size`; creation requires the plan `slug`.
For a normal image send its `slug`. For a snapshot send its integer `id`.

### VPS response

Create returns `{ "reglet": Reglet, "links": { "actions": [...] } }`.
Show returns `{ "reglet": RegletWithDiskUsage }`. The current list OpenAPI
spells its array property `reglet`, while the first-party documentation and
examples spell it `reglets`
([server list](https://developers.cloudvps.reg.ru/reglets/list.html)).
The list decoder must accept either property, but not an arbitrary bare array.

The current `Reglet` properties are:

- identity/context: `id` integer, `service_id` integer, `name` string,
  `region_slug` string, `created_at` string;
- state: `status` string, `sub_status` nullable string, `locked` integer,
  `backups_enabled` boolean, `last_backup_date` nullable string;
- capacity: `disk`, `memory`, `vcpus` integers, `size_slug` string, nested
  `size`;
- image: `image_id` integer and nested `image`;
- network: `hostname`, `ip`, `ipv6`, and `ptr`, each nullable where declared;
- private networking: optional `vpcs` integer array.

`GET /v1/reglets/{id}` adds `disk_usage` number inside `reglet`.
Documented statuses in the current schema are `new`, `active`, `ordered`,
`off`, `suspended`, and `archive`.

The v1 sources disagree on legacy scalar types: HTML examples contain
`locked` as `0`/`1`, `backups_enabled` as `"1"`, and nested image `private` as
`0`/`1`, while current schemas use integer, boolean, and boolean respectively.
Decode these named boolean-like fields from boolean, integer `0`/`1`, or string
`"0"`/`"1"` only. Do not apply loose truthiness to other fields.

The schema does not mark `id` required even though every follow-up operation
needs it. **Fail closed:** a returned server without a positive integer
identity is provider contract drift, not a successful create or usable list
entry. Keep v1 timestamps as strings: the API does not declare a stable
timestamp format.

Nested `Size` exposes `id`, `slug`, `name`, `disk`, `memory`, `vcpus`,
`price` string, `price_month` integer, `regions` string array, and `weight`.
Nested v1 `Image` exposes `id`, `slug`, `name`, `type`, `distribution`,
`private`, `region_slug`, `created_at`, `size_gigabytes`,
`min_disk_size`, `isp_license_size`, and recursive `version`. Several numeric
image sizes/prices are strings; preserve their exact decimal text.

### Rename and destroy

- Rename sends `PUT /v1/reglets/{resource_id}` with exactly
  `{ "name": "..." }` and receives the same `reglet` plus
  `links.actions` shape as create
  ([rename](https://developers.cloudvps.reg.ru/reglets/rename.html)).
- Destroy sends `DELETE /v1/reglets/{resource_id}` and succeeds only on
  `204`; it has no response body or documented action ID
  ([delete](https://developers.cloudvps.reg.ru/reglets/delete.html)).

**Inference:** a default waiter has nothing to poll after VPS deletion.
Treat `204` as provider completion. If stronger confirmation is required,
reconcile through the list endpoint; the single-server OpenAPI does not even
declare a `404` response. Never replay create or destroy automatically after an
ambiguous transport failure because no idempotency mechanism is documented.

## Lifecycle action request matrix

All rows send `POST /v1/reglets/{resource_id}/actions`. The generic OpenAPI
requires only `type`; the first-party per-action pages establish the
conditional requirements below.

| Operation | JSON body | Source |
| --- | --- | --- |
| Start | `{ "type": "start" }` | [power](https://developers.cloudvps.reg.ru/reglets/power_on_off.html) |
| Stop | `{ "type": "stop" }` | [power](https://developers.cloudvps.reg.ru/reglets/power_on_off.html) |
| Reboot | `{ "type": "reboot" }` | [reboot](https://developers.cloudvps.reg.ru/reglets/reboot.html) |
| Rebuild from catalog image | `{ "type": "rebuild", "image": "<slug>" }` | [rebuild](https://developers.cloudvps.reg.ru/reglets/rebuild.html) |
| Rebuild from snapshot | `{ "type": "rebuild", "image": <snapshot-id> }` | [rebuild](https://developers.cloudvps.reg.ru/reglets/rebuild.html) |
| Resize | `{ "type": "resize", "size": "<plan-slug>" }` | [resize](https://developers.cloudvps.reg.ru/reglets/resize.html) |
| Reset root password | `{ "type": "password_reset" }` | [password reset](https://developers.cloudvps.reg.ru/reglets/password-reset.html) |
| Clone | `{ "type": "clone", "offline": <bool>, "name": "..." }` | [clone](https://developers.cloudvps.reg.ru/reglets/clone.html) |
| Snapshot | `{ "type": "snapshot", "name": "...", "offline": <bool> }` | [snapshot](https://developers.cloudvps.reg.ru/snapshots/add.html) |
| Enable backups | `{ "type": "enable_backups" }` | [backup switch](https://developers.cloudvps.reg.ru/reglets/switch_backups.html) |
| Disable backups | `{ "type": "disable_backups" }` | [backup switch](https://developers.cloudvps.reg.ru/reglets/switch_backups.html) |
| Restore backup | `{ "type": "restore", "image": <backup-id> }` | [restore](https://developers.cloudvps.reg.ru/reglets/restore.html) |

For clone and snapshot, `offline=true` requests a consistent disk operation
with a server stop; `false` requests one without a stop. The current schema
also accepts integer `0`/`1`; emit a boolean. Snapshot `name` is required by
the action page even though the generic schema does not encode that
dependency. Restore documentation calls `image` a backup ID, while its example
quotes that ID and the OpenAPI allows either integer or string. Use the integer
`id` returned by the catalog.

The current action enum additionally includes `generate_vnc_link` and
`resize_isp_license`; the latter requires `isp_license_size` according to the
[first-party page](https://developers.cloudvps.reg.ru/reglets/isp_license.html).
They are beyond the ticket's named command scope. The generic schema also
allows `ssh_keys`, but no inspected per-action page establishes where it is
valid. Do not expose or send an action/field combination based only on the
union schema.

The resize page says API resize is currently upgrade-only. A client cannot
reliably derive every provider compatibility rule from raw capacities.
**Fail closed:** select the target from the current region's v2 plans, but let
the provider validate whether the transition is permitted; surface its
structured error without fallback or automatic alternative selection.

## Action response and waiting

### Verified wire variants

`POST .../actions` is declared to return `201` with a direct `Action` object.
The action-specific HTML examples return `{ "action": Action }`. Resource
create/rename and IP mutations can instead expose
`{ "links": { "actions": [Action, ...] } }`. Decode these three named forms
and normalize them; reject an unrelated success shape.

An action has:

| Field | Wire rule |
| --- | --- |
| `id` | Integer in legacy `Action`, string in `ActionByStr`; normalize to string. |
| `status` | String; normalized below. |
| `type`, `resource_type`, `region_slug` | Strings. Do not assume returned `type` equals the requested verb: snapshot/clone examples return `create`, and newer tasks use names such as `StopServerUseCase`. |
| `resource_id` | Integer. |
| `created_at` | String. |
| `completed_at` | Nullable string. |

Legacy examples also contain `started_at`, which the current schemas omit.
Several legacy examples omit `created_at`, and the backup-switch example is
only `{ "action": { "status": "completed" }, "reglet_id": "..." }` even
though the current schemas mark the full action fields required. Ignore or
retain `started_at` as optional diagnostic data and do not require timestamps
or identity when the provider has already returned a documented terminal
status.

Poll `GET /v1/actions/{action_id}`. Its path accepts a decimal ID or
`chain_<digits>` and its response is `{ "action": Action|ActionByStr }`
([task queue](https://developers.cloudvps.reg.ru/getting-started/taskqueue.html)).

Normalize:

| Wire status | Internal state |
| --- | --- |
| `wait`, `new`, `in-progress`, `in_progress` | pending |
| `completed` | succeeded |
| `errored`, `failed` | failed |

The first-party task-queue page warns that REG.RU is migrating between task
mechanisms and that their response format will change. No poll interval,
long-polling, webhook, cancellation endpoint, maximum execution time, or task
retention promise is documented.

### Implementation inference

Poll pending actions with capped exponential backoff and jitter, bounded by
the command context. A client timeout or cancellation is not provider failure:
return the last action ID/status so the user can resume with `action show` or
`action wait`. `--no-wait` must return the normalized action ID whenever one
was provided.

Unknown statuses and unresolved pending actions must never become success. An
unknown status should remain visible as provider contract
drift/pending-unknown until the bounded wait ends. A pending action with no
usable ID cannot support `--no-wait` or polling and must fail closed. A
documented terminal `completed` action may succeed without an ID; a terminal
failure without an ID still fails and preserves all available provider detail.
If a documented mutation returns no action at all but does return its
documented synchronous success (for example delete `204`), treat it as
synchronous rather than inventing a task.

## IP and PTR contract

### List and add

`GET /v1/ips` has optional integer query `reglet_id`. Each item can contain
`created_at`, `id`, `ip`, `ptr`, `region_slug`, `reglet_id`, `status`, and
`type`; status enum is `new`, `active`, `suspended`, and type is `ipv4` or
`ipv6`.

The OpenAPI accidentally names the array `snapshots`; the HTML documentation
uses `ips` and sometimes represents `reglet_id` as a string
([IP list](https://developers.cloudvps.reg.ru/add-ip/list.html)). Accept
`ips` or `snapshots` only when the elements match the IP shape, and accept a
decimal string or integer for `reglet_id`. An item without an address is
contract drift.

`POST /v1/ips` fields are:

| Field | Wire type | Rule |
| --- | --- | --- |
| `reglet_id` | integer | Required by the HTML contract. |
| `ipv4_count` | integer | Conditionally required. |
| `ipv6_count` | integer | Conditionally required. |

At least one count must be present. REG.RU documents a maximum of four
additional IPv4 and four additional IPv6 addresses attached to one server
([IP add](https://developers.cloudvps.reg.ru/add-ip/add.html)). The OpenAPI
does not encode required fields, positive minima, or the per-server maximum.
**Inference:** require a positive requested count, reject a single requested
count above four, and allow the provider to enforce the aggregate/racy limit.

The `201` response is
`{ "ips": object, "links": { "actions": [ActionByStr, ...] } }`; the `ips`
object is not further specified. Completion should be derived from any action,
then reconciled through `GET /v1/ips?reglet_id=...`, not from an assumed
created-address shape.

### PTR and removal

PTR sends `PUT /v1/ips/{ip}` with exactly `{ "ptr": "host.example" }`.
The current schema gives `ptr` length 6–63 and a provider-specific hostname
pattern. Success is `201 { "ip": { "ptr": "..." } }`
([PTR](https://developers.cloudvps.reg.ru/ptr/index.html)).

IP removal is contradictory:

- current OpenAPI: `200 { "links": { "actions": [...] } }`;
- first-party HTML: `204` and empty body
  ([IP delete](https://developers.cloudvps.reg.ru/add-ip/delete.html)).

Accept exactly those two documented success variants. On `200`, wait for a
returned action when present; on `204`, treat it as synchronous. Reconcile
absence through the list when a stronger result is needed. Do not require JSON
on `204`.

## SSH key contract

| Operation | Request | Documented response |
| --- | --- | --- |
| List | `GET /v1/account/keys` | Array of `Key`. OpenAPI envelope `ssh_key`; HTML envelope `ssh_keys`. |
| Add | `POST /v1/account/keys` with required `name`, `public_key` | One `Key`. OpenAPI envelope `ssh_keys`; HTML envelope `ssh_key`. |
| Rename | `PUT /v1/account/keys/{key_id}` with only required `name` | One `Key`; same singular/plural discrepancy. |
| Remove | `DELETE /v1/account/keys/{key_id}` | `204`, empty. |

Sources:
[list](https://developers.cloudvps.reg.ru/ssh-keys/list.html),
[add](https://developers.cloudvps.reg.ru/ssh-keys/add.html),
[rename](https://developers.cloudvps.reg.ru/ssh-keys/rename.html),
[delete](https://developers.cloudvps.reg.ru/ssh-keys/delete.html).

`Key` fields are `id` integer, `fingerprint` string, `name` string,
`public_key` string, and OpenAPI-only `service_id` integer. `key_id` is a path
string matching alphanumerics/colon; the first-party docs explicitly allow
either numeric ID or fingerprint. Normalize identity without changing the
fingerprint text.

The current upload schema accepts RSA, DSS, Ed25519, and `ecdsa-sha2-*` public
key forms through a restrictive regex. It does not publish support for newer
security-key algorithms. Let the provider return a validation error for a
well-formed but not-yet-documented algorithm rather than rewriting key
material. Never return private key material: this API accepts and returns only
the public key.

Because the two first-party sources invert `ssh_key` and `ssh_keys`, the
decoder must accept both only when the value has the expected cardinality
(object for add/rename, array for list). A key returned without either `id` or
`fingerprint` is not safely addressable and must be reported as drift.

## Snapshots and backups

### Snapshots

- List: `GET /v1/snapshots` with optional `region`; response
  `{ "snapshots": [Image, ...] }`
  ([snapshot list](https://developers.cloudvps.reg.ru/snapshots/list.html)).
- Create: the `snapshot` lifecycle action above; `name` required and `offline`
  optional.
- Remove: `DELETE /v1/snapshots/{image_id}`; success `204`, empty
  ([snapshot delete](https://developers.cloudvps.reg.ru/snapshots/delete.html)).
- Dependency query:
  `GET /v1/reglets_for_snapshot/{snapshot_id}` returns
  `{ "reglets": [integer, ...] }`.

Legacy snapshot examples use `slug: null` and `private: 1`, while the current
v1 `Image` schema says string and boolean. Accept nullable `slug` and the
documented boolean-like variants. Snapshot size fields remain decimal strings.
The public contract does not state whether a snapshot delete is rejected when
the dependency query returns servers; do not invent cascade behavior.

### Backups

There is no dedicated `/backups` resource in the current v1 OpenAPI.

- Discover backup images through
  `GET /v2/images?region=...&type=backup&page=...&items_per_page=...`;
  `private=true` is an optional additional filter.
- Enable/disable scheduled backups through the lifecycle action.
- Read current enablement from `Reglet.backups_enabled` and last known backup
  time from nullable `last_backup_date`.
- Restore by sending the selected backup image integer `id` in the `restore`
  action.

**Gap / fail closed:** no public contract describes backup schedule,
retention, immediate "backup now", deletion, consistency, or restore-point
availability. Do not expose commands for those operations or claim that
`last_backup_date` identifies the exact image returned by v2.

## v2 plans, images, and pagination

Both endpoints require `region`, `page`, and `items_per_page`. `page` starts at
1; current OpenAPI bounds `items_per_page` to 10–100. The HTML table says
1–100 in one place but its explanatory note and OpenAPI both say 10–100; send
10–100.

Plans additionally accept:

| Query | Type/constraint |
| --- | --- |
| `disk` | integer, minimum 10 |
| `memory` | integer, minimum 1 |
| `vcpus` | integer, minimum 1 |
| `unit` | `hour` or `month` |
| `plan_line` | string |

The response is `{ "plans": [Plan], "metadata": Paginator }`. Every current
`Plan` requires `disk`, `id`, `memory`, `name`, `plan_line`,
`price_per_hour` string, `price_per_month` integer, `slug`, `unit`, `vcpus`,
and `videocards`
([plan documentation](https://developers.cloudvps.reg.ru/sizes/index.html)).
Keep decimal prices as strings and do not assume an undocumented currency.

The plan documentation labels `memory` as GB, while its own examples return
values such as `1024` for a 1 GB plan. The OpenAPI describes a count but does
not define a unit. **Gap / fail closed:** preserve the raw integer in the
transport/domain result and do not expose a unit-labelled memory filter or
perform automatic GB/MB conversion until an authorized live fixture or a new
first-party statement resolves the unit.

Images additionally accept optional `type` enum
`application`, `backup`, `clone`, `distribution`, `restore`, `snapshot`,
`custom`, and optional boolean `private`. The response is
`{ "images": [Image], "metadata": Paginator }`. Current v2 `Image` requires:

`created_at`, `distribution`, `id`, `min_disk_size`, `name`, `private`,
`region_slug`, `size_gigabytes`, `slug`, and `type`;
`isp_license_size` is optional/nullable
([image documentation](https://developers.cloudvps.reg.ru/images/list.html)).

The v2 schema marks `created_at` as `date-time`, but the first-party example
uses a space-separated timestamp rather than RFC 3339. Decode it as an opaque
string. `min_disk_size` and `size_gigabytes` are decimal strings.

The current region enum for both endpoints is:

`msk1`, `openstack-msk1`, `openstack-msk2`, `openstack-msk3`,
`openstack-spb1`, `openstack-sam1`, `openstack-fz1`.

The HTML pages list fewer values; use the live OpenAPI enum for request
validation.

Paginator is:

```json
{
  "pages": {
    "current": 1,
    "items_per_page": 10,
    "total": 3
  },
  "total": 27
}
```

`pages.total` is page count; top-level `total` is item count. Traverse while
`pages.current < pages.total`. Validate that returned page metadata makes
forward progress; a repeated/decreasing current page or impossible total is
contract drift. Ticket-relevant v1 list endpoints declare no pagination.

## Structured errors and retry boundary

### Verified

The v1 `Error` schema is:

```json
{ "code": "string", "message": "string" }
```

On 2026-07-31, an unauthenticated
[`GET /v1/reglets`](https://api.cloudvps.reg.ru/v1/reglets) returned `401`:

```json
{ "code": 401, "message": "Токен авторизации отсутствует либо невалиден." }
```

Therefore v1 `code` is demonstrably string-or-number on the wire. The same
read-only observation returned `X-Request-ID`, but the header is absent from
the published contract.

The v2 general error is `{ "code": string, "message"?: string }`.
Specialized errors may contain only `code`; documented examples include
`TOKEN_VALIDATION_FAILED` (`401`) and `ENVIRONMENT_BLOCKED` (`403`). On the
research date, an unauthenticated
[`GET /v2/plans`](https://api.cloudvps.reg.ru/v2/plans?region=openstack-msk1&page=1&items_per_page=10)
returned `401 { "code": "TOKEN_VALIDATION_FAILED" }`.

Declared operation errors are primarily:

- v1 `400` validation and `401` missing/invalid token; selected operations
  declare `404`;
- v2 images: `400`, `401`, `403`; v2 plans: `401`, `403`, `404`.

No public source inspected here documents a rate-limit quota, `429`,
`Retry-After`, idempotency keys, request deduplication, or safe mutation replay
semantics.

### Implementation inference

Use HTTP status as the primary class and preserve provider `code` as normalized
text plus optional `message`. Parse `code` from string or JSON number without
using floating-point conversion. Preserve a syntactically safe
`X-Request-ID` when present as diagnostic metadata, but do not require it.
Bound any captured unknown response body and never include request headers.

Suggested stable mapping:

| Condition | CLI classification |
| --- | --- |
| `400` or provider validation code | provider validation failure; not retryable |
| `401`, `TOKEN_VALIDATION_FAILED` | CloudVPS authentication failure; not automatically retryable |
| `403`, `ENVIRONMENT_BLOCKED` | capability/service blocked; not retryable without external change |
| `404` | requested provider resource/catalog selection not found |
| malformed documented success/error JSON | provider contract drift |
| timeout before any response, connection failure, `5xx` | provider/network failure; reads may be retryable |
| timeout after an ambiguous mutation | outcome unknown; reconcile, never blind-replay |

Retry idempotent reads conservatively with capped backoff and jitter. If an
undocumented `429` is encountered, honor a valid `Retry-After` if present and
surface persistent throttling; this is defensive behavior, not a published
guarantee. Do not automatically replay `POST /reglets`, `POST /ips`, or
`POST .../actions`. Do not assume syntactic `PUT`/`DELETE` idempotence equals a
provider replay guarantee.

## Fail-closed checklist for implementation and fixtures

The implementation ticket should not be considered complete unless fixtures
exercise all of these boundaries:

1. Both documented server-list envelopes (`reglet`, `reglets`) and rejection
   of an unrelated success shape.
2. Direct, `action`, and `links.actions` task shapes; integer and `chain_*`
   IDs; every normalized status; unknown status never succeeding.
3. Boolean-like legacy variants only for the named fields, decimal-string
   preservation, nullable snapshot slug, and non-RFC3339 timestamps.
4. IP `ips`/`snapshots` array aliases, string/integer `reglet_id`, both
   documented IP-delete statuses, and local `ip show` lookup without the
   undocumented route.
5. SSH key singular/plural envelope inversions with cardinality checks.
6. Mandatory action-specific fields despite the generic union schema:
   rebuild image, resize size, snapshot name, and restore image.
7. v2 mandatory pagination, page-progress validation, full current region
   enum, and raw/unknown plan-memory unit.
8. v1 numeric/string error codes, v2 code-only errors, malformed/non-JSON
   errors, safe request-ID capture, and secret-free diagnostics.
9. Ambiguous transport failure on every billable/destructive `POST` returns
   outcome-unknown and performs no automatic replay.
10. Missing resource identity or a pending action ID cannot produce a success
    result that the user cannot inspect or resume; the documented minimal
    terminal backup-switch response remains accepted.

## Primary sources

- [REG.RU CloudVPS developer documentation](https://developers.cloudvps.reg.ru/)
- [Current CloudVPS v1 OpenAPI](https://api.cloudvps.reg.ru/v1/openapi.json)
- [Current CloudVPS v2 OpenAPI](https://api.cloudvps.reg.ru/v2/api/swagger.json)
- [Authentication](https://developers.cloudvps.reg.ru/getting-started/authentication.html)
- [Task queue and migration warning](https://developers.cloudvps.reg.ru/getting-started/taskqueue.html)
- [Server list](https://developers.cloudvps.reg.ru/reglets/list.html)
- [Server creation](https://developers.cloudvps.reg.ru/reglets/add.html)
- [Server action index](https://developers.cloudvps.reg.ru/reglets/index.html)
- [Plans](https://developers.cloudvps.reg.ru/sizes/index.html)
- [Images](https://developers.cloudvps.reg.ru/images/list.html)
- [SSH keys](https://developers.cloudvps.reg.ru/ssh-keys/index.html)
- [Snapshots](https://developers.cloudvps.reg.ru/snapshots/index.html)
- [Additional IPs](https://developers.cloudvps.reg.ru/add-ip/index.html)
- [PTR updates](https://developers.cloudvps.reg.ru/ptr/index.html)
- [Repository implementation ticket](https://github.com/adinvadim/reg-ru-cli/issues/5)
