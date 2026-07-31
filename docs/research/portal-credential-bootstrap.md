# Bootstrapping REG.RU credentials from one portal session

Research date: 2026-07-30

## Scope and evidence standard

This note asks whether `regru auth login` can accept one interactive REG.RU
login and then discover the credentials needed for CloudVPS, S3 management,
REG.API, and private portal APIs without making the user copy several secrets
by hand.

The research used only current first-party documentation and public JavaScript
served by `cloud.reg.ru`. No account was authenticated, no credential value was
read, and no key, token, API setting, or resource was created or changed.
Operation names found in the panel bundle are implementation evidence, not a
published or versioned API contract.

## Executive answer

**One interactive provider login can support a zero-copy setup for CloudVPS and
S3.** It cannot silently turn into every possible REG.RU credential without
additional authorization decisions.

| Capability | What one authenticated portal session can bootstrap | Remaining boundary |
| --- | --- | --- |
| CloudVPS | The panel's read-only `environmentInfo` GraphQL query returns both `serviceId` and the already-created CloudVPS bearer `token`. Multiple environments can first be enumerated by service ID. | Importing/persisting the bearer token is a sensitive disclosure. Rotating it is a separate mutation and needs explicit confirmation. |
| S3 management | Existing key sets can be listed and read through the session; the current query result includes `accessKey` and `secretKey`. REG.RU's public guide independently says both values can be reopened later in a key set's parameters. | If no suitable key set exists, creating one is a persistent-access mutation. Resetting one rotates credentials and may break existing clients. Both need explicit confirmation. |
| Portal BFF/GraphQL | The same browser session authenticates the cloud panel's private GraphQL and related internal services with cookies. | Cookies and internal operations are undocumented, session-lived interfaces. They need a dedicated version-probed adapter and compatibility checks. |
| REG.API 2 | The session can reveal the account login, but the published REG.API authentication contract still requires `username` plus a main/alternate password, or a request signature. Browser cookies are not a documented REG.API credential. | The CLI must either receive and persist an API password, or create/configure an alternate password or certificate flow with explicit approval. Source-IP allowlisting is also required. |

The desired product promise should therefore be:

> **One interactive login and no manual secret copy-paste.**

It should not promise “one login and no further confirmations.” Creating a new
S3 key set, rotating a CloudVPS token, setting an alternate REG.API password,
or widening the REG.API IP allowlist all create, replace, or expand persistent
access.

## The shared browser session

The current REG.Cloud frontend uses REG.RU's first-party login service. Its
public client code derives `login.reg.ru` as the authentication host, obtains a
CSRF cookie with `GET /authenticate`, and performs
`POST /authenticate` with `login` and `password`. The same request model
supports optional SMS session/confirmation fields and CAPTCHA response fields.
The client refreshes the session with `POST /refresh`; requests use cookies and
the CSRF header. This is direct evidence that an interactive browser flow can
handle ordinary login as well as provider-driven SMS or CAPTCHA challenges
without the CLI scraping the user's password.
([first-party authentication client bundle][panel-auth-bundle])

The cloud panel sends GraphQL over
`https://cloudvps-graphql-server.svc.reg.ru/api` and subscriptions over the
corresponding `wss` URL. Its Apollo client uses `credentials: "include"` and
adds a `Service-ID` header when an environment is selected.
([panel endpoint utility bundle][panel-utils-bundle],
[panel GraphQL client][panel-graphql-client])

The GraphQL `environments` query returns the available `serviceId` values.
Consequently, a single browser session can enumerate Cloud environments and
then run discovery against each selected service rather than asking the user
for one token per environment.
([current cloud panel operation bundle][panel-core-bundle])

These endpoints are private panel implementation. A production CLI should not
treat their hostnames, operation schemas, or cookie behavior as provider
guarantees merely because they are visible in public frontend code.

## CloudVPS token bootstrap

REG.RU's public support guide says the CloudVPS API key is created
automatically and is available in the cloud environment's **Settings** tab.
The same guide describes rotation through the panel's **Update** action.
([CloudVPS API key guide][cloudvps-token-guide])

The current panel implementation is even more useful for bootstrap:
`query environmentInfo` selects `serviceId` and `token` as normal fields of
`EnvironmentInfo`. This means an authenticated session can read the existing
bearer token; the user does not need to locate and paste it manually.
([environment query bundle][panel-environment-bundle])

The current rotation implementation is not the same operation as reading the
token. It obtains a CSRF token and posts an action shaped as:

```text
service_id: <selected CloudVPS service>
type: renew_token
headers:
  X-CSRF-TOKEN: <session CSRF token>
  USER-ID: <account contract number>
```

The response supplies the replacement token. This is a private SRS action
rather than a documented CloudVPS token-management API.
([current cloud panel operation bundle][panel-core-bundle])

Product boundary:

- Reading environment IDs and determining whether a token exists is read-only.
- Revealing the bearer token to `regru` and persisting it in a credential store
  should require an explicit “import this existing credential” confirmation.
