# REG.RU portal session lifecycle and account switching

Research date: 2026-07-30

## Scope and evidence standard

This note characterizes what `regru` can rely on after one interactive browser
login across `www.reg.ru`, `login.reg.ru`, and `cloud.reg.ru`. It uses only
current first-party documentation, public first-party frontend code, and
unauthenticated HTTP protocol observations. No authenticated browser session,
credential, account identifier, cookie value, or private response was read; no
provider state was changed.

REG.RU's published rules and help are treated as the supported contract.
Hashed JavaScript bundles and observed headers describe the implementation on
the research date, not a versioned API promise.

## Decision for `regru`

**Model a portal login as one browser-resident session for exactly one REG.RU
account principal. Give every additional REG.RU account its own isolated,
persistent browser context/profile. Model Cloud environments as children of
that principal, selected independently by opaque service ID.**

Concretely:

1. Keep the provider cookie jar and all origin storage in a dedicated managed
   browser context. Do not export cookies to a general-purpose HTTP client,
   serialize them into CLI config, or share one context between account
   profiles.
2. Treat the session as valid only while an in-context refresh succeeds and
   the returned principal still matches the profile's identity fingerprint.
   A failed refresh, `401`, GraphQL `Unauthorized`, logout event, or identity
   mismatch moves the profile to `reauth-required`.
3. Never implement REG.RU account switching by changing `USER-ID`, contract
   number, login, or a URL parameter. Public material documents multiple
   personal cabinets and logout, but no supported in-session account selector.
   Switching accounts means switching isolated browser contexts, or explicitly
   logging out and authenticating the target account in the same context.
4. Within one principal, enumerate Cloud environments and require an explicit
   environment choice when more than one is returned. Scope caches, pending
   operations, and credentials beneath `(portal-profile, service-id)`.
5. Use the provider's page and current private adapters only as a guarded
   browser capability. Do not present the observed login, GraphQL, BFF, CSRF,
   or selector shapes as a stable public API.

This is the only model that fits both the published account boundary and the
current frontend: authentication is coordinated across REG.RU origins, while
identity hints and refresh timestamps are cached separately in each origin's
`localStorage`.

## What is published versus observed

| Question | Published contract | Current first-party implementation evidence | `regru` consequence |
| --- | --- | --- | --- |
| What authenticates a personal cabinet? | The personal cabinet is accessed with its login and password; REG.RU may use cookies for automatic authorization on its sites. ([site rules][site-rules]) | Both account and cloud frontends derive `https://login.reg.ru` as the production auth host and call it with credentials included. ([account app][account-index], [cloud auth client][cloud-auth]) | One provider-hosted interactive login may establish the browser session. Do not capture the password. |
| Is there one origin? | The cookie policy covers `www.reg.ru` and its subdomains, but does not publish an origin-by-origin auth protocol. ([cookie policy][cookie-policy]) | The browser loads UI and APIs from several origins: `www.reg.ru`, `login.reg.ru`, `cloud.reg.ru`, `gql-acc.svc.reg.ru`, and `cloudvps-graphql-server.svc.reg.ru`. ([account GraphQL client][account-gql], [cloud endpoint utility][cloud-utils]) | Preserve a whole browser context, not a cookie copied from one origin. |
| How long does a login last? | REG.RU distinguishes session cookies from persistent cookies, but says persistent-cookie lifetime differs by cookie and publishes no portal idle timeout, absolute lifetime, or refresh-token lifetime. ([cookie policy][cookie-policy]) | Both current auth helpers use a 150-second client-side refresh staleness threshold. This is a polling/cache decision, not the server session lifetime. ([account auth client][account-auth], [cloud auth client][cloud-auth]) | Do not promise a duration. Probe validity by refresh and require reauthentication when the provider rejects it. |
| Can one user have multiple accounts? | REG.RU explicitly says one user can have multiple personal cabinets and instructs users to check each account; the cabinet guide exposes a logout action. ([missing domain guide][multiple-accounts], [cabinet guide][cabinet-guide]) | The account frontend tracks one current `screen_name`/login and one current user/contract. No supported account-selector call is exposed. ([account auth client][account-auth], [account app][account-index]) | Separate browser profile per account. No implicit or header-based account switching. |
| Can one account have multiple Cloud environments? | No public session/selector contract was found. | `environments` returns environment `serviceId` values; the Cloud Apollo link adds the selected value as `Service-ID`. ([cloud operations][cloud-core], [cloud GraphQL client][cloud-graphql]) | Treat service ID as an opaque environment selector under the authenticated principal, not as an account credential. |
| What security settings can alter portability? | An account can restrict login to configured IPs and bind a session to an IP address. ([advanced security][security]) | The frontend does not provide a portable-session guarantee. | Keep the session on the same browser/network path; never assume replay from another host or IP will work. |

