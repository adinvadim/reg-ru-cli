# CloudVPS CLI integration contract

Research date: 2026-07-31

## Conclusion

Issue [Task: implement CloudVPS and VPS commands][issue-5] can be integrated
without replacing the established Cobra root, account selection, output
envelope, or credential-process boundary. The clean seam is a typed
`internal/provider/cloudvps` package plus CloudVPS-specific Cobra handlers.
The current generic `cli.Executor` is sufficient for placeholders but is too
lossy for implementation: it carries positional strings only and cannot
represent typed flags, asynchronous action state, or command-specific output.
The scaffold itself describes that interface as temporary and says real
implementation tickets should replace it with narrow consumer-owned
interfaces. ([CLI types](../../internal/cli/types.go), [issue #5][issue-5])

The implementation must preserve the already-published `vps get`, `vps
create`, and `vps delete` names. The issue's `show`, `deploy`, and `destroy`
names should be Cobra aliases of those canonical commands, not three additional
provider operations: CloudVPS publishes one server read, one server create, and
one server delete operation. Cobra has first-class command aliases, and its
command path remains suitable for the canonical output-envelope `command`
field. ([current command tree](../../internal/cli/commands.go),
[Cobra `Command`][cobra-command], [server operations][cloudvps-reglets])

This contract covers the complete surface requested by issue #5. It is not the
complete surface of the v1 OpenAPI document. Private networks, billing/history,
removed-server inventory, feedback, environment settings, VNC-link generation,
and ISP-license resizing are outside this ticket. In particular, the published
VPC delete operation currently declares only `501 Not Implemented`, so
presenting this ticket as complete coverage of every CloudVPS endpoint would be
incorrect. ([issue #5][issue-5], [v1 OpenAPI][cloudvps-v1])

## Non-negotiable repository contracts

CloudVPS commands inherit these established behaviors:

- Account selection is
  `--account > REGRU_ACCOUNT > project account > user default > error`.
  Commands never choose the only configured profile implicitly. ([CLI
  contract](../cli-contract.md), [runtime](../../internal/cli/runtime.go))
- The only direct public-API credential is the lazy
  `cloudvps.token` field from the selected profile's
  `credential_process`. The helper is run without a shell, at most once per
  invocation, and only after local validation, dry-run, and confirmation have
  passed. ([profile-secret contract](../profile-secret-contract.md),
  [credential resolver](../../internal/credentialprocess/resolver.go),
  [CloudVPS authentication][cloudvps-auth])
- Success data goes to stdout. Diagnostics, prompts, and the one final error go
  to stderr. JSON retains the `regru.cli/v1` envelope; plain mode remains one
  stable escaped tab-separated record per line. ([CLI contract](../cli-contract.md),
  [output implementation](../../internal/cli/output.go))
- A mutation performs all local validation first. `--dry-run` then returns a
  redacted local plan without constructing a provider client, resolving a
  credential, or making any HTTP call. A real mutation next requires a TTY
  confirmation or `--force`; `--force` bypasses only that confirmation.
  ([runtime](../../internal/cli/runtime.go), [CLI contract](../cli-contract.md))
- The command context carries interruption and deadlines through every HTTP
  request and polling delay. Go's `context` contract explicitly requires
  outgoing calls to accept and propagate cancellation and deadlines.
  ([Go `context`][go-context])

Do not allow a portal session, `credentials.cloudvps_ref`, a token-valued
environment variable, or a command-line flag to act as a fallback bearer
token. The current profile document establishes a generic external helper as
the base binary's secret boundary. A portal session can bootstrap or discover a
credential in another workflow, but it is not authentication for the public
CloudVPS REST API. ([profile-secret contract](../profile-secret-contract.md),
[CloudVPS authentication][cloudvps-auth])

## Canonical command tree

The implementable tree is:

```text
regru vps list
regru vps get <server-id>                         # alias: show
regru vps ips <server-id>                         # compatibility command
regru vps create --size S --image I [flags]       # alias: deploy
regru vps rename <server-id> --name NAME
regru vps start <server-id>
regru vps stop <server-id>
regru vps reboot <server-id>
regru vps rebuild <server-id> --image I [--ssh-key KEY ...]
regru vps resize <server-id> --size S
regru vps password-reset <server-id>
regru vps clone <server-id> [--name NAME] [--offline]
regru vps delete <server-id>                      # alias: destroy

regru vps action show <action-id>
regru vps action wait <action-id> [--wait-timeout D]

regru vps ip list [--server SERVER-ID]
regru vps ip show <address>
regru vps ip add <server-id> [--ipv4-count N] [--ipv6-count N]
regru vps ip ptr <address> --ptr HOSTNAME
regru vps ip remove <address>

regru vps ssh-key list
regru vps ssh-key add --name NAME --public-key-file PATH
regru vps ssh-key rename <id-or-fingerprint> --name NAME
regru vps ssh-key remove <id-or-fingerprint>

regru vps snapshot list [--region REGION]
regru vps snapshot create <server-id> --name NAME [--offline]
regru vps snapshot remove <image-id>

regru vps backup enable <server-id>
regru vps backup disable <server-id>
regru vps backup restore <server-id> <backup-image-id>

regru vps plan list --region REGION [filters]
regru vps image list --region REGION [filters]
```

`show`, `deploy`, and `destroy` use `cobra.Command.Aliases`, while JSON
envelopes always report the canonical command (`vps get`, `vps create`, or
`vps delete`). This avoids splitting completion, help, output fixtures, and
metrics across synonyms. ([Cobra `Command`][cobra-command])

`vps ips <server-id>` must remain because it is in the established visible
command tree. It delegates to the same use case as `vps ip list --server
<server-id>` but retains `command: "vps ips"` in its envelope. Removing or
hiding it would break the current CLI contract. ([CLI contract](../cli-contract.md),
[current command tree](../../internal/cli/commands.go))

### Command flags and validation

| Command | Local request contract |
| --- | --- |
| `create` | Required `--size` and `--image`; optional `--name`, repeatable `--ssh-key`, `--backups[=bool]`, `--isp-license`, `--region`, `--floating-ip[=bool]`, and `--promocode`. Use `Flags().Changed` for the two optional booleans so omission remains distinct from explicit false. |
| `rename` | `--name` required. |
| `rebuild` | `--image` required; repeatable `--ssh-key`. |
| `resize` | `--size` required. |
| `clone` and `snapshot create` | Optional `--offline`; snapshot name is required, clone name is optional. |
| `ip add` | Counts default to zero; at least one must be greater than zero. Reject negative values locally. The first-party documentation states at most four additional IPv4 and four additional IPv6 addresses per server, so reject values over four before confirmation. |
| `ip ptr` | `--ptr` required and validated as a hostname according to the current documented schema. |
| `ssh-key add` | Read one regular file, capped at 16 KiB, after parsing and before confirmation. Reject directories, empty files, oversized input, and unsupported documented public-key syntax. Do not accept private-key input. |
| `plan list` | `--region` required; optional `--plan-line`, `--disk`, `--memory`, `--vcpus`, and `--unit hour|month`. |
| `image list` | `--region` required; optional `--type` and `--private[=bool]`. |

The create schema requires `size` and `image` and documents the other fields
above; lifecycle actions share one typed action endpoint with conditional
fields. The v2 catalog requires region, page, and page size, while the CLI
should own pagination internally. ([server creation][cloudvps-create],
[v1 OpenAPI][cloudvps-v1], [v2 OpenAPI][cloudvps-v2])

All server IDs and image IDs that are documented as numeric must parse as
positive decimal values before any credential or HTTP work. SSH-key
identifiers may also be fingerprints. Action IDs must be stored as strings and
accept both decimal IDs and `chain_<digits>`, because the published action
route supports both forms. ([task queue][cloudvps-actions],
[v1 OpenAPI][cloudvps-v1])

Cobra local flags belong on the leaf command. Use
`MarkFlagRequired`, `MarkFlagsOneRequired`, and explicit `PreRunE` cross-field
validation where applicable; do not add CloudVPS flags to the root. Cobra
distinguishes local from persistent flags and supplies these validation
helpers. ([Cobra flags guide][cobra-flags], [Cobra `Command`][cobra-command])

### Catalog pagination

`plan list` and `image list` automatically request pages beginning at 1 with
`items_per_page=100`, stopping when `metadata.pages.current >=
metadata.pages.total`. They emit one combined list, never provider page
envelopes. The v2 OpenAPI requires `items_per_page` in `10..100` and defines
the page metadata. ([plan catalog][cloudvps-plans], [image catalog][cloudvps-images],
[v2 OpenAPI][cloudvps-v2])

Do not add pagination flags to v1 lists. Server, IP, SSH-key, and snapshot list
operations are unpaginated in the current v1 OpenAPI. ([v1 OpenAPI][cloudvps-v1])

## Account and credential resolution

The sequence for every implemented CloudVPS leaf is:

1. Root `PersistentPreRunE` validates global modes and resolves the account
   alias using the existing precedence.
2. The leaf parses and validates IDs, enums, required-together fields, and
   bounded file input.
3. For a mutation, `--dry-run` returns now. Otherwise the central confirmation
   gate runs.
4. The runtime creates a command-scoped credential resolver and adapts it to a
   `cloudvps.TokenSource`.
5. The first HTTP request calls
   `Resolve(ctx, "cloudvps.token")`; later requests and polls reuse the
   resolver's command-scoped value.
6. Request construction sets `Authorization: Bearer <token>` without logging,
   returning, or placing the token in a URL. The copied token buffer is cleared
   on release on the same best-effort basis as the current resolver.

The direct API has no documented "current principal" or token-introspection
endpoint. Therefore selecting profile alias `work` cannot, by itself, prove
that a helper returned the intended environment's token. If
`profile.Cloud.EnvironmentID` is present and a v1 resource includes
`service_id`, compare them and return `account_mismatch` on disagreement.
However, empty inventory and catalog-only commands cannot perform this check.
This is a real limit, not a check that `--force` may bypass.
([server information][cloudvps-server-info], [v1 OpenAPI][cloudvps-v1])

The following current readiness behavior is inconsistent with the credential
contract and must be corrected in the same implementation:

- `accountResult` and `localCapabilityStates` currently report CloudVPS
  configured from `Credentials.CloudVPSRef` or `Portal.SessionRef`, but ignore
  a configured `credential_process`.
- No code resolves `Credentials.CloudVPSRef`, and a portal session is not a
  bearer token for the public API.
- Consequently, a profile that can actually return `cloudvps.token` may be
  shown as not configured, while a portal-only profile may be shown as
  configured but fail at execution.

For schema-version-1 compatibility, continue decoding the legacy opaque field
but do not treat it as executable credential routing. Report
`cloudvps.instances=configured` when a helper is present (the existing account
output separately reports `credentialProcess: true`), and reserve actual field
availability and provider reachability for `capability probe`. A later profile
schema migration can remove the unused field. ([profile implementation](../../internal/profile/config.go),
[profile commands](../../internal/cli/profile_commands.go),
[profile-secret contract](../profile-secret-contract.md))

## Provider and dependency-injection seams

Add these files:

```text
internal/provider/cloudvps/
  client.go          HTTP construction, authentication, v1/v2 routing
  interface.go       Doer, TokenSource, Sleeper, JitterSource
  model.go           strict normalized domain types
  requests.go        typed public request structs
  wire_v1.go         tolerant v1-only DTOs and envelope normalization
  wire_v2.go         v2-only DTOs and pagination metadata
  waiter.go          action normalization and bounded polling
  errors.go          typed provider/transport/action errors
  client_test.go
  waiter_test.go
  testdata/
    v1/*.json
    v2/*.json

internal/cli/
  cloudvps_commands.go
  cloudvps_output.go
  cloudvps_commands_test.go
```

Do not place provider wire structs in `internal/cli`, and do not let provider
JSON tags define the stable CLI schema. First-party sources disagree on
`reglet` versus `reglets`, `ssh_key` versus `ssh_keys`, `ips` versus the
OpenAPI's erroneous collection name in one schema, wrapped versus direct
actions, numeric versus string action IDs, boolean versus `0/1`, and string
versus numeric provider error codes. The transport boundary must accept the
documented variants and produce one strict model. ([CloudVPS contract
resolution][issue-11], [v1 OpenAPI][cloudvps-v1], [task queue][cloudvps-actions])

Recommended low-level interfaces:

```go
type Doer interface {
    Do(*http.Request) (*http.Response, error)
}

type TokenSource interface {
    Token(context.Context) ([]byte, error)
}

type Sleeper interface {
    Sleep(context.Context, time.Duration) error
}

type JitterSource interface {
    Duration(time.Duration) time.Duration
}
```

`Client` should expose typed methods, not paths or `map[string]any`:

```go
ListServers(context.Context) ([]Server, error)
GetServer(context.Context, int64) (Server, error)
CreateServer(context.Context, CreateServerRequest) (Server, []Action, error)
RenameServer(context.Context, int64, string) (Server, []Action, error)
DeleteServer(context.Context, int64) error
RunAction(context.Context, int64, ActionRequest) (Action, error)
GetAction(context.Context, string) (Action, error)

ListIPs(context.Context, *int64) ([]IPAddress, error)
AddIPs(context.Context, AddIPsRequest) ([]IPAddress, []Action, error)
UpdatePTR(context.Context, string, string) (IPAddress, error)
DeleteIP(context.Context, string) ([]Action, error)

ListSSHKeys(context.Context) ([]SSHKey, error)
AddSSHKey(context.Context, AddSSHKeyRequest) (SSHKey, error)
RenameSSHKey(context.Context, string, string) (SSHKey, error)
DeleteSSHKey(context.Context, string) error

ListSnapshots(context.Context, string) ([]Image, error)
DeleteSnapshot(context.Context, int64) error
ListPlans(context.Context, PlanFilter) ([]Plan, error)
ListImages(context.Context, ImageFilter) ([]Image, error)
```

Snapshot creation, backups, restore, rebuild, resize, password reset, clone,
start, stop, and reboot are typed constructors of `ActionRequest`, not separate
HTTP implementations. This keeps conditional-field validation outside the wire
decoder while preserving the one official action endpoint. ([server
operations][cloudvps-reglets], [v1 OpenAPI][cloudvps-v1])

In `cli.Options`, add a CloudVPS factory rather than broadening `Operation` with
an `any` payload:

```go
type CloudVPSFactory interface {
    New(CloudVPSTokenSource) CloudVPSService
}
```

The CLI-owned `CloudVPSService` interface should use the normalized request and
result types needed by the commands above. `DefaultOptions` provides the real
factory; tests inject a fake. Keep the existing generic `Executor` for
unimplemented command families until their own tickets replace it. This
preserves current auth/S3/billing/support work and makes it impossible for a
CloudVPS unit test to accidentally reach the network.

At the HTTP seam, use `http.NewRequestWithContext` and an injected `Doer`.
Never mutate `http.DefaultClient` or package-global URLs. Go's `net/http`
request API associates the supplied context with dialing, request writing, and
response reading, and `httptest.Server` supplies a loopback server and matching
client for end-to-end transport tests. ([Go `net/http`][go-http],
[Go `httptest`][go-httptest])

## Wait and no-wait behavior

All action-producing mutations wait by default. Add local `--no-wait` to
`create`, `rename`, lifecycle actions, IP add/remove, snapshot create, and
backup operations. A response with no action ID cannot be polled: server
delete, SSH-key mutations, snapshot delete, PTR update, and any other bodyless
or synchronous response finish when their successful HTTP response arrives.
CloudVPS does not publish a second operation that makes those calls waitable.
([v1 OpenAPI][cloudvps-v1])

If an operation whose response normally carries an action is accepted without
an action ID, `--no-wait` may return the accepted resource. The default waiting
path may return success only when the response or an exact read-after-write
check already proves the requested terminal state; otherwise it returns
`outcome_unknown` with the resource ID and observed state. It must not invent an
action ID or silently claim that it waited.

The existing global `--timeout` is documented as the limit for one network
operation, but `newOperationCommand` currently wraps the entire executor call
in that timeout. Default async waiting makes those two meanings observably
different. Preserve the documented meaning by:

- using global `--timeout` as the deadline for each credential process
  (capped as today) and each individual HTTP request;
- adding `--wait-timeout` for the end-to-end action wait, default `10m`;
- allowing `--wait-timeout` from `1s` through `24h`;
- applying it once across all action IDs returned by one mutation;
- making `vps action wait` use the same flag and behavior.

The 10-minute default and 24-hour ceiling are repository-owned safety choices,
not provider guarantees. REG.RU documents no task-duration maximum, poll
interval, long polling, cancellation endpoint, or retention period. A timed-out
wait is not a failed provider action; its error details must preserve the
action ID and last status so the user can resume with `vps action wait`.
([CLI contract](../cli-contract.md), [current timeout wrapper](../../internal/cli/commands.go),
[task queue][cloudvps-actions])

Normalize statuses as follows:

| Wire status | Normalized state | Wait behavior |
| --- | --- | --- |
| `wait`, `new`, `in-progress`, `in_progress` | `pending` | Poll. |
| `completed` | `completed` | Success terminal. |
| `errored`, `failed` | `failed` | Failure terminal. |
| any other non-empty string | `unknown` | Preserve it and return `outcome_unknown`; do not spin forever or call it failed. |

Action IDs are strings in the normalized model. Preserve the provider's raw
`type` and status alongside normalized state because REG.RU documents a
migration from legacy action values to newer chain IDs and use-case-like
types. ([task queue][cloudvps-actions], [v1 OpenAPI][cloudvps-v1])

Polling uses capped exponential backoff with equal jitter: `500ms`, doubling to
`5s`, then randomized in `[delay/2, delay]`. Inject both sleeping and jitter so
tests contain no real delays or nondeterminism. A sleep uses a `time.Timer` and
selects on `ctx.Done()`; every iteration checks the context before issuing the
next request. Go documents cancellation propagation through `Context`, while
`math/rand/v2` is appropriate for non-security scheduling jitter. ([Go
`context`][go-context], [Go `time.Timer`][go-timer], [Go
`math/rand/v2`][go-rand])

If one response yields several action IDs, retain order, de-duplicate IDs, and
wait until all are terminal or one fails. `--no-wait` returns the same mutation
result shape immediately with `waited: false`; it does not change confirmation,
retry, or error rules.

## Output contract

Keep the existing outer envelopes exactly:

```json
{
  "schemaVersion": "regru.cli/v1",
  "ok": true,
  "command": "vps get",
  "data": {},
  "warnings": []
}
```

```json
{
  "schemaVersion": "regru.cli/v1",
  "ok": false,
  "command": "vps action wait",
  "error": {
    "code": "timeout",
    "message": "the CloudVPS action is still pending",
    "exitCode": 124,
    "retryable": true,
    "details": {}
  }
}
```

The envelope is already implemented and tested. CloudVPS changes only `data`
and typed error details. ([output implementation](../../internal/cli/output.go),
[output tests](../../internal/cli/execute_test.go))

New CloudVPS data fields use lower camel case consistently. The repository
currently mixes lower camel case (`projectSelected`) with snake case
(`dry_run`) in command data; do not extend that inconsistency into the new
resource schema. Existing dry-run field names remain unchanged for
compatibility. ([profile commands](../../internal/cli/profile_commands.go),
[runtime](../../internal/cli/runtime.go))

### Success data

- List commands return a JSON array directly; an empty result is `[]`, never
  `null`.
- Singular reads return the normalized object directly.
- `vps create` returns `{ "server": Server, "actions": [Action],
  "waited": bool, "completed": bool }`.
- Other action-producing mutations return `{ "resourceId": string,
  "actions": [Action], "waited": bool, "completed": bool }`.
- Immediate deletes return `{ "resourceId": string, "deleted": true }`.
- Rename, PTR, and SSH-key writes return the resulting normalized resource.
- `vps action wait` returns the terminal normalized `Action`.

The minimum normalized objects are:

```text
Server:
  id, name, status, subStatus, region, hostname, ipv4, ipv6, ptr,
  vcpus, memoryMiB, diskGiB, diskUsageGiB, sizeSlug, imageId,
  imageSlug, locked, backupsEnabled, createdAt, serviceId

Action:
  id, type, rawStatus, state, resourceId, resourceType, region,
  createdAt, completedAt

IPAddress:
  id, address, type, status, ptr, region, serverId, createdAt

SSHKey:
  id, name, fingerprint, publicKey

Image:
  id, slug, name, type, distribution, region, private,
  minDiskGiB, sizeGiB, createdAt

Plan:
  id, slug, name, planLine, unit, region, vcpus, memoryGiB, diskGiB,
  videocards, pricePerHour, pricePerMonth
```

Use strings for action IDs because the provider accepts numeric and
`chain_*` forms. Keep server, image, key, and IP numeric IDs numeric when the
published schema does; accept numeric-or-string only in wire DTOs. Preserve
provider timestamps as strings because v1 returns timezone-free date-time
text, so converting it to RFC 3339 would invent a timezone. Keep both price
fields as decimal strings in the normalized model so output never passes
currency through binary floating point. ([v1 OpenAPI][cloudvps-v1], [v2
OpenAPI][cloudvps-v2])

Plain records use escaped tab-separated fields in these stable orders:

```text
server:   id name status region ipv4 ipv6 sizeSlug imageSlug locked backupsEnabled
action:   id type state rawStatus resourceId region
ip:       address type status serverId ptr region
ssh-key:  id name fingerprint publicKey
snapshot: id slug name type region private sizeGiB
image:    id slug name type region private minDiskGiB sizeGiB
plan:     id slug name planLine unit region vcpus memoryGiB diskGiB pricePerHour pricePerMonth
```

Human output is not an automation contract and may use tables. It must still
avoid ANSI when color is disabled and never render credentials, authorization
headers, raw response bodies, or helper commands.

### Error mapping

Add stable constructors without changing existing exit meanings:

| Condition | Code | Exit | Retryable |
| --- | --- | ---: | --- |
| Missing `cloudvps.token` | existing `credential_required` | 3 | false |
| Provider `401`/`403` | `cloudvps_authentication_failed` | 5 | false |
| Provider `404` | `resource_not_found` | 6 | false |
| Provider validation/rejection | `provider_error` | 6 | false |
| Transport failure or retryable `5xx`/`429` read | existing `network_error` | 6 | true |
| Terminal `failed`/`errored` action | `cloudvps_action_failed` | 6 | false |
| Undecodable documented envelope | `provider_contract_drift` | 8 | false |
| Ambiguous mutation delivery or unknown action state | existing `outcome_unknown` | 10 | false |
| Wait deadline | existing `timeout` | 124 | true |
| Context cancellation | existing `interrupted` | 130 | false |

Do not use `private_contract_drift` for the public CloudVPS API, and do not use
the existing portal-specific `AuthenticationExpired` message, which tells the
user to run browser login. CloudVPS authentication remediation is to configure
or refresh the selected profile's credential process.

Error details may include `httpStatus`, normalized `providerCode`,
`requestId` when present, `resourceId`, `actionId`, `actionStatus`, and
`actionType`. They must never include the bearer token, request headers,
response body, helper stderr, or helper command. The v1 schema says provider
error codes are strings, but the observed first-party endpoint can return a
number, so normalize the value to string. ([issue #11 resolution][issue-11],
[v1 OpenAPI][cloudvps-v1])

## Confirmation, dry-run, force, and retry rules

Use the existing central confirmation gate for every CloudVPS write, which is
stricter than the map's minimum requirement for destructive and financial
changes and therefore preserves the established CLI contract. Prompts should
name the action and target, for example `Delete CloudVPS server 123? [y/N]`,
but JSON/plain output remains untouched. `--no-input` without `--force`
returns `confirmation_required` before credential resolution.

`--dry-run` performs documented local validation and bounded input-file
reading, then emits only the existing redacted fields: account alias, canonical
action, and argument count. It cannot verify that a plan, image, snapshot,
server, IP, or SSH key exists because the zero-network guarantee forbids that.
Calling such a preview "provider validated" would be impossible. ([CLI
contract](../cli-contract.md), [runtime](../../internal/cli/runtime.go))

Do not automatically replay create, action, IP-add, or other mutations after
an ambiguous transport failure. The provider publishes no idempotency key or
safe replay contract. When no action ID or created resource can be reconciled
unambiguously, return `outcome_unknown`. Read retries may be conservative and
bounded by the per-request timeout; mutation retries require an exact
read-after-write reconciliation or explicit user retry. ([issue #11
resolution][issue-11])

## Fixture-backed tests

Use checked-in, synthetic, secret-free JSON fixtures for every documented wire
variant. `net/http/httptest.NewTLSServer` should assert method, escaped path,
query, bearer-header presence using a synthetic sentinel, content type, and
request JSON, then return the fixture. Tests must assert that the sentinel is
absent from stdout, stderr, returned errors, and serialized snapshots. Go
documents `httptest.Server` as a loopback test server with a matching client.
([Go `httptest`][go-httptest])

Minimum provider fixtures:

- server list under both `reglet` and `reglets`; singular server with
  `disk_usage`; create with zero, one, and multiple `links.actions`;
- wrapped and direct action objects; integer and `chain_*` IDs; every pending
  spelling; completed, failed, errored, and unknown statuses;
- boolean and `0/1` resource fields;
- IP list under the documented and OpenAPI-mismatched collection keys; delete
  success as both documented `204` and current-schema `200`;
- SSH key result under both `ssh_key` and `ssh_keys`;
- snapshots and v2 plans/images with one and multiple pages;
- provider errors with numeric and string codes, empty body, oversized body,
  malformed JSON, duplicate fields, and unexpected trailing JSON;
- cancellation during request and during sleep, per-request timeout, wait
  timeout, retryable read failure, and ambiguous mutation transport failure.

Minimum CLI tests:

- exact help/completion tree including aliases and all local flags, with zero
  factory and credential calls;
- account precedence and missing-account failure before factory construction;
- every required/cross-field validation failure before confirmation;
- every mutation in dry-run, declined-confirmation, `--no-input`, and
  `--force` modes, proving call order;
- default wait, `--no-wait`, timeout with resumable action details, terminal
  failure, unknown status, and multi-action completion;
- exact JSON and plain fixtures for every resource kind and mutation outcome;
- JSON empty arrays, one trailing LF, stdout/stderr separation, and secret
  sentinel blocking;
- `get/show`, `create/deploy`, and `delete/destroy` producing identical
  behavior and canonical JSON command names;
- compatibility equivalence of `vps ips ID` and `vps ip list --server ID`.

`httptest` covers the real request encoder and response decoder. Separate fake
`CloudVPSService` CLI tests cover Cobra parsing and rendering. Separate fake
`Sleeper`/`JitterSource` waiter tests cover exact poll sequences without real
time. This three-layer split makes success and failure fixtures meaningful
without coupling CLI tests to provider JSON.

## Inconsistencies and impossible literal requirements

These must be resolved as stated before coding, rather than left to incidental
implementation behavior:

1. **`show/deploy/destroy` versus the established tree.** They are aliases of
   `get/create/delete`; there are no distinct provider operations.
2. **"Full CloudVPS" versus issue scope.** Issue #5 omits several v1 resources
   and action types. Completion means the issue's surface, not every OpenAPI
   path.
3. **`ip show` has no current OpenAPI operation.** The older HTML documentation
   mentions `GET /ips/{ip}`, but it is absent from the current v1 OpenAPI.
   Implement `ip show` as `GET /ips` plus exact client-side address match; do
   not call an undocumented route. ([additional IP list][cloudvps-ip-list],
   [v1 OpenAPI][cloudvps-v1])
4. **Not every mutation is waitable.** Several successful operations return no
   action ID. Wait only when a response contains one.
5. **Timeout meaning currently conflicts.** The global contract says one
   network operation; current code limits the whole handler. Async waiting
   requires the separate `--wait-timeout` described above.
6. **CloudVPS readiness currently points at unusable sources.** Portal session
   and opaque reference state cannot authenticate this public client; the lazy
   helper can but is ignored by readiness output.
7. **Account identity cannot always be proven.** There is no token
   introspection/current-principal endpoint, and empty inventory exposes no
   `service_id`.
8. **Dry-run cannot validate remote existence or pricing.** It is deliberately
   zero-network.
9. **A timed-out action is not a failed action.** The action ID and last status
   must survive in error details for resumable waiting.
10. **A guaranteed "low-cost" server cannot be hard-coded.** Plan prices and
    availability are region- and time-dependent. The live verification flow
    must first list current hourly plans, have a human choose an acceptable
    price, then create and delete the disposable server. ([plan
    catalog][cloudvps-plans])

## Disposable live-verification flow to document after implementation

The separate authorized live ticket should use this sequence, not a fixed plan
slug or price:

```sh
regru --account verify --json vps plan list --region REGION --unit hour
regru --account verify --json vps image list --region REGION --type distribution
regru --account verify --dry-run vps create \
  --region REGION --size SELECTED_PLAN --image SELECTED_IMAGE --name regru-e2e
regru --account verify --force --json vps create \
  --region REGION --size SELECTED_PLAN --image SELECTED_IMAGE \
  --name regru-e2e --no-wait
regru --account verify --json vps action wait ACTION_ID --wait-timeout 20m
regru --account verify --force --json vps delete SERVER_ID
regru --account verify --json vps list
```

Before the real create, a human must approve the currently returned hourly and
monthly prices and the spend ceiling. After delete, list inventory to reconcile
absence because server delete returns `204` without a documented action ID.
([server deletion][cloudvps-delete], [v1 OpenAPI][cloudvps-v1])

## Primary sources

- [Wayfinder map: agent-oriented REG.RU CLI][issue-1]
- [Task: implement CloudVPS and VPS commands][issue-5]
- [Research resolution: establish the CloudVPS API contract][issue-11]
- [Repository CLI contract](../cli-contract.md)
- [Repository profile and credential-process contract](../profile-secret-contract.md)
- [REG.RU CloudVPS documentation][cloudvps-home]
- [REG.RU CloudVPS v1 OpenAPI][cloudvps-v1]
- [REG.RU CloudVPS v2 OpenAPI][cloudvps-v2]
- [Cobra v1.10.2 `Command` API][cobra-command]
- [Cobra official flags guide][cobra-flags]
- [Go standard-library `context` documentation][go-context]
- [Go standard-library `net/http` documentation][go-http]
- [Go standard-library `net/http/httptest` documentation][go-httptest]

[issue-1]: https://github.com/adinvadim/reg-ru-cli/issues/1
[issue-5]: https://github.com/adinvadim/reg-ru-cli/issues/5
[issue-11]: https://github.com/adinvadim/reg-ru-cli/issues/11#issuecomment-5130979395
[cobra-command]: https://pkg.go.dev/github.com/spf13/cobra@v1.10.2#Command
[cobra-flags]: https://cobra.dev/docs/how-to-guides/working-with-flags/
[go-context]: https://pkg.go.dev/context
[go-http]: https://pkg.go.dev/net/http
[go-httptest]: https://pkg.go.dev/net/http/httptest
[go-timer]: https://pkg.go.dev/time#Timer
[go-rand]: https://pkg.go.dev/math/rand/v2
[cloudvps-home]: https://developers.cloudvps.reg.ru/
[cloudvps-auth]: https://developers.cloudvps.reg.ru/getting-started/authentication.html
[cloudvps-v1]: https://api.cloudvps.reg.ru/v1/openapi.json
[cloudvps-v2]: https://api.cloudvps.reg.ru/v2/api/swagger.json
[cloudvps-reglets]: https://developers.cloudvps.reg.ru/reglets/index.html
[cloudvps-create]: https://developers.cloudvps.reg.ru/reglets/add.html
[cloudvps-delete]: https://developers.cloudvps.reg.ru/reglets/delete.html
[cloudvps-server-info]: https://developers.cloudvps.reg.ru/reglets/info.html
[cloudvps-actions]: https://developers.cloudvps.reg.ru/getting-started/taskqueue.html
[cloudvps-plans]: https://developers.cloudvps.reg.ru/sizes/index.html
[cloudvps-images]: https://developers.cloudvps.reg.ru/images/list.html
[cloudvps-ip-list]: https://developers.cloudvps.reg.ru/add-ip/list.html