- `renew_token` must require a stronger rotation confirmation explaining that
  existing automation may stop working.
- The CLI should prefer the existing token. It must not rotate merely to make
  setup easier.

## S3 key bootstrap

REG.RU's public S3 guide tells an authenticated user to open
**My resources → S3 storage → Access keys**, choose an existing key set's
parameters, and copy `S3 API Endpoint`, `Access key ID`, and
`Secret access key`. This is direct first-party evidence that the current
product supports later retrieval of an existing secret, not only a one-time
display during key creation.
([S3 access guide][s3-access-guide])

The current public panel code confirms the backing operation shapes:

- `query objectStoreKeyPairs(page, pageSize)` returns key-pair `id`, `name`,
  `instanceId`, `accessKey`, `secretKey`, and `createdAt`.
- `query objectStoreKeyPair(keyPairId)` returns the same fields for one pair.
- `mutation createObjectStoreKeyPair(name)` returns a newly created pair,
  including both secret fields.
- `mutation resetObjectStoreKeyPair(keyPairId)` returns the rotated pair.
- `mutation deleteObjectStoreKeyPair(keyPairId)` deletes a pair.
- Initial `mutation createObjectStore` returns the new object store together
  with an initial `keypair`.

All are carried by the cookie-authenticated Cloud GraphQL client.
([S3 GraphQL operation bundle][panel-s3-bundle])

This gives `regru auth login` a clean zero-copy path:

1. List key-set metadata.
2. If an existing set is selected, read it and import it into the secure local
   credential store after one explicit disclosure/persistence confirmation.
3. If no suitable set exists, offer **Create key set for regru** as a separate
   confirmed action.
4. Never reset an existing pair as part of ordinary setup.

The public docs do not describe per-key scopes or bucket binding. Setup copy
must not claim that a generated key is read-only, bucket-scoped, or
least-privilege unless a later authorized characterization proves it.

## REG.API is the exception

The published REG.API contract requires either:

- `username` plus `password`, where `password` is the main REG.RU password or
  an alternate API password configured in **Partner settings** or
  **API settings**; or
- `username` plus a per-request RSA/SHA-512 signature, backed by a certificate
  registered in the account.

It also says that if any API SSL certificate is registered, every
authenticated request must present a certificate.
([REG.API authentication parameters][regapi-auth])

REG.API access is additionally restricted to source addresses configured in
**API settings**. The provider's current guide instructs the user to add IP
ranges in the portal before requests can succeed.
([REG.API IP restrictions][regapi-ip])

No first-party source found in this research documents exchanging a portal
cookie for a REG.API password, signature credential, or bearer token. Therefore
an interactive login performed wholly inside the provider's browser page does
not give the CLI a documented REG.API credential.

The CLI should not solve this by intercepting the password typed into the
provider page. That would turn a browser-login convenience into account-password
capture and would bypass the useful isolation provided by the browser.
Instead:

- Use the browser session for private portal BFF/GraphQL features such as
  billing and support.
- Use REG.API only when a dedicated API credential is already configured, or
  offer an explicit setup step for an alternate API password/signature.
- Treat adding or changing the API password, adding an IP range, and
  registering a certificate as separately confirmed account mutations.

If the product later chooses to reuse the main password, that must be a
deliberate direct credential-entry mode, not an invisible consequence of
`regru auth login`.

## Recommended `regru auth login` flow

1. Launch a dedicated browser profile at the REG.Cloud/REG.RU login page.
   The user enters credentials directly into the provider page and completes
   any SMS, second-factor, or CAPTCHA challenge.
2. Detect successful authentication without reading keystrokes. Keep the
   provider cookie jar inside that managed browser profile.
3. Enumerate Cloud environments and perform read-only capability discovery:
   environment IDs, presence of a CloudVPS token, S3 object-store state, and
   key-set metadata.
4. Show a concise import plan. For example:

   ```text
   Cloud environment 123: existing CloudVPS token can be imported
   S3: existing key set "regru" can be imported
   REG.API: IP allowlist or API credential still requires setup
   ```

5. Ask once before disclosing and persisting each existing high-value secret.
   Store it only in the chosen OS credential store or 1Password reference;
   never in command arguments, logs, issue text, or ordinary config.
6. Offer missing persistent credentials as separate actions:
   **Create S3 key set**, **Rotate CloudVPS token**, **Set REG.API password**,
   or **Add REG.API source IP**. Display the exact consequence and confirm
   immediately before each mutation.
7. Keep portal automation session-based. A browser-resident request bridge
   controlled through the browser debugging protocol is preferable to
   exporting REG.RU cookies into a general-purpose Go HTTP cookie jar.

Agent Browser is useful for development-time characterization, but it should
not become a required installed runtime component. The shipped CLI can use a
managed Chromium profile and the Chrome DevTools Protocol, or another small
browser-control layer, while keeping the provider page visible for interactive
challenges.

## Confirmation matrix