## Origin and session boundary

### `login.reg.ru` is the authentication coordinator

The current account SPA resolves its production auth base URL to
`https://login.reg.ru`. The Cloud client independently derives the same host
from `cloud.reg.ru`. Both send credentialed requests. The auth surface visible
in both bundles includes:

- `GET /authenticate` to obtain CSRF state;
- `POST /authenticate` for login;
- `POST /refresh`;
- `POST /logout`;
- `POST /logout_other_sessions`;
- optional SMS-session, confirmation-code, and CAPTCHA fields during login.

These are implementation observations, not a published non-browser API.
([account auth client][account-auth], [account app host resolver][account-index],
[cloud auth client][cloud-auth])

An unauthenticated `GET https://login.reg.ru/authenticate` on the research date
returned a `csrftoken` cookie with `Domain=reg.ru`, `Path=/`, and
`Max-Age=31449600`; the value was discarded. This proves the current CSRF
cookie is deliberately available to REG.RU subdomains. It does **not** reveal
the authenticated session-cookie names, attributes, or lifetime, and the
roughly one-year CSRF-cookie lifetime must not be mistaken for login lifetime.
([authentication endpoint][auth-endpoint], [cookie domain-matching
standard][rfc-cookie-domain])

Credentialed CORS preflights to `POST /refresh` from both
`https://www.reg.ru` and `https://cloud.reg.ru` returned the matching
`Access-Control-Allow-Origin`, `Access-Control-Allow-Credentials: true`, and
permission for `x-csrf-token`/`x-csrftoken`. An unrelated origin received no
allow-origin header. This establishes a current origin allowlist, not a
commitment to those exact origins or headers. ([refresh endpoint][refresh])

### Account and Cloud APIs remain separate origins

The account SPA sends GraphQL HTTP requests to
`https://gql-acc.svc.reg.ru/` and WebSocket subscriptions to the corresponding
host, with browser credentials included. Its middleware refreshes the
`login.reg.ru` session before GraphQL operations. ([account GraphQL
client][account-gql])

The Cloud panel shell at `https://cloud.reg.ru/panel` loads its application
from `cloudvps-static.svc.reg.ru`. In production, the application resolves
GraphQL to `https://cloudvps-graphql-server.svc.reg.ru/api` and subscriptions
to the corresponding `wss` origin, again with browser credentials included.
([cloud shell][cloud-shell], [cloud endpoint utility][cloud-utils], [cloud
GraphQL client][cloud-graphql])

Therefore “one login” means that one browser context can participate in the
provider's cross-origin session choreography. It does not mean that a raw
cookie from any one origin is a complete or supported bearer credential.

## CSRF requirements are service-specific

There is no single portal CSRF token:

- Auth requests use cookie `csrftoken` and header `x-csrf-token`. If the cookie
  is absent, the clients first issue credentialed `GET /authenticate`.
  ([account auth client][account-auth], [cloud auth client][cloud-auth])
- Account GraphQL uses a distinct cookie `acc-csrftoken`, header
  `x-acc-csrftoken`, and issuer
  `GET https://gql-acc.svc.reg.ru/account/issue_csrf_token`; operations also
  include browser credentials. ([account GraphQL client][account-gql])
- The current Cloud GraphQL link includes browser credentials and, when an
  environment is selected, `Service-ID`; it does not add either account-CSRF
  header. Separately, some Cloud actions against REG.RU's SRS endpoint fetch
  `/user/regenerate_csrf_token` and send `X-CSRF-TOKEN` plus `USER-ID`.
  ([cloud GraphQL client][cloud-graphql], [cloud operations][cloud-core])

`USER-ID` in those SRS actions is populated from the current user's contract
number. It is additional request context, not evidence that callers may select
another account by header. `regru` must not generalize any one of these private
CSRF or identity shapes to another backend.

## Refresh, expiry, logout, and principal-change behavior

### Current refresh behavior

The Cloud auth helper currently defaults to:

- refresh after its stored timestamp is 150 seconds old;
- optional checks every 30 seconds;
- refresh whenever a hidden document becomes visible;
- a mutex to avoid overlapping refreshes;
- `localStorage` keys for last refresh and current `screen_name`.

The Cloud router and GraphQL middleware call refresh before admitting protected
routes or operations; a Cloud GraphQL `Unauthorized` result triggers logout
handling and routes back to auth. ([cloud auth client][cloud-auth], [cloud
GraphQL client][cloud-graphql])

The newer account helper also uses a 150-second staleness threshold and a
cross-tab mutex in `localStorage`. The account Apollo middleware invokes
`refreshIfNeeded()` before operations, and the application invokes it after
route changes. A `401` opens the auth form. ([account auth client][account-auth],
[account GraphQL client][account-gql], [account app][account-index])

