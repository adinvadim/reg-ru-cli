# CloudVPS action polling and mutation-safety contract

Research date: 2026-07-31

Scope: implementation guidance for
[Task: implement CloudVPS and VPS commands](https://github.com/adinvadim/reg-ru-cli/issues/5).
This note narrows the broader
[CloudVPS API contract](cloudvps-api-contract.md) to asynchronous actions,
waiting, retries, ambiguous mutation outcomes, and read-after-write
reconciliation.

## Executive conclusion

REG.RU exposes one polling endpoint,
`GET /v1/actions/{action_id}`, but does not publish a polling interval, task
duration bound, cancellation endpoint, webhook, action-retention period,
rate-limit quota, retry guarantee, or idempotency mechanism. The API is also
mid-migration between integer legacy actions and string `chain_*` actions.
The live OpenAPI and first-party HTML documentation disagree on the action
submission envelope.

The implementation should therefore:

1. Decode integer and string IDs and all first-party action envelope variants
   into one normalized action.
2. Treat `wait` and `new` as queued, `in-progress` and `in_progress` as
   running, `completed` as success, and `errored` and `failed` as terminal
   failure.
3. Poll only with cancellable, jittered backoff under the command deadline.
   A local timeout or Ctrl-C stops observation; it does not cancel or fail the
   provider action.
4. Automatically retry reads and action polling only. Never automatically
   replay a CloudVPS mutation after an ambiguous transport failure.
5. Preserve the action ID, last raw status, resource ID, and provider request
   ID in every wait failure so the user can resume with `action wait`.
6. Re-read the affected resource after an accepted mutation. Use that read to
   present current state and to reconcile ambiguous operations where there is
   strong evidence, not to override an explicit terminal action failure.

Items described as **fact** below are supported by first-party REG.RU
documentation, the live OpenAPI, repository source/docs, or an Internet
standard. Items described as **recommendation** are implementation inferences
made where REG.RU publishes no contract.

## Published action contract

### Endpoints and response shapes

**Fact.** The current v1 OpenAPI declares:

| Request | Published success | Published body |
| --- | --- | --- |
| `POST /v1/reglets` | `201` | `{ "reglet": {...}, "links": { "actions": [Action \| ActionByStr] } }` |
| `POST /v1/reglets/{resource_id}/actions` | `201` | an `Action` object directly |
| `GET /v1/actions/{action_id}` | `200` | `{ "action": Action \| ActionByStr }` |
| `PUT /v1/reglets/{resource_id}` | `201` | `{ "reglet": {...}, "links": { "actions": [Action] } }` |
| `DELETE /v1/reglets/{resource_id}` | `204` | no body |

The action lookup path accepts decimal IDs and strings matching
`chain_<digits>`. `Action.id` is an integer; `ActionByStr.id` is a string.
Both schemas are nullable. The action submission route declares only the
integer-ID `Action`, even though REG.RU's migration note demonstrates newer
string IDs. ([v1 OpenAPI](https://api.cloudvps.reg.ru/v1/openapi.json))

**Fact.** The HTML action pages instead wrap lifecycle submissions in
`{ "action": {...} }`. Some responses have additional siblings: the clone
example includes `reglet_id`, and the backup-switch example contains only a
minimal `action.status` plus `reglet_id`. The create page returns a resource
plus `links.actions`; its example resource uses `resource_id` rather than
`id`, is `locked: 1`, and has status `new` while the create action is still
running. ([create](https://developers.cloudvps.reg.ru/reglets/add.html),
[reboot](https://developers.cloudvps.reg.ru/reglets/reboot.html),
[power on/off](https://developers.cloudvps.reg.ru/reglets/power_on_off.html),
[clone](https://developers.cloudvps.reg.ru/reglets/clone.html),
[backup switch](https://developers.cloudvps.reg.ru/reglets/switch_backups.html))

**Fact.** REG.RU explicitly says it is gradually replacing the mechanism named
`actions`, that the newer format differs to preserve compatibility, and that
the format will change more substantially after migration. Its newer example
uses ID `chain_119123`, region `service`, and type `StopServerUseCase`, rather
than the legacy integer ID and action type `stop`.
([task queue](https://developers.cloudvps.reg.ru/getting-started/taskqueue.html))

**Recommendation.** Use a permissive wire layer and a strict normalized model:

```go
type Action struct {
	ID             string
	RawStatus      string
	Phase          ActionPhase // queued, running, succeeded, failed, unknown
	Type           string
	ResourceID     string
	ResourceType   string
	Region          string
	CreatedAt      string
	StartedAt      string
	CompletedAt    string
}

type ActionSubmission struct {
	Actions          []Action
	Reglet           *Reglet
	RelatedRegletID  string
}
```

The wire decoder should:

- accept a direct action, `{ "action": action }`, and
  `{ "links": { "actions": [...] } }`;
- accept JSON numbers or strings for action and resource IDs and normalize
  them to decimal/text strings without floating-point conversion;
- accept `id` or `resource_id` as the created reglet identifier, preferring
  `id` when both agree and rejecting the response when both are present but
  disagree;
- allow absent/null timestamp and descriptive fields, and preserve unknown
  action types;
- ignore unknown response siblings after size-bounded decoding, but retain the
  documented `reglet_id` sibling;
- require a non-empty action ID from a submission only when a non-terminal
  action must be polled. A terminal `completed`, `failed`, or `errored`
  response can be useful even if an old endpoint omitted its ID. If a GET of
  a known action returns a usable status but omits the body ID, retain the ID
  from the requested path and attach a contract-deviation warning;
- reject malformed IDs, objects in scalar fields, duplicate security-relevant
  keys, trailing JSON, and oversized bodies as provider contract errors.

**Recommendation.** Dates have no published timezone or format guarantee.
Keep their wire strings for output and parse them only opportunistically. Do
not infer completion from `completed_at`: first-party examples show non-null
completion timestamps on actions whose status is still `in-progress`.

### Status normalization

**Fact.** The legacy queue page documents `new`, `in-progress`, `errored`, and
`completed`. The live `Action` schema adds `wait`; `ActionByStr` additionally
contains `in_progress` and `failed`.
([task queue](https://developers.cloudvps.reg.ru/getting-started/taskqueue.html),
[v1 OpenAPI](https://api.cloudvps.reg.ru/v1/openapi.json))

**Recommendation.** Use the following exact mapping and always retain the raw
value:

| Raw status | Normalized phase | Terminal | CLI result |
| --- | --- | ---: | --- |
| `wait` | `queued` | no | keep polling |
| `new` | `queued` | no | keep polling |
| `in-progress` | `running` | no | keep polling |
| `in_progress` | `running` | no | keep polling |
| `completed` | `succeeded` | yes | success, then reconcile resource |
| `errored` | `failed` | yes | provider action failure |
| `failed` | `failed` | yes | provider action failure |
| anything else | `unknown` | unknown | stop with public-contract drift; do not guess |

**Recommendation.** Do not case-fold or replace punctuation generically. Only
the two documented running spellings should collapse. Treating every unknown
value as pending could wait until timeout on a future terminal state; treating
it as success could report a failed mutation as successful. Fail closed while
returning the ID and raw status so a newer client or the user can inspect it.

The action schemas contain no failure code, reason, or message field. A
terminal `failed`/`errored` result should therefore report the normalized
action metadata and a best-effort current resource snapshot, not invent a
failure reason. ([v1 OpenAPI](https://api.cloudvps.reg.ru/v1/openapi.json))

## Polling and waiting

### What REG.RU does and does not promise

**Fact.** REG.RU instructs clients to check an asynchronous operation with
`GET /v1/actions/<id>` and inspect `status`. It publishes no interval, long
poll, webhook, maximum task duration, retention duration, consistency window,
or completion SLA. The live OpenAPI has one read-only operation on the action
path and no action cancellation route.
([task queue](https://developers.cloudvps.reg.ru/getting-started/taskqueue.html),
[v1 OpenAPI](https://api.cloudvps.reg.ru/v1/openapi.json))

**Fact.** The implementation ticket requires mutations to wait by default,
support `--no-wait`, and expose action show/wait with cancellation, timeout,
and jittered backoff. The repository contract currently gives network
operations a default `--timeout 30s`, caps it at `5m`, propagates the first
Ctrl-C through the command context, and reserves exit `124` for deadline
expiry, `130` for interruption, and `10` for a mutation whose outcome cannot
be verified. ([implementation ticket](https://github.com/adinvadim/reg-ru-cli/issues/5),
[CLI contract](../cli-contract.md),
[command timeout wrapper](../../internal/cli/commands.go),
[error model](../../internal/cli/errors.go))

### Recommended wait algorithm

This policy is an implementation recommendation, not a provider guarantee:

1. Classify the submission response before making another request. Return
   immediately for a terminal action. With `--no-wait`, return every supplied
   action and resource identifier without polling.
2. For an explicit `action wait <id>`, issue the first GET immediately. For a
   just-submitted pending action, wait before the first GET because the
   submission already supplied an observation.
3. Start at a one-second base delay, double to a five-second cap, and apply
   equal jitter in `[0.5 * delay, delay]`. A valid pending response advances
   the same bounded poll cadence; a transient error records the last
   observation and also waits before retrying.
4. Use a context-aware timer (`select` on timer and `ctx.Done()`), never
   `time.Sleep`. Use the same command deadline for submission, polling, and
   final reconciliation so the advertised timeout is a hard upper bound.
5. On `200`, decode and classify. On `401`, other non-retryable `4xx`, malformed
   JSON, a missing action, or an unknown status, stop immediately. On a
   transport error, `408`, `429`, `500`, `502`, `503`, or `504`, keep polling
   with backoff while time remains.
6. If `Retry-After` appears on a retryable read response, parse either
   delta-seconds or HTTP-date and wait at least that long, still bounded by the
   command context. If it is absent or invalid, use local backoff. HTTP defines
   both forms; `429` may include it. REG.RU does not document using either.
   ([RFC 9110 §10.2.3](https://www.rfc-editor.org/rfc/rfc9110.html#section-10.2.3),
   [RFC 6585 §4](https://www.rfc-editor.org/rfc/rfc6585.html#section-4))
7. On `completed`, perform the operation-specific read-after-write check below
   with the remaining time. Action completion remains the authoritative
   operation result; delayed resource visibility becomes a warning, not an
   action failure.

Inject the clock, timer, and jitter source. Tests should use a deterministic
sequence and assert requested delays rather than sleep.

### Cancellation and timeout semantics

**Fact.** There is no documented remote cancellation operation. Canceling the
HTTP request or local context only stops the CLI from observing the action.
It does not stop a create, resize, reboot, clone, restore, or other accepted
provider task. ([v1 OpenAPI](https://api.cloudvps.reg.ru/v1/openapi.json))

**Recommendation.**

- If context cancellation/deadline occurs before a mutation is handed to
  `http.Client.Do`, return the ordinary `interrupted`/`timeout` error.
- Once a mutation has been handed to the transport, a transport error before
  an authoritative HTTP response is ambiguous. Return `outcome_unknown`
  (exit 10), not a retryable timeout, unless reconciliation proves the result.
- Once a `201` and action ID have been decoded, acceptance is known. A later
  local deadline returns `timeout` (124); Ctrl-C returns `interrupted` (130).
  Both errors must include `action_id`, `last_status`, `resource_id`,
  `resource_type`, and a safe `resume_command`.
- Set `retryable: false` on a timed-out *mutation command*: rerunning it would
  resubmit the mutation. Set `retryable: true` on the equivalent read-only
  `action wait` timeout: repeating the wait is safe.
- Never emit a synthetic `failed` action on timeout/cancel. Wording should say
  that waiting stopped and the provider operation may still be running.

The normalized action must be stored before rendering progress. Progress is
stderr-only; structured stdout remains reserved for the single final success
envelope.

## Rate limits, `Retry-After`, and retry safety

### Published evidence and gaps

**Fact.** Neither live CloudVPS OpenAPI document declares a `429` response,
rate-limit headers, `Retry-After`, request quotas, an idempotency header,
idempotency keys in request bodies, or request deduplication. The v1 OpenAPI
declares no header parameters at all. The getting-started and task-queue pages
also provide no retry or rate-limit policy.
([getting started](https://developers.cloudvps.reg.ru/getting-started/index.html),
[task queue](https://developers.cloudvps.reg.ru/getting-started/taskqueue.html),
[v1 OpenAPI](https://api.cloudvps.reg.ru/v1/openapi.json),
[v2 OpenAPI](https://api.cloudvps.reg.ru/v2/api/swagger.json))

Unauthenticated probes on 2026-07-31 returned `401` with `X-Request-ID` but no
rate-limit or `Retry-After` header. This is only a dated observation of
rejected requests, not evidence that authenticated traffic is unlimited.
`X-Request-ID` is useful diagnostic correlation, but REG.RU does not document
it as an idempotency or replay key.
([server-list endpoint](https://api.cloudvps.reg.ru/v1/reglets),
[action endpoint](https://api.cloudvps.reg.ru/v1/actions/1))

**Fact.** HTTP defines GET as safe and defines PUT and DELETE as idempotent, but
also says a client should not automatically retry a non-idempotent method
unless it knows the semantics are idempotent or knows the original request was
not applied. ([RFC 9110 §9.2.2](https://www.rfc-editor.org/rfc/rfc9110.html#section-9.2.2))

**Fact.** Go's `net/http.Transport` automatically replays only a narrow class
of network failures on a successfully reused connection. Its documented
replay classification covers GET/HEAD/OPTIONS/TRACE or requests marked with
`Idempotency-Key`/`X-Idempotency-Key`; it also requires no body or `GetBody`.
([Go `net/http.Transport`](https://pkg.go.dev/net/http#Transport),
[Go transport retry source](https://go.dev/src/net/http/transport.go#L815))

**Recommendation.** Do not send either idempotency header. REG.RU does not
advertise support, and setting one would also tell Go's transport that a POST
is replayable. Do not add a generic retrying RoundTripper around mutations.
The standard transport may safely recover when it knows nothing was written;
any mutation error visible above `http.Client.Do` should still be treated as
ambiguous.

### Operation policy

**Recommendation.**

| Operation | Automatic retry | Reconciliation after ambiguous result |
| --- | --- | --- |
| Any GET, including action polling | yes, for transient transport/408/429/selected 5xx | repeat the same read |
| `POST /reglets` create | never | list/get and show candidates by returned ID if one was received; otherwise compare name, region, plan, image, and creation time only as hints |
| `POST .../actions` start/stop | never | compare pre-state and current state; use any action in `links.actions`; desired state can prove effect only when pre-state differed |
| `POST .../actions` reboot | never | action ID is the only strong evidence; an active server cannot prove a reboot happened |
| `POST .../actions` rebuild/resize/clone/restore/snapshot/backups/password reset | never | use action ID; additionally check operation-specific fields/resources |
| PUT rename/PTR | no blind retry | GET current value; success if it equals the requested value, otherwise report ambiguity rather than assuming the first request was not applied |
| DELETE server/IP/key/snapshot | no blind retry | absence can prove the desired effect; continued presence cannot prove the first request was not accepted |

Names are not documented as unique, so a same-name server is never sufficient
proof that an ambiguous create succeeded. Conversely, absence immediately
after an ambiguous create is not proof that it failed: creation is explicitly
asynchronous. Never automatically submit a second billed create.

An authoritative provider `400` or `401` is not retryable. A mutation `429` or
`5xx` also receives no automatic replay under this conservative policy,
because REG.RU does not document whether such a response can race with queued
work. Return the parsed provider error and `Retry-After` as diagnostics; let
the user reconcile before resubmitting.

## Read-after-write reconciliation

**Fact.** `GET /v1/reglets/{resource_id}` returns the current server under
`reglet`; `GET /v1/reglets` returns servers plus active actions. The HTML uses
`reglets` for the collection while the OpenAPI currently spells it `reglet`,
so the read decoder already needs both. Server states include `new`, `active`,
`off`, `suspended`, and `archive`; the live OpenAPI additionally includes
`ordered`. ([server info](https://developers.cloudvps.reg.ru/reglets/info.html),
[server list](https://developers.cloudvps.reg.ru/reglets/list.html),
[v1 OpenAPI](https://api.cloudvps.reg.ru/v1/openapi.json))

**Recommendation.** Use these checks as best-effort evidence after action
success or ambiguous submission:

| Mutation | Strong postcondition/evidence | Important limitation |
| --- | --- | --- |
| create/deploy | returned reglet/action resource ID becomes readable | final state need not be `active`; `suspended` can be a created server |
| start | status `active` after a different pre-state | if already active before submission, this does not prove acceptance |
| stop | status `off` after a different pre-state | REG.RU warns stopped servers remain billable |
| reboot | terminal action only; GET is presentation | post-state `active` does not prove reboot |
| rename | `name` equals requested value | direct response may already contain the new object |
| resize | `size_slug` equals requested value | only upgrades are documented through this API |
| rebuild/restore | terminal action; current image/status is supplemental | no consistency window is published |
| clone | terminal action plus new `action.resource_id` becoming readable | preserve sibling `reglet_id`; it can identify the source |
| backup enable/disable | `backups_enabled` equals requested boolean | legacy examples use booleans, integers, and strings |
| snapshot | snapshot list contains returned ID/name | name alone is not documented unique |
| additional IP add | IP list for reglet reflects returned addresses/counts | create response can have an empty actions array |
| PTR update | IP record's `ptr` equals requested value | no async action is documented |
| delete | authoritative `204`, or later verified absence after ambiguity | immediate presence can be eventual/pending, not proof of failure |

For an accepted action, poll the action to terminal first, then make one
immediate resource GET. Retry transient errors and short-lived not-found
responses with the remaining wait backoff until the command deadline. If the
action is `completed` but the resource is still not observable, return success
with a `reconciliation_pending` warning and the action ID; do not downgrade the
provider's explicit terminal success.

If an action is `failed`/`errored` but a resource read happens to show the
desired state, report the terminal action failure plus the observed state as a
warning. External work or a partial effect can produce that combination; the
read does not erase the task result.

`DELETE /reglets/{id}` is documented as synchronous `204` with an empty body,
not as an action-producing request. A received `204` is success and needs no
invented polling. Only an ambiguous transport failure calls for absence
reconciliation. ([server deletion](https://developers.cloudvps.reg.ru/reglets/delete.html),
[v1 OpenAPI](https://api.cloudvps.reg.ru/v1/openapi.json))

## Error and output recommendations

Add public-API-specific errors without reusing the repository's
`private_contract_drift` wording:

| Condition | Suggested stable code | Exit | Retryable |
| --- | --- | ---: | ---: |
| terminal `failed`/`errored` | `cloudvps_action_failed` | 6 | false |
| malformed accepted/poll response or unknown status | `cloudvps_contract_drift` | 6 | false |
| transient read exhaustion | `network_error` | 6 | true |
| ambiguous mutation transport result | `outcome_unknown` | 10 | false |
| mutation wait deadline with known action | `timeout` | 124 | false; resume wait |
| explicit action-wait deadline | `timeout` | 124 | true |
| Ctrl-C while waiting | `interrupted` | 130 | false on mutation; wait is resumable |

Provider diagnostics should retain HTTP status, provider `code` and `message`,
`X-Request-ID` when present, action ID, raw status, resource ID/type, and the
last observation time. Never include the bearer token, request authorization
header, or entire unclassified response body.

Default mutation success should contain the final normalized action(s) and
the reconciled resource snapshot. `--no-wait` success should make
`accepted: true`, `terminal: false`, and `action_id` explicit rather than look
like a completed mutation. If a creation response contains multiple actions,
wait for all returned non-null actions; one terminal failure makes the command
fail while retaining every action result.

## Fixture and test matrix

All tests should be hermetic `httptest` fixtures with an injected clock and
jitter source. Assert request count and method as well as output; the most
important safety property is that ambiguous mutations are sent exactly once.

### Wire normalization

| Fixture | Expected assertion |
| --- | --- |
| direct integer action with `in-progress` | ID normalized to decimal string; phase `running` |
| wrapped `chain_123` action with `in_progress` and UseCase type | ID/type preserved; phase `running` |
| create response with `reglet.resource_id` and `links.actions` | created ID recovered; action list normalized |
| direct/wrapped `completed` action | terminal success; no poll if supplied by mutation |
| `failed` and `errored` | both terminal failure with distinct raw status |
| `wait` and `new` | both queued |
| minimal terminal action containing only `status` | accepted for rendering; no ID required |
| pending submission action missing ID | `outcome_unknown`; never resubmit or spin |
| polled action missing body ID but having a valid status | retain requested path ID; emit contract-deviation warning |
| null action and null items in actions array | nulls ignored only if another usable result exists; otherwise contract error |
| integer/string resource IDs | lossless string normalization |
| conflicting reglet `id` and `resource_id` | contract drift |
| legacy `started_at`, absent timestamps, timezone-free timestamps | preserved as optional strings |
| unknown status | raw value preserved; immediate contract-drift failure |
| unknown type and extra sibling fields | type preserved; safe extras ignored |
| duplicate keys, scalar/object type confusion, trailing JSON, oversized body | bounded decode failure; no secret/raw-body echo |

### Poll behavior

| Sequence | Expected assertion |
| --- | --- |
| `new → in-progress → completed` | delays follow injected jitter; final success and reconcile GET |
| `wait → in_progress → failed` | stop on failure; no further poll |
| initial action already completed | zero action GETs |
| `--no-wait` with pending action | one mutation, zero action GETs, accepted/non-terminal output |
| poll `500 → 503 → completed` | reads retried within deadline; mutation not repeated |
| poll `429` with delta-seconds `Retry-After` | next GET not before requested delay |
| poll `503` with HTTP-date `Retry-After` | HTTP date honored with injected clock |
| invalid/missing `Retry-After` | local jittered backoff used |
| `Retry-After` beyond remaining deadline | deadline wins; no late GET |
| poll `401`, non-retryable `400`, malformed `200` | stop immediately |
| unknown action status | stop immediately, preserve ID/raw status |
| cancel during timer and during in-flight GET | timer/request canceled; no leaked follow-up; resumable details present |
| deadline after accepted action | exit 124, not action failure; mutation `retryable:false` |
| same deadline through `action wait` | exit 124, `retryable:true` |

### Mutation replay safety

| Fixture | Expected assertion |
| --- | --- |
| create connection closes after request is received | exactly one POST; outcome unknown; reconciliation reads only |
| reboot connection closes after receipt | exactly one POST; no attempt to infer success from `active` |
| start/stop already in desired pre-state and no conflicting active action is visible | optional idempotent short-circuit without POST, clearly reported as already satisfied |
| start/stop ambiguous after differing pre-state, then desired state observed | exactly one POST; reconcile as effect observed, with no invented action ID |
| rename ambiguous, subsequent GET has requested name | exactly one PUT; reconcile success |
| rename ambiguous, GET has old name | no blind replay; outcome unknown |
| delete returns `204` empty body | success; no action polling |
| delete connection closes, later GET/list proves absence | exactly one DELETE; reconcile success |
| delete connection closes, resource remains visible | exactly one DELETE; outcome unknown |
| mutation returns `400`, `401`, `429`, or `503` | exactly one mutation request; no automatic replay |
| client construction | no `Idempotency-Key` or `X-Idempotency-Key`; bearer header never appears in diagnostics |

### Read-after-write

| Fixture | Expected assertion |
| --- | --- |
| completed create, GET is briefly 404 then readable | bounded read retry; return resource |
| completed action, resource remains unavailable to deadline | success plus `reconciliation_pending`, not action failure |
| completed start/stop with expected state | final output includes reconciled reglet |
| completed reboot with active state | action proves reboot; state is supplemental only |
| failed action but desired resource state observed | action failure retained with state warning |
| clone response with source `reglet_id` and new action resource ID | both IDs preserved; reconcile new resource ID |
| multiple returned create actions, all complete | success only after every action is terminal-success |
| multiple actions, one fails | fail once all already-known terminal results are retained; no new mutation |

## Primary sources

- [REG.RU CloudVPS developer documentation](https://developers.cloudvps.reg.ru/)
- [CloudVPS v1 OpenAPI](https://api.cloudvps.reg.ru/v1/openapi.json)
- [CloudVPS v2 OpenAPI](https://api.cloudvps.reg.ru/v2/api/swagger.json)
- [Task queue and action migration note](https://developers.cloudvps.reg.ru/getting-started/taskqueue.html)
- [Create server](https://developers.cloudvps.reg.ru/reglets/add.html)
- [Power on/off](https://developers.cloudvps.reg.ru/reglets/power_on_off.html)
- [Reboot](https://developers.cloudvps.reg.ru/reglets/reboot.html)
- [Delete server](https://developers.cloudvps.reg.ru/reglets/delete.html)
- [Server info](https://developers.cloudvps.reg.ru/reglets/info.html)
- [Server list](https://developers.cloudvps.reg.ru/reglets/list.html)
- [Clone server](https://developers.cloudvps.reg.ru/reglets/clone.html)
- [RFC 9110: HTTP Semantics](https://www.rfc-editor.org/rfc/rfc9110.html)
- [RFC 6585: 429 Too Many Requests](https://www.rfc-editor.org/rfc/rfc6585.html)
- [Go `net/http` package documentation](https://pkg.go.dev/net/http)
- [Repository CLI contract](../cli-contract.md)
- [Implementation ticket](https://github.com/adinvadim/reg-ru-cli/issues/5)
