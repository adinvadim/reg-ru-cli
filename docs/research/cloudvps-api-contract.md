# REG.RU CloudVPS API contract

Research date: 2026-07-30

## Executive conclusion

The CloudVPS API is a split-version API at `https://api.cloudvps.reg.ru`: resource management remains under `/v1`, while the current catalog endpoints are only `GET /v2/plans` and `GET /v2/images`. REG.RU explicitly instructs clients to discover current plan and image slugs through v2, then create servers through `POST /v1/reglets`. Both versions use the same bearer token. ([documentation home](https://developers.cloudvps.reg.ru/), [server creation](https://developers.cloudvps.reg.ru/reglets/add.html), [v1 OpenAPI](https://api.cloudvps.reg.ru/v1/openapi.json), [v2 OpenAPI](https://api.cloudvps.reg.ru/v2/api/swagger.json))

The API can support a CloudVPS CLI, but the published contract has material inconsistencies between the narrative documentation and the current OpenAPI documents. In particular, collection envelope names, action envelopes, some status codes, and the v1 error `code` type disagree. A robust client should isolate wire decoding from its domain model and accept the documented variants described below.

## Authentication

- Send `Authorization: Bearer <token>`. The token is available and rotatable in the CloudVPS environment's **Settings** tab. ([authentication](https://developers.cloudvps.reg.ru/getting-started/authentication.html), [REG.RU help](https://help.reg.ru/support/servery-vps/oblachnyye-servery/rabota-s-serverom/api-dlya-oblachnykh-serverov))
- The token grants access to **all operations** for the CloudVPS service/environment; no scopes or restricted tokens are documented. It must therefore be treated as a high-value secret. ([authentication](https://developers.cloudvps.reg.ru/getting-started/authentication.html))
- Both OpenAPI documents define HTTP bearer authentication. Most v1 resource operations require it; the v1 spec leaves `/prices`, `/sizes`, and `/random_reglet_name` without declared security and makes legacy `GET /images` optionally authenticated. Both v2 catalog operations require bearer auth. These exceptions are properties of the present specification, not a promise that anonymous access will remain supported. ([v1 OpenAPI](https://api.cloudvps.reg.ru/v1/openapi.json), [v2 OpenAPI](https://api.cloudvps.reg.ru/v2/api/swagger.json))
- No token expiry, token format, scopes, multiple-token support, or programmatic token-creation endpoint is documented. The CLI should accept a token supplied outside command arguments and never print it. **The storage mechanism is an implementation decision, not part of the API contract.**

## Versions and endpoint surface

The narrative portal displays the build/version date `2021.01.14`, but it points to live Swagger/OpenAPI documents as the current request/response reference. The current v1 document identifies itself as OpenAPI 3.0.3, title `api cloudvps v1`, version `1.0`; v2 identifies itself as OpenAPI 3.0.2, title `Публичный API`, version `v2`. Neither document advertises a deprecation or sunset header/policy. ([documentation home](https://developers.cloudvps.reg.ru/), [v1 OpenAPI](https://api.cloudvps.reg.ru/v1/openapi.json), [v2 OpenAPI](https://api.cloudvps.reg.ru/v2/api/swagger.json))

### v1 resource inventory

The current v1 OpenAPI exposes these resource groups:

| Capability | Methods and paths |
| --- | --- |
| Servers | `GET, POST /v1/reglets`; `GET, PUT, DELETE /v1/reglets/{resource_id}` |
| Server lifecycle | `POST /v1/reglets/{resource_id}/actions`; `GET /v1/actions/{action_id}` |
| Additional IPs and PTR | `GET, POST /v1/ips`; `PUT, DELETE /v1/ips/{ip}` |
| SSH keys | `GET, POST /v1/account/keys`; `PUT, DELETE /v1/account/keys/{key_id}` |
| Images and snapshots | `GET /v1/images`; `GET /v1/snapshots`; `DELETE /v1/snapshots/{image_id}`; `GET /v1/reglets_for_snapshot/{snapshot_id}` |
| Private networks | `GET, POST /v1/vpcs`; `GET, PUT, DELETE /v1/vpcs/{vpcs_id}`; `GET, POST /v1/vpcs/{vpcs_id}/members`; `DELETE /v1/vpcs/{vpcs_id}/members/{resource_id}` |
| Account/inventory support | `GET /v1/balance_data`, `/billing_history`, `/history`, `/removed_servers`, `/prices`, `/sizes`, `/random_reglet_name`; feedback and one environment-setting endpoint |

The spec also exposes a cookie-authenticated kubeconfig endpoint intended for the panel, not bearer-token CLI use. The VPC delete operation is declared but returns only `501 Not Implemented`; it should not be presented as working functionality. ([v1 OpenAPI](https://api.cloudvps.reg.ru/v1/openapi.json))

### v2 catalog inventory

The entire current v2 OpenAPI surface is:

- `GET /v2/plans`
- `GET /v2/images`

Both require `region`, `page`, and `items_per_page`. Plans can additionally be filtered by `disk`, `plan_line`, `unit`, `memory`, and `vcpus`; images by `type` and `private`. The current OpenAPI enumerates regions `msk1`, `openstack-msk1`, `openstack-msk2`, `openstack-msk3`, `openstack-spb1`, `openstack-sam1`, and `openstack-fz1`. The HTML pages list fewer regions and image types, so the live OpenAPI enum is the broader current contract. ([plans](https://developers.cloudvps.reg.ru/sizes/index.html), [images](https://developers.cloudvps.reg.ru/images/list.html), [v2 OpenAPI](https://api.cloudvps.reg.ru/v2/api/swagger.json))

## Server deployment and lifecycle

### Discover, then create

1. Enumerate compatible plans and images from v2 for the desired region.
2. Pass the selected plan `slug` as `size` and the selected image `slug` as `image` to `POST /v1/reglets`.

REG.RU explicitly says that even though plans and images are obtained from v2, server creation must use v1. `size` and `image` are required. The current v1 schema also permits optional `name`, `ssh_keys`, `backups`, `isp_license_size`, `region_slug`, `floating_ip`, and `promocode`. An image may be a string slug or an integer snapshot/image ID; creation from a snapshot uses the latter form. ([server creation](https://developers.cloudvps.reg.ru/reglets/add.html), [v1 OpenAPI](https://api.cloudvps.reg.ru/v1/openapi.json))

`POST /v1/reglets` is specified to return HTTP `201` with a `reglet` plus `links.actions`. A newly accepted server may have status `new` and be locked while its creation action runs. The published server status set is `new`, `active`, `ordered`, `off`, `suspended`, and `archive`; the older HTML description omits `ordered`. ([server list](https://developers.cloudvps.reg.ru/reglets/list.html), [v1 OpenAPI](https://api.cloudvps.reg.ru/v1/openapi.json))

### Lifecycle operations

`POST /v1/reglets/{resource_id}/actions` takes a `type` from the current enum:

`start`, `stop`, `reboot`, `rebuild`, `password_reset`, `resize`, `generate_vnc_link`, `snapshot`, `enable_backups`, `disable_backups`, `restore`, `clone`, `resize_isp_license`.

Additional fields depend on the action: `image`, `size`, `name`, `offline`, `ssh_keys`, and `isp_license_size`. The per-operation HTML pages document those conditional requirements more precisely; for example, snapshot creation requires `name`, while `offline` selects a consistent snapshot with a stop versus one without a stop. ([server operations index](https://developers.cloudvps.reg.ru/reglets/index.html), [snapshot creation](https://developers.cloudvps.reg.ru/snapshots/add.html), [v1 OpenAPI](https://api.cloudvps.reg.ru/v1/openapi.json))

Other lifecycle calls are:

- Rename: `PUT /v1/reglets/{resource_id}` with `name`.
- Read one: `GET /v1/reglets/{resource_id}`; this response adds `disk_usage`.
- Delete: `DELETE /v1/reglets/{resource_id}`. The narrative documentation and current OpenAPI agree on HTTP `204` with no response body. ([server deletion](https://developers.cloudvps.reg.ru/reglets/delete.html), [v1 OpenAPI](https://api.cloudvps.reg.ru/v1/openapi.json))

## Asynchronous operation semantics

Some accepted operations execute in the background and return an action/task identifier. Poll `GET /v1/actions/{action_id}` until a terminal status. The polling route accepts both a decimal ID and the newer `chain_<digits>` string form. ([task queue](https://developers.cloudvps.reg.ru/getting-started/taskqueue.html), [v1 OpenAPI](https://api.cloudvps.reg.ru/v1/openapi.json))

The combined status vocabulary in the live v1 schemas is:

- Non-terminal: `wait`, `new`, `in-progress`, `in_progress`
- Success terminal: `completed`
- Failure terminal: `errored`, `failed`

The older queue page documents only `new`, `in-progress`, `errored`, and `completed`, while also warning that REG.RU is migrating away from the legacy `actions` mechanism and that the response format will change. Newer tasks may have string IDs and use-case-like `type` values such as `StopServerUseCase`. ([task queue](https://developers.cloudvps.reg.ru/getting-started/taskqueue.html), [v1 OpenAPI](https://api.cloudvps.reg.ru/v1/openapi.json))

Three response shapes appear in first-party materials:

1. `{ "action": { ... } }`
2. an action object directly (the current OpenAPI response schema for `POST .../actions`)
3. `{ "links": { "actions": [ ... ] } }` on resource creation/list responses

The CLI decoder should normalize all three into one internal operation type. It should preserve unknown status and action-type strings rather than failing to decode them. **This is a compatibility recommendation inferred from the documented migration and schema discrepancies, not a server guarantee.**

REG.RU documents no poll interval, server-side wait/long-polling, webhook, maximum task duration, cancellation, or retention period for completed action IDs. A client-side timeout must not be interpreted as operation failure; report the last known task ID/status so the user can resume polling.

## IP addresses

Primary IPv4, IPv6, and PTR values are embedded in a server object as `ip`, `ipv6`, and `ptr`. Additional addresses use `/v1/ips`. `GET /v1/ips` lists them and can filter by `reglet_id`; each item includes `id`, `ip`, `ptr`, `region_slug`, `reglet_id`, `status`, and `type`. ([server list](https://developers.cloudvps.reg.ru/reglets/list.html), [additional-IP list](https://developers.cloudvps.reg.ru/add-ip/list.html))

`POST /v1/ips` accepts `reglet_id` and at least one of `ipv4_count` or `ipv6_count`. The HTML documentation states a maximum of four additional IPv4 and four additional IPv6 addresses per server. The operation may include queued actions in `links.actions`. ([add IP](https://developers.cloudvps.reg.ru/add-ip/add.html))

`PUT /v1/ips/{ip}` changes the PTR record with `{ "ptr": "host.example" }`; `DELETE /v1/ips/{ip}` removes an additional address. ([PTR](https://developers.cloudvps.reg.ru/ptr/index.html), [delete IP](https://developers.cloudvps.reg.ru/add-ip/delete.html))

There are two important discrepancies:

- The HTML list page shows `GET /v1/ips/{ip}`, but that operation is absent from the current v1 OpenAPI. Do not depend on it without a live compatibility test.
- The HTML delete page says success is `204` with an empty body, while the current OpenAPI declares `200`. Accept either success status and do not require a JSON body.

The v1 OpenAPI response schema for `GET /ips` also mistakenly names its collection property `snapshots`; the HTML documentation and examples use `ips`. A tolerant decoder may accept both, but emitted/user-facing vocabulary should be `ips`. ([additional-IP list](https://developers.cloudvps.reg.ru/add-ip/list.html), [v1 OpenAPI](https://api.cloudvps.reg.ru/v1/openapi.json))

## Pagination

v2 pagination is page-based and mandatory on both lists:

- Request: `page` (minimum 1) and `items_per_page` (current OpenAPI minimum 10, maximum 100).
- Response: `metadata.pages.current`, `metadata.pages.items_per_page`, `metadata.pages.total` (page count), and `metadata.total` (item count).

The HTML parameter tables say “1 to 100” in one place but their accompanying notes and the v2 OpenAPI say `10..100`; use `10..100`. Traverse while `current < pages.total`. ([plans](https://developers.cloudvps.reg.ru/sizes/index.html), [images](https://developers.cloudvps.reg.ru/images/list.html), [v2 OpenAPI](https://api.cloudvps.reg.ru/v2/api/swagger.json))

v1 does not define general pagination. Server, IP, SSH-key, snapshot, VPC, and removed-server list endpoints have no pagination parameters in the current OpenAPI and return arrays. Only `GET /v1/history` declares offset pagination: optional `limit` from 1 to 50 and optional `offset` from 0, but its response has no total or next-page metadata. Stop when fewer than `limit` items are returned. **That stopping rule is a conventional inference; REG.RU does not document an end-of-list rule or consistency guarantees during traversal.** ([v1 OpenAPI](https://api.cloudvps.reg.ru/v1/openapi.json))

## Errors and response inconsistencies

v1 operation specs primarily declare:

- `400`: validation error
- `401`: access token missing or invalid
- `404`: resource not found on selected operations
- `501`: declared for the unimplemented VPC delete

The v1 `Error` schema is `{ "code": string, "message": string }`. However, an unauthenticated read-only request to `GET /v1/reglets` on the research date returned HTTP `401` with `{ "code": 401, "message": "Токен авторизации отсутствует либо невалиден." }`, i.e. a numeric `code`. Therefore decode `code` as string-or-number and treat the HTTP status as authoritative. The response also carried `X-Request-ID`, but this header is not documented as a stable contract. ([v1 OpenAPI](https://api.cloudvps.reg.ru/v1/openapi.json), [unauthenticated endpoint](https://api.cloudvps.reg.ru/v1/reglets))

v2 documents `400`, `401`, `403`, and `404` as applicable. Its general error shape is `{ "code": string, "message"?: string }`; specialized examples include `TOKEN_VALIDATION_FAILED` and `ENVIRONMENT_BLOCKED`. ([v2 OpenAPI](https://api.cloudvps.reg.ru/v2/api/swagger.json))

Other envelope mismatches that affect a client:

- The server-list HTML uses `reglets`, but the current v1 OpenAPI schema spells the collection `reglet`.
- Server-action HTML examples wrap the task in `action`, while the current POST action schema describes a direct action object.
- Several legacy HTML examples use integer booleans (`0`/`1`) and sometimes string `"1"`; current schemas increasingly use JSON booleans.

The safest boundary is a permissive wire decoder followed by a strict normalized domain model. Outgoing requests should follow the current OpenAPI types, except where a per-operation first-party page gives a required conditional field omitted by the generic schema. ([server list](https://developers.cloudvps.reg.ru/reglets/list.html), [reboot](https://developers.cloudvps.reg.ru/reglets/reboot.html), [v1 OpenAPI](https://api.cloudvps.reg.ru/v1/openapi.json))

## Retries, idempotency, and rate limits

**Undocumented gaps:** neither current OpenAPI document nor the first-party getting-started pages document rate-limit quotas, a `429` response, `Retry-After`, retry guarantees, idempotency keys, request deduplication, or safe replay semantics. No rate-limit headers were present on the unauthenticated read-only response sampled during this research. This is evidence of missing documentation, not evidence that limits do not exist. ([getting started](https://developers.cloudvps.reg.ru/getting-started/index.html), [v1 OpenAPI](https://api.cloudvps.reg.ru/v1/openapi.json), [v2 OpenAPI](https://api.cloudvps.reg.ru/v2/api/swagger.json))

Recommended conservative client policy (**inference, not REG.RU contract**):

- Retry idempotent reads after connection failures, timeouts before a response, and transient `5xx`, using capped exponential backoff with jitter.
- If a `429` is encountered, honor `Retry-After` when present; otherwise use the same backoff and surface persistent throttling.
- Do not automatically replay `POST /reglets`, `POST /ips`, `POST .../actions`, or other mutations after an ambiguous transport failure. First reconcile by listing the resource or polling a returned action ID. There is no documented idempotency key protecting duplicate purchases or duplicate actions.
- `PUT` and `DELETE` are syntactically idempotent, but the provider does not state replay guarantees. Retry only after reconciling current resource state.
- Poll asynchronous actions with backoff and jitter, keep the action ID, and bound only the CLI wait—not the server operation itself.
- Preserve HTTP status, parsed provider code/message, action ID, and `X-Request-ID` when present in diagnostics.

## Contract decisions suitable for the CLI

1. Use one CloudVPS API origin with explicit v1 resource and v2 catalog clients.
2. Always choose deployment plan/image data from v2, then create through v1.
3. Normalize collection and action envelope variants at the transport boundary.
4. Model provider codes and action IDs as strings internally; accept numeric wire values.
5. Treat `completed` as success, `errored`/`failed` as failure, the documented non-terminal variants as pending, and unknown statuses as visible non-terminal states requiring user attention.
6. Expose pagination automatically for v2; do not invent pagination for unpaginated v1 lists.
7. Make mutation retries opt-in/reconciliatory because the provider publishes no idempotency contract.
8. Keep rate-limit behavior configurable and conservative because no quota is published.

## Primary sources

- [CloudVPS developer documentation](https://developers.cloudvps.reg.ru/)
- [CloudVPS v1 OpenAPI](https://api.cloudvps.reg.ru/v1/openapi.json)
- [CloudVPS v1 Swagger UI](https://api.cloudvps.reg.ru/v1/ui/)
- [CloudVPS v2 OpenAPI](https://api.cloudvps.reg.ru/v2/api/swagger.json)
- [Authentication](https://developers.cloudvps.reg.ru/getting-started/authentication.html)
- [Task queue](https://developers.cloudvps.reg.ru/getting-started/taskqueue.html)
- [Server creation](https://developers.cloudvps.reg.ru/reglets/add.html)
- [Server list and status fields](https://developers.cloudvps.reg.ru/reglets/list.html)
- [Server operations](https://developers.cloudvps.reg.ru/reglets/index.html)
- [Plan catalog and pagination](https://developers.cloudvps.reg.ru/sizes/index.html)
- [Image catalog and pagination](https://developers.cloudvps.reg.ru/images/list.html)
- [Additional IP list](https://developers.cloudvps.reg.ru/add-ip/list.html)
- [Additional IP creation](https://developers.cloudvps.reg.ru/add-ip/add.html)
- [PTR updates](https://developers.cloudvps.reg.ru/ptr/index.html)
- [REG.RU help: Cloud server API](https://help.reg.ru/support/servery-vps/oblachnyye-servery/rabota-s-serverom/api-dlya-oblachnykh-serverov)