Successful implementation responses use statuses including `authenticated`,
`session_refreshed`, and `ok`; login may instead report SMS- or
CAPTCHA-related states. Those strings are useful for a compatibility probe,
but they are not a published enum. ([account auth client][account-auth],
[cloud auth client][cloud-auth])

### What remains unknown

No first-party published source found in this research specifies:

- authenticated cookie names, domain/path attributes, `SameSite`, `Secure`, or
  `HttpOnly` behavior;
- idle timeout, absolute session lifetime, refresh grace period, refresh-token
  rotation, or clock-skew rules;
- whether closing the browser ends an authenticated session in every account
  configuration;
- the exact effect of password reset, security-setting changes, or
  `logout_other_sessions` on Cloud and account sessions;
- whether session refresh may require a renewed SMS/CAPTCHA challenge;
- whether an authenticated cookie jar is supported outside a normal browser.

The published cookie policy only defines generic session and persistent cookie
classes, while REG.RU's rules reserve cookies for automatic authorization.
([cookie policy][cookie-policy], [site rules][site-rules])

Consequently, `regru` should not run a timer that predicts expiry. Refresh
in-context before private work, classify provider rejection as
`reauth-required`, and retain no retry loop that can hammer `/refresh`.

### Identity-change propagation is origin-local

Both frontend auth helpers cache the current `screen_name` in
`localStorage` and publish login/logout or screen-name events to other tabs.
The account header reloads user data when the screen name changes; Cloud
redirects to auth when it becomes empty. ([account auth client][account-auth],
[account header][account-header], [cloud auth client][cloud-auth])

Browser `localStorage` is origin-scoped. Thus a login change noticed by
`www.reg.ru` is not itself a synchronized identity change in
`cloud.reg.ru` storage; each origin learns the authoritative state through its
next `login.reg.ru` refresh. This is why account isolation must be at browser
context/profile level rather than at tab, origin, or selected-service level.
([Web Storage standard][web-storage])

## Identity and selector model

The frontend exposes three distinct concepts that must not be collapsed:

1. **Portal principal.** Auth responses expose `user_id` and `screen_name`, and
   frontend state uses `screen_name` as the login/logout change signal. These
   values identify the current authenticated principal but are private account
   data; `regru` should not log or place them in paths. ([account auth
   client][account-auth], [cloud auth client][cloud-auth])
2. **Current REG.RU account/contract.** The account and Cloud user queries
   expose the current login and `contractNumber`. The account GraphQL client
   has no general account-selector header; it resolves the account from the
   session. `USER-ID` appears only on particular SRS calls and must not be used
   as a switching mechanism. ([account app][account-index], [cloud environment
   query][cloud-environment], [cloud operations][cloud-core])
3. **Cloud environment.** The Cloud `environments` query returns zero or more
   opaque `serviceId` values. The panel changes its Apollo link to send the
   selected ID in `Service-ID`. This chooses a Cloud environment inside the
   current principal; it does not authenticate a different REG.RU account.
   ([cloud operations][cloud-core], [cloud GraphQL client][cloud-graphql])

REG.RU's help states that one person can hold several personal cabinets, while
the documented cabinet menu contains logout but no account switcher.
([multiple-account guidance][multiple-accounts], [cabinet guide][cabinet-guide])
The supported conclusion is therefore logout/re-auth or context switching for
accounts, versus `Service-ID` selection for Cloud environments.

## Safe multi-account isolation model

Each configured portal profile should own:

```text
PortalProfile
  opaque local profile id
  dedicated browser context/profile directory
  identity fingerprint (keyed hash; never the raw login/contract)
  session state: unknown | active | reauth-required
  CloudEnvironment[]
    opaque service id
    environment-scoped cache and imported credentials
```

Required invariants:

- Never attach two REG.RU account identities to one browser context.
- Never copy cookies or Web Storage between contexts.
- Never derive a profile filename from login, email, contract number, or
  service ID.
- Before any private operation, refresh in the same context and compare an
  in-memory/keyed identity fingerprint. On mismatch, cancel the operation,
  clear only `regru`'s derived caches, and require the user to choose or repair
  the profile.
- Partition Apollo/BFF caches and pending requests by profile. Partition Cloud
  resource caches again by service ID; selecting an environment must replace
  or reset the client link/cache exactly as the current panel does.
- Treat logout as affecting the whole profile. Do not keep Cloud work running
  because another origin has stale local state.
- Keep multiple profiles serialized or in separate browser processes if the
  runtime cannot prove cookie-jar isolation.