| Action | Provider mutation? | Required product treatment |
| --- | --- | --- |
| Enumerate Cloud environment IDs | No | Run after login. |
| List S3 key-set names/metadata | No | Run after login; do not print secrets. |
| Read/import an existing CloudVPS token | No | Confirm secret disclosure and destination persistence. |
| Read/import an existing S3 access/secret pair | No | Confirm secret disclosure and destination persistence. |
| Create an S3 key set | Yes | Confirm creation of new persistent access. |
| Reset an S3 key set | Yes | Strong rotation confirmation; warn about breaking clients. |
| Rotate the CloudVPS token | Yes | Strong rotation confirmation; warn about breaking clients. |
| Set/change an alternate REG.API password | Yes | Confirm creation/replacement of persistent access. |
| Add a REG.API source IP/range | Yes | Confirm expansion of permitted network sources. |
| Register the first REG.API certificate | Yes | Confirm that it changes the requirements for all authenticated API requests. |

## What still requires an authorized network capture

The public frontend establishes operation and field shapes, but it cannot
prove every runtime property. A read-only authenticated capture is still
needed to establish:

- the actual cookie names, domain/path attributes, expiry, refresh behavior,
  and whether requests can be replayed outside the originating browser;
- the environment/account-selection sequence and exact `Service-ID` behavior
  when one login owns multiple Cloud environments;
- whether every account receives the unmasked `token`, `accessKey`, and
  `secretKey` fields or whether account role/state can redact them;
- the concrete GraphQL error variants when S3 is not activated, no key set
  exists, an environment is locked, or the user lacks permission;
- the mutation CSRF requirements for S3 key creation/reset and the point at
  which activating an object store becomes billable;
- whether an imported S3 pair can successfully perform the bucket-management
  calls needed by `regru`, including create/delete and provider quota controls;
- the internal endpoints for REG.API settings, whether an alternate API
  password has any read-back path, and how IP/certificate changes are
  confirmed;
- whether billing and support BFF/GraphQL sessions are accepted directly from
  the same managed browser profile in every account configuration.

The capture should never log response bodies containing token, access-key,
secret-key, cookie, ticket, billing, or personal values. Record only operation
names, request origins, redacted header presence, status/type names, and
whether a sensitive field is present or masked.

## Implementation-status conclusion

The credential UX no longer needs three manual copy/paste prompts. The
evidence supports one browser authorization followed by automatic discovery
and optional secure import of CloudVPS and S3 credentials.

The internal panel contract is strong enough to prototype this flow, but not
stable enough to promise as an unguarded public API. Implement it behind an
explicit portal-adapter capability, retain the browser-session fallback, and
surface provider schema/auth changes as a compatibility error rather than
silently creating or rotating credentials.

## Primary sources

- [REG.Cloud panel shell][panel-shell]
- [REG.Cloud authentication client bundle][panel-auth-bundle]
- [REG.Cloud GraphQL endpoint utility bundle][panel-utils-bundle]
- [REG.Cloud GraphQL client bundle][panel-graphql-client]
- [REG.Cloud environment GraphQL bundle][panel-environment-bundle]
- [REG.Cloud S3 GraphQL bundle][panel-s3-bundle]
- [REG.Cloud core operation bundle][panel-core-bundle]
- [CloudVPS API key guide][cloudvps-token-guide]
- [REG.Cloud S3 access guide][s3-access-guide]
- [REG.Cloud S3 key-set creation guide][s3-key-create-guide]
- [REG.API 2 authentication parameters][regapi-auth]
- [REG.API IP restrictions][regapi-ip]

[panel-shell]: https://cloud.reg.ru/panel
[panel-auth-bundle]: https://cloudvps-static.svc.reg.ru/panel/107.7ba232fd9b902061aea1.js
[panel-utils-bundle]: https://cloudvps-static.svc.reg.ru/panel/6186.21e8041e88ab8276f0d9.js
[panel-graphql-client]: https://cloudvps-static.svc.reg.ru/panel/__federation_expose_panel.230377f66688d4eeb56c.js
[panel-environment-bundle]: https://cloudvps-static.svc.reg.ru/panel/3539.e74c128bd819e4bb7701.js
[panel-s3-bundle]: https://cloudvps-static.svc.reg.ru/panel/3619.65a559365d90541d575b.js
[panel-core-bundle]: https://cloudvps-static.svc.reg.ru/panel/2720.12363fb271d587598e64.js
[cloudvps-token-guide]: https://reg.cloud/support/cloud/oblachnyye-servery/rabota-s-serverom/api-dlya-oblachnykh-serverov
[s3-access-guide]: https://reg.cloud/support/instrukcii/obektnoe-hranilishe-s3/sposoby-dostupa-k-faylam-v-s3
[s3-key-create-guide]: https://reg.cloud/support/instrukcii/obektnoe-hranilishe-s3/kak-nastroit-backup-dannyh-s-servera-s-ispmanager-v-hranilishche-s3
[regapi-auth]: https://www.reg.ru/reseller/api2doc#common_auth_params
[regapi-ip]: https://www.reg.ru/support/partneram/reg-api/kakiye-ogranicheniya-yest-pri-rabote-s-reg-api
