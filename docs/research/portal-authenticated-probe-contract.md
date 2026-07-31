# Browser-authenticated portal probe contract

Research date: 2026-07-31

## Scope and evidence boundary

This note defines the minimum session probe for
[`Prototype: implement browser-authenticated portal sessions`](https://github.com/adinvadim/reg-ru-cli/issues/15).
It answers four questions: how the broker can recognize an authenticated
REG.RU principal, how it detects a different account, what expiry means, and
where the probe must execute.

The evidence is limited to current first-party REG.RU/REG.Cloud help, public
frontend assets, anonymous protocol observations, and the repository's prior
redacted authenticated observation. This investigation did not use a logged-in
session, read a cookie or token value, require login/CAPTCHA, or change
provider state. Anonymous observations recorded only status names, response
keys, CORS metadata, and GraphQL `__typename`.

REG.RU does not publish the portal authentication protocol, its status enum,
or its session lifetime. The URLs and shapes below are current private web
implementation, not a supported API contract.

## Decision

Use a two-stage, fail-closed probe:

1. Run the current `login.reg.ru` refresh choreography inside an isolated
   JavaScript world attached to the dedicated first-party browser profile.
   Reduce the response in that world to an auth state and an opaque,
   keyed principal digest. Do not return cookies, CSRF values, raw response
   bodies, login names, user IDs, or contracts over CDP.
2. Only after the refreshed principal matches the profile binding, run
   adapter-specific read-only probes. For REG.Cloud, the current
   `environments` query is a useful secondary probe: its top-level union
   currently distinguishes `Environments`, `Unauthorized`, and
   `EnvironmentNotFound`.

Do not export the cookie jar into Go's HTTP client. A browser context, not a
cookie, is the session credential boundary.

The core state reported to the CLI should be:

```text
unknown
  -> authenticated       refresh recognized; principal digest matches
  -> reauth-required     no current user session or explicit auth rejection
  -> account-mismatch    refresh recognized; principal digest differs
  -> incompatible        auth envelope/status/identity shape changed
  -> unavailable         browser or provider transport failed
```

`reauth-required` needs a local reason:

- `not-established` when this context has never completed a successful
  binding;
- `session-lost` when the same bound context was previously authenticated but
  now has no session;
- `explicit-logout` when `regru auth logout` initiated the transition.

The provider evidence does **not** distinguish idle expiry, absolute expiry,
manual logout in another tab, server revocation, password/security changes,
or an IP-binding rejection. The CLI must not claim a precise `expired` cause
or predicted expiry time.

## Current first-party signals

### Core auth refresh

Both the current REG.RU account client and REG.Cloud client use
`https://login.reg.ru` with browser credentials. Their visible auth sequence
is:

```text
GET  /authenticate   obtain the readable csrftoken cookie when absent
POST /refresh        send the cookie-derived CSRF header and browser cookies
POST /logout         terminate the current session
```

The current account client recognizes `session_refreshed` for a successful
refresh and maps response fields `user_id` and `screen_name`. It stores a
screen-name hint and the last-refresh time in origin-local `localStorage`.
The current REG.Cloud client does the same with its own origin-local keys.
Both use a 150-second staleness threshold. REG.Cloud also checks every 30
seconds and on focus; the account application refreshes around route/GraphQL
work. These are frontend scheduling choices, not a provider session TTL.
([account auth client][account-auth], [account application][account-index],
[account GraphQL client][account-gql], [Cloud auth client][cloud-auth])

An anonymous `POST https://login.reg.ru/refresh` observed on 2026-07-31
returned HTTP 200 with this value-free shape:

```json
{
  "success": true,
  "result": {
    "status": "no_user_session"
  }
}
```

The result had no principal fields. Requests with first-party origins
`https://www.reg.ru` and `https://cloud.reg.ru` received matching
`Access-Control-Allow-Origin` plus
`Access-Control-Allow-Credentials: true`. An unrelated origin did not receive
an allow-origin header. Current preflight metadata allows `POST` and the
`x-csrf-token`/`x-csrftoken` headers. ([refresh endpoint][refresh])

An anonymous `GET https://login.reg.ru/authenticate` returned HTTP 200 and set
a `csrftoken` cookie scoped to the `reg.ru` domain. Its value was not read or
retained. This matches the frontend's current CSRF bootstrap, but says nothing
about the authenticated cookie names, attributes, or lifetime.
([authenticate endpoint][authenticate], [account auth client][account-auth])

The repository's earlier redacted authenticated observation saw the browser
perform a `login.reg.ru` session refresh and matched one active browser
context to one profile through a one-way in-memory digest without retaining
the principal. It did not preserve the private response body.
([redacted capability matrix][redacted-matrix],
[authenticated portal observation][authenticated-observation])

### Cloud capability probe

The current REG.Cloud GraphQL client sends browser credentials to
`https://cloudvps-graphql-server.svc.reg.ru/api`. It adds `Service-ID` only
after a Cloud environment is selected. Its middleware first refreshes auth;
an `Unauthorized` result triggers logout handling and routes the UI to auth.
([Cloud GraphQL client][cloud-graphql])

The public panel source currently contains this read-only top-level operation:

```graphql
query environments {
  environments {
    ... on Environments {
      __typename
      environments {
        __typename
        serviceId
      }
    }
    ... on Unauthorized {
      __typename
    }
    ... on EnvironmentNotFound {
      __typename
    }
  }
}
```

An anonymous value-redacted request on 2026-07-31 returned HTTP 200,
`data.environments.__typename == "Unauthorized"`, and no GraphQL errors.
([Cloud operations bundle][cloud-operations],
[Cloud GraphQL endpoint][cloud-graphql-endpoint])

This is a useful secondary contract probe, but not the core identity probe:

- `Environments` proves that the current session can reach this Cloud
  capability; the broker must count or digest service IDs without logging
  them.
- `Unauthorized` means the adapter must stop and require auth refresh.
- `EnvironmentNotFound` is an authenticated capability/domain outcome unless
  a later authorized capture proves otherwise; it must not be relabelled as a
  globally unauthenticated account.
- The query has no `Service-ID` because it discovers environments. Subsequent
  environment-scoped calls must use an explicitly selected opaque service ID.

### Page and local-storage state are hints only

REG.RU's official cabinet guide says an authorized page shows the current
login in its header and exposes a logout action. The current frontend caches
`screen_name` in `localStorage` and dispatches login/logout events when it
changes. ([cabinet guide][cabinet-guide], [account auth client][account-auth],
[Cloud auth client][cloud-auth])

Neither signal is authoritative:

- the SPA shell can load with HTTP 200 before auth is established;
- a route or visible login can be stale while a session expires elsewhere;
- `localStorage` is scoped per origin, so `www.reg.ru` and `cloud.reg.ru` can
  temporarily disagree;
- DOM text and selectors are more brittle than the auth response and would
  expose the account login if copied into diagnostics.

Page route/header state may decide whether to keep waiting during interactive
login, but successful login is complete only after the core refresh probe
passes and the principal is bound.

## Principal binding and account mismatch

An authenticated boolean cannot detect that the user logged into a different
REG.RU cabinet in the profile. Account mismatch must be a first-class result.

The current refresh parser exposes `user_id` and `screen_name`, while the
frontends use `screen_name` as their login-change signal. REG.RU also
documents that one person may have multiple personal cabinets, and no
supported in-session account selector was found. ([account auth
client][account-auth], [multiple-cabinet guidance][multiple-cabinets])

For an unbound profile, `auth login` should:

1. require `success == true`, `status == "session_refreshed"`, and the current
   expected non-empty identity fields;
2. derive a keyed digest in the browser's isolated execution world;
3. return only the digest to the broker;
4. store the binding and its key inside the dedicated private browser-session
   boundary, never ordinary config.

For a bound profile, every refresh must derive the digest the same way and use
constant-time comparison. A mismatch stops all capability probes. The CLI
must not overwrite the binding, select a `USER-ID`, reuse old Cloud
`Service-ID` values, or silently attach the new account to the old profile.

Because REG.RU publishes no stability contract for `user_id` or
`screen_name`, the initial adapter should digest both fields with explicit
field framing. A change in either fails closed and asks the user to
reauthenticate or explicitly rebind the profile. A later authorized capture
may establish `user_id` alone as the durable principal key.

An implementation may instead compare the raw expected identity inside the
isolated world and return only `match: true|false`, provided the expected
identity itself lives only in the private browser-session boundary. It must
never pass raw identity through logging, tracing, JSON output, fixtures, or
issue text.

Cloud `Service-ID`, contract number, `USER-ID`, route parameters, and a visible
login are not substitutes for this binding. A service ID selects a Cloud
environment beneath the already authenticated principal.

## Browser-context execution contract

The probe should execute as follows:

1. Attach through the loopback-only CDP endpoint to the dedicated browser
   process for exactly one local profile.
2. Navigate or reuse a first-party `www.reg.ru` or `cloud.reg.ru` frame.
   Refuse arbitrary origins.
3. Create an isolated execution world for the probe.
4. If `csrftoken` is absent, run the current credentialed
   `GET /authenticate` bootstrap. Read the cookie only inside the world.
5. Run one credentialed `POST /refresh` with the current CSRF header.
6. Enforce a small response-size limit, JSON content, exact envelope and
   allowlisted status shapes. Reduce the response before returning from the
   isolated world.
7. Compare the opaque principal binding before any BFF, GraphQL, WebSocket, or
   Cloud environment call.
8. Run capability probes in the same browser context and origin choreography.

The CDP layer must disable request/response-body logging for these targets.
DevTools network events, crash reports, traces, screenshots, and test fixtures
must not retain cookie headers, CSRF values, GraphQL variables/results,
principal values, service IDs, or opaque locators.

Cookie export is the wrong boundary even if a raw Go request happens to work:

- the provider uses several origins and service-specific CSRF schemes;
- authenticated cookie attributes and rotation behavior are unpublished;
- the first-party clients deliberately use browser credentials and an origin
  allowlist;
- `localStorage`, focus/route refresh behavior, and GraphQL middleware
  participate in the current session choreography;
- REG.RU lets accounts bind sessions to an IP address, so portability to
  another process/network path is not guaranteed.
  ([advanced security][advanced-security])

Do not call CDP cookie-dump APIs as a convenience, serialize a cookie jar, or
replay the session from another host. Browser-context execution is not merely
an anti-bot fallback; it is the credential containment model.

## Refresh and expiry behavior

No first-party published source found here specifies idle timeout, absolute
session lifetime, refresh grace, rotation, clock skew, or the effects of
password/security changes. REG.RU's cookie policy distinguishes generic
session and persistent cookies but does not provide a portal session TTL.
([cookie policy][cookie-policy])

Therefore:

- `auth status` must be explicit that an authoritative check performs a
  refresh and may extend the provider session. A cached local status is only a
  hint.
- `auth refresh` forces the in-context refresh regardless of the local
  staleness timestamp.
- Before private operations, use a per-profile single-flight refresh gate.
  The current 150-second frontend threshold is a compatibility default, not a
  promise; after process restart or uncertain state, probe immediately.
- A successful refresh updates the broker's monotonic last-verified time only
  after identity comparison succeeds.
- `no_user_session`, HTTP `401`/`403` auth rejection, a recognized GraphQL
  `Unauthorized`, or a logout event transitions to `reauth-required`.
- A transport timeout, DNS/TLS failure, `429`, or `5xx` is `unavailable`, not
  proof of expiry. Preserve the binding and do not clear the browser profile.
- Do not blindly retry an ambiguously delivered refresh. The provider
  publishes no refresh idempotency/rotation contract. Let the next explicit
  probe retry after a bounded delay.
- An unknown HTTP-success status, missing expected principal field, malformed
  JSON, changed content type, or unexpected GraphQL union is
  `private-contract-drift`. Do not fall back to DOM scraping, cookie export,
  another account, or a guessed request.

The useful local distinction is:

```text
never successfully bound + no_user_session
  => reauth-required/not-established

previously matched binding + later no_user_session
  => reauth-required/session-lost

successful refresh + different digest
  => account-mismatch
```

`session-lost` is intentionally broader and more honest than `expired`.

## Fail-closed response matrix

| Observation | Broker result | Capability calls |
| --- | --- | --- |
| Recognized `session_refreshed`, required identity present, digest matches | `authenticated` | May proceed to separately probed read adapters |
| Recognized `session_refreshed`, digest differs | `account-mismatch` | Block all calls; require explicit account/profile repair |
| Recognized `no_user_session` before first binding | `reauth-required/not-established` | Block; keep/wake headed login UI |
| Recognized `no_user_session` after a prior match | `reauth-required/session-lost` | Block; do not claim precise expiry cause |
| HTTP 401/403 or recognized GraphQL `Unauthorized` | `reauth-required` | Block and return actionable auth error |
| Cloud `EnvironmentNotFound` after matched core auth | capability `not-configured` or `unavailable` pending capture | Do not turn it into global auth failure |
| Network, browser, or provider 5xx failure | `unavailable` | Block; retain binding and session profile |
| Unknown status, envelope, content type, identity shape, operation, or `__typename` | `incompatible/private-contract-drift` | Fail closed; no fallback request |

Manifest and asset hashes are drift alarms, not semantic promises. The current
REG.Cloud panel manifest identifies `cloudVpsPanel`, reports build `2.69.3`,
uses content-hashed assets, and is served with `Cache-Control: no-cache` and an
ETag. Fetch it without a stale cache before private adapter use; a changed
build triggers structural re-probing. The decisive gate remains the exact
allowlisted auth/operation/result shape, because an unrelated frontend release
need not invalidate the broker. ([Cloud panel manifest][cloud-manifest])

## What remains unproven

No live authenticated session was available to this research. In particular,
the following remain uncertain:

- the exact successful `/refresh` response keys in every account state and
  whether both identity fields are always non-empty;
- whether refresh rotates any authenticated cookie or can require a renewed
  SMS/CAPTCHA challenge;
- whether `no_user_session` covers every idle/absolute expiry and revocation
  mode;
- whether HTTP 401/403 is ever used instead of an HTTP-200 domain status by
  `login.reg.ru`;
- whether `EnvironmentNotFound` always means no Cloud environment rather than
  a session/environment race;
- whether a session can be safely refreshed after sleep, network change, or
  browser restart under every account security configuration.

Until an authorized redacted capture answers them, the implementation must
accept only the shapes already recognized by the current first-party clients,
return `reauth-required` for a recognized missing session, and return
`private-contract-drift` for everything else. It must never expose or persist
the private body merely to make diagnosis easier.

## Recommended redacted fixtures

Tests can cover the contract without real secret material:

- synthetic `session_refreshed` envelopes with non-provider dummy identities,
  reduced to matching and mismatching keyed digests;
- the value-free `no_user_session` envelope;
- HTTP 401/403, malformed JSON, oversized body, HTML challenge, timeout, and
  unknown status cases;
- Cloud `Environments`, `Unauthorized`, `EnvironmentNotFound`, and unknown
  `__typename` fixtures with dummy service IDs reduced before crossing CDP;
- assertions that CDP logging, JSON output, errors, and fixtures contain no
  cookie, CSRF, raw principal, contract, or service-ID values.

An eventual authorized integration capture should record only origin,
operation name, HTTP/domain status, response-key presence, `__typename`, and
whether required identity fields are present. Values must be reduced to
same/different/empty in memory and discarded.

## Primary sources

- [REG.RU rules: cookies may be used for automatic authorization][site-rules]
- [REG.RU advanced security: login IP restrictions and session-to-IP
  binding][advanced-security]
- [REG.RU personal-cabinet guide][cabinet-guide]
- [REG.RU help acknowledging multiple personal cabinets][multiple-cabinets]
- [Current REG.RU account shell][account-shell]
- [Current account auth client][account-auth]
- [Current account application][account-index]
- [Current account GraphQL client][account-gql]
- [REG.RU auth bootstrap endpoint][authenticate]
- [REG.RU refresh endpoint][refresh]
- [Current REG.Cloud panel shell][cloud-shell]
- [Current REG.Cloud panel manifest][cloud-manifest]
- [Current REG.Cloud auth client][cloud-auth]
- [Current REG.Cloud GraphQL client][cloud-graphql]
- [Current REG.Cloud environment operations][cloud-operations]
- [Current REG.Cloud GraphQL endpoint][cloud-graphql-endpoint]

Local redacted evidence:

- [REG.RU portal session lifecycle and account switching](portal-session-lifecycle.md)
- [Authenticated REG.RU portal capability observation](authenticated-portal-capability.md)
- [Redacted REG.RU account capability matrix](account-capability-matrix-redacted.md)
- [Private REG.RU portal resilience contract](private-portal-resilience-contract.md)

[site-rules]: https://www.reg.ru/company/rules
[cookie-policy]: https://help.reg.ru/support/dokymenty/pravila-i-politiki/politika-v-othoshenii-obrabotki-fajlov-cookie/politika-v-othoshenii-obrabotki-fajlov-cookie11082025
[advanced-security]: https://help.reg.ru/support/lichnyy-kabinet/bezopasnost-akkaunta/rasshirennyye-nastroyki-bezopasnosti
[cabinet-guide]: https://help.reg.ru/support/lichnyy-kabinet/registratsiya-i-kontaktnyye-dannyye/znakomstvo-s-lichnym-kabinetom-reg-ru
[multiple-cabinets]: https://help.reg.ru/support/domains/problema-s-domenom/pochemu-moy-domen-nedostupen-v-lichnom-kabinete
[account-shell]: https://www.reg.ru/user/account/
[account-auth]: https://www.reg.ru/user/account/1508.40f765beebd5cfa3df2d.js
[account-index]: https://www.reg.ru/user/account/index.82f5f8db7d99ba5ed418.js
[account-gql]: https://www.reg.ru/user/account/4229.37275d176a2e742ac00c.js
[authenticate]: https://login.reg.ru/authenticate
[refresh]: https://login.reg.ru/refresh
[cloud-shell]: https://cloud.reg.ru/panel
[cloud-manifest]: https://cloudvps-static.svc.reg.ru/panel/mf-manifest.json
[cloud-auth]: https://cloudvps-static.svc.reg.ru/panel/107.7ba232fd9b902061aea1.js
[cloud-graphql]: https://cloudvps-static.svc.reg.ru/panel/__federation_expose_panel.230377f66688d4eeb56c.js
[cloud-operations]: https://cloudvps-static.svc.reg.ru/panel/2720.12363fb271d587598e64.js
[cloud-graphql-endpoint]: https://cloudvps-graphql-server.svc.reg.ru/api
[redacted-matrix]: account-capability-matrix-redacted.md
[authenticated-observation]: authenticated-portal-capability.md