- Respect IP allowlisting and session-to-IP binding. Do not migrate a session
  between machines or proxy paths. ([advanced security][security])

## Recommended session state machine

```text
unknown
  -> interactive-login
  -> active             refresh succeeds and identity matches

active
  -> active             refresh succeeds; same principal
  -> reauth-required    refresh rejected, 401, Unauthorized, logout,
                        challenge required, or principal mismatch

reauth-required
  -> interactive-login  user explicitly resumes this profile
  -> active             login succeeds; identity matches or user confirms
                        replacing this profile's account binding
```

Do not silently replace a profile's identity after interactive login. A user
may have intentionally opened another REG.RU account in the provider UI;
binding that account to existing Cloud caches or imported credentials risks
cross-account actions.

## Implementation boundary

The evidence is strong enough to design a safe browser-session adapter and
multi-account profile model. It is **not** strong enough to ship a direct HTTP
client for the private auth/BFF/GraphQL endpoints or to promise a fixed session
lifetime.

The first implementation should:

- launch one managed browser context per portal profile;
- detect success and refresh through the provider page without reading login
  fields;
- retain all provider session state in that context;
- expose only `active` versus `reauth-required`, not a predicted expiry time;
- enumerate Cloud environments read-only and select them explicitly;
- compatibility-gate private operation names, status enums, origins, CSRF
  names, and selector headers;
- hand control back to the provider UI for fresh login, SMS, CAPTCHA, or
  account switching.

## Primary sources

- [REG.RU site rules][site-rules]
- [REG.RU cookie policy][cookie-policy]
- [REG.RU advanced security settings][security]
- [REG.RU personal-cabinet guide][cabinet-guide]
- [REG.RU help acknowledging multiple personal cabinets][multiple-accounts]
- [Current REG.RU account shell][account-shell]
- [Current account auth client bundle][account-auth]
- [Current account GraphQL client bundle][account-gql]
- [Current account application bundle][account-index]
- [Current account header bundle][account-header]
- [Current REG.Cloud panel shell][cloud-shell]
- [Current REG.Cloud auth client bundle][cloud-auth]
- [Current REG.Cloud endpoint utility bundle][cloud-utils]
- [Current REG.Cloud GraphQL client bundle][cloud-graphql]
- [Current REG.Cloud environment query bundle][cloud-environment]
- [Current REG.Cloud operations bundle][cloud-core]
- [REG.RU authentication endpoint][auth-endpoint]
- [REG.RU refresh endpoint][refresh]
- [RFC 6265 domain matching][rfc-cookie-domain]
- [WHATWG Web Storage standard][web-storage]

[site-rules]: https://www.reg.ru/company/rules
[cookie-policy]: https://img.reg.ru/faq/politika_obrabotki_fajlov_cookie_010425.pdf
[security]: https://help.reg.ru/support/lichnyy-kabinet/bezopasnost-akkaunta/rasshirennyye-nastroyki-bezopasnosti
[cabinet-guide]: https://help.reg.ru/support/lichnyy-kabinet/registratsiya-i-kontaktnyye-dannyye/znakomstvo-s-lichnym-kabinetom-reg-ru
[multiple-accounts]: https://help.reg.ru/support/domains/problema-s-domenom/pochemu-moy-domen-nedostupen-v-lichnom-kabinete
[account-shell]: https://www.reg.ru/user/account/
[account-auth]: https://www.reg.ru/user/account/1508.40f765beebd5cfa3df2d.js
[account-gql]: https://www.reg.ru/user/account/4229.37275d176a2e742ac00c.js
[account-index]: https://www.reg.ru/user/account/index.82f5f8db7d99ba5ed418.js
[account-header]: https://www.reg.ru/user/account/968.da26a07c6ed64f4b4989.js
[cloud-shell]: https://cloud.reg.ru/panel
[cloud-auth]: https://cloudvps-static.svc.reg.ru/panel/107.7ba232fd9b902061aea1.js
[cloud-utils]: https://cloudvps-static.svc.reg.ru/panel/6186.21e8041e88ab8276f0d9.js
[cloud-graphql]: https://cloudvps-static.svc.reg.ru/panel/__federation_expose_panel.230377f66688d4eeb56c.js
[cloud-environment]: https://cloudvps-static.svc.reg.ru/panel/3539.e74c128bd819e4bb7701.js
[cloud-core]: https://cloudvps-static.svc.reg.ru/panel/2720.12363fb271d587598e64.js
[auth-endpoint]: https://login.reg.ru/authenticate
[refresh]: https://login.reg.ru/refresh
[rfc-cookie-domain]: https://www.rfc-editor.org/rfc/rfc6265.html#section-5.1.3
[web-storage]: https://html.spec.whatwg.org/multipage/webstorage.html#the-localstorage-attribute
