# Interactive browser login: TTY and safety contract

Research date: 2026-07-31

## Scope and evidence standard

This note defines an implementable CLI contract for an interactive
`regru auth login` implemented in Go. It covers TTY and automation behavior,
`--no-input`, `--force`, `--dry-run`, cancellation, deadlines, browser
availability, session expiry, account mismatch, private-interface drift, and
secret-safe output.

The sources are primary specifications and documentation, official Go package
documentation, first-party CLI contracts, and current first-party REG.RU
frontend code. Values such as the proposed ten-minute login deadline are local
product choices, not provider guarantees.

REG.RU's portal endpoints and frontend bundles are private implementation
evidence, not a versioned API promise. No authenticated session or provider
mutation was used for this note.

## Decision

`regru auth login` is an explicitly interactive command which authenticates in
a visible, CLI-owned browser profile. It never captures a password, OTP,
CAPTCHA answer, cookie, or provider token through CLI arguments, stdin, logs,
or normal output.

The command must obey these invariants:

1. A real login starts only when stdin is a TTY and interactive input has not
   been disabled. Non-TTY and `--no-input` runs may verify an already
   committed session read-only, but if fresh authentication is required they
   fail before opening a browser, binding a listener, creating staging state,
   or changing profile state.
2. `--force` means “perform a fresh login even if the profile currently looks
   active.” It never means “assume yes,” accept another account, ignore
   compatibility drift, disable timeouts, or reveal/store a secret.
3. `--dry-run` performs local, read-only preflight and prints the intended
   steps. It does not launch a browser, contact authenticated endpoints, start
   a callback/listener, access cookie contents, or modify profile state.
4. Login is transactional. All new browser/session state is staged separately
   and becomes current only after an in-context refresh, identity check, and
   auth-contract compatibility check succeed. Cancellation, timeout, browser
   failure, account mismatch, and auth-contract drift leave the last committed
   profile untouched.
5. One root `context.Context` owns the whole attempt. Ctrl-C, user
   cancellation, browser exit, and the total deadline cancel every child
   request, wait loop, listener, and CLI-owned browser process.
6. Every wait and cleanup path is bounded. There is no “timeout 0 means
   forever” mode.
7. Provider secrets and raw account identity never enter `os.Args`, structured
   logs, error strings, stdout, stderr, telemetry, crash attachments, or shell
   commands.

CLI guidance recommends prompting only when stdin is a TTY, making
`--no-input` disable every interactive action, keeping Ctrl-C functional, and
giving network work a configurable finite timeout. It also gives the
conventional meanings used here for `--force` and `--dry-run`.
([CLI Guidelines][cli-guidelines]) GitHub CLI provides a first-party precedent
for an environment-level switch that disables terminal prompting, while
Terraform separates “no interactive input” from “approve without a prompt”;
with input disabled, Terraform conservatively refuses an operation that still
needs approval. ([GitHub CLI environment][gh-environment],
[Terraform apply][terraform-apply])

## Command-line contract

Recommended surface:

```text
regru auth login [--profile NAME] [--force] [--dry-run]
                 [--no-input] [--timeout DURATION] [--json]
```

`NAME` is a local display alias, not a provider login or contract number.

### Flag semantics

| Invocation | Required behavior |
| --- | --- |
| default, active profile | Verify the committed session read-only. If it is active and identity-compatible, return success without opening a browser; otherwise begin interactive login |
| `--force` | Skip the active-session short circuit and stage a fresh interactive login; preserve the committed profile until the new login commits |
| `--no-input` | Disable prompts, browser launch, browser interaction, CAPTCHA/SMS handoff, and any other human wait; an already active profile may verify successfully, but a required fresh login returns `interactive_required` before login side effects |
| `--dry-run` | Report local profile state, browser resolution result, whether `--force` would replace the session, configured deadlines, and planned compatibility checks; make no network or state changes |
| `--dry-run --no-input` | Valid; the useful CI-safe preflight |
| `--dry-run --force` | Valid; report that a fresh staged login would replace the current session, but do not do it |
| `--force --no-input` without `--dry-run` | Return `interactive_required`; `--force` does not imply interaction or consent |
| `--timeout=0`, negative, below one minute, or above thirty minutes | Usage error; an unbounded or implausibly short/long login is not supported |

The default active-session check is not a hidden login: it may refresh only
the already committed browser session and must never open a login UI. If the
check encounters expiry, a provider challenge, or an identity change, it
continues to interactive login only when stdin is a TTY and interaction is
enabled. Otherwise it returns `reauth_required`.

Do not add `--username`, `--password`, `--otp`, `--captcha`,
`--cookie`, `--session`, `--token`, or equivalent secret-bearing flags. A
process invoked with sensitive arguments or environment values may expose
them to other local processes; this is the weakness catalogued as CWE-214.
([CWE-214][cwe-214])

### TTY and streams

Use `term.IsTerminal(int(os.Stdin.Fd()))` for the interaction gate. The Go
`x/term` package explicitly exposes `IsTerminal`; it also provides
no-echo password input, but browser login should not need `ReadPassword`
because credentials belong only in the provider page.
([Go x/term][go-term])

Only stdin decides whether interaction is available:

- stdin TTY: prompts and a browser handoff are allowed unless `--no-input`;
- stdin not a TTY: no prompt and no browser launch;
- stdout not a TTY: still allowed when stdin is a TTY; write the stable result
  to stdout without decoration;
- stderr not a TTY: suppress spinners, cursor control, color, and animations,
  but keep concise progress/error lines.

Human progress and diagnostics go to stderr. The final result goes to stdout.
`--json` emits exactly one JSON value on stdout; no progress or browser
messages may contaminate it. This follows the CLI convention of reserving
stdout for primary/machine-readable output and stderr for messages.
([CLI Guidelines][cli-guidelines])

Before any side effect, evaluate in this order:

1. parse and validate flags;
2. if `--dry-run`, run the local read-only plan and return;
3. inspect the committed profile without reading or printing cookie values;
4. if an active compatible session makes login unnecessary and `--force` is
   absent, return success;
5. enforce `!noInput && stdinTTY`;
6. acquire the per-profile attempt lock;
7. resolve the browser and complete anonymous/auth-adapter preflight;
8. only then create staging state and launch the browser.

This ordering prevents CI, pipes, and invalid invocations from unexpectedly
opening GUI windows or touching session state.

## Transactional login state machine

```text
committed profile (optional)
  -> local-preflight
  -> auth-contract-preflight
  -> staging-profile
  -> browser-starting
  -> waiting-for-user
  -> verifying-session
  -> verifying-identity
  -> atomic-commit
  -> authenticated

any pre-commit state
  -> canceled | timed-out | browser-unavailable | login-failed
  -> account-mismatch | provider-incompatible
  -> bounded cleanup of staging state
  -> committed profile remains unchanged
```

Create the staging browser directory with user-only permissions. Never reuse
the committed directory for a forced login: browser writes are not
transactional and a failed login could otherwise log out or corrupt the
working session. Hold a per-profile lock for the attempt so two login commands
cannot race to commit different principals.

At commit, close the staging browser cleanly, flush its profile, validate that
the staging and destination directories are on the intended local filesystem,
and atomically replace a small local pointer/metadata record. Do not try to
atomically rename a live browser directory. If the pointer commit fails,
preserve both the old committed state and the quarantined staging state, but
never select the latter automatically on the next run.

On cancellation or failure, best-effort delete the uncommitted staging
profile within the cleanup deadline because it may contain partial cookies. If
cleanup fails, quarantine it under an opaque random name with user-only
permissions, record only that opaque identifier, and remove it before the next
login attempt. Never print its cookie database or provider identity.

## Browser launch and missing-browser behavior

The login page must be visible and credentials must be typed into the
provider-controlled page. RFC 8252 is an OAuth best-current-practice rather
than a specification of REG.RU's private login, but its security rationale is
directly applicable: an external browser keeps the client from recording
keystrokes or copying credentials, whereas an embedded web view can access
credentials and cookies. ([RFC 8252][rfc-8252])

For REG.RU, “external browser” does not mean “silently fall back to any
existing system browser.” The current portal session model needs one isolated,
persistent browser context per account. Launch a supported browser executable
directly with a dedicated user-data directory and a remote-control channel;
do not use a shell, a shared everyday browser profile, or an embedded login
form.

Implementation rules:

- Resolve an explicit configured browser path first, then a small allowlist of
  supported browser binaries for the platform.
- Accept only an absolute configured executable path. For name lookup, use
  `exec.LookPath`; reject `exec.ErrDot` and never execute a candidate resolved
  relative to the current directory.
- Launch with `exec.CommandContext` or equivalent context ownership, without
  `sh -c`, `cmd /c`, or interpolation. Replace `Cmd.Cancel` with a
  CLI-owned graceful-close function and set a finite `Cmd.WaitDelay`; the
  `CommandContext` default is to kill the process immediately. Go's
  `os/exec` deliberately avoids a shell, documents this cancellation hook,
  exposes `ErrNotFound`, and rejects implicit current-directory resolution
  because it is unsafe. ([Go os/exec][go-exec],
  [Go PATH security][go-path-security])
- Pass only non-secret switches: the staging profile path, a random local
  debugging port or pipe, and the public provider origin. Never put cookies,
  CSRF values, callback state, login names, or session material in browser
  arguments.
- Treat “executable missing,” “not executable,” unsupported version, startup
  timeout, early process exit, and inability to establish the control channel
  as `browser_unavailable`.

If no supported browser is available, fail before creating staging state and
show installation/configuration remediation. Do not print a “copy this login
URL” fallback: a login in an unmanaged browser would not populate the
CLI-owned isolated profile and could bind the wrong account.

The CLI owns and may close/kill only the process it launched for the staging
profile. It must never terminate the user's normal browser or an unrelated
process discovered by name.

## Cancellation and Ctrl-C

Construct the command root with
`signal.NotifyContext(parent, os.Interrupt)` and propagate that context as the
first argument through browser launch, HTTP/CDP operations, wait loops,
verification, and cleanup orchestration. Go documents that
`NotifyContext` cancels the context on the signal and that its `stop` function
restores normal signal behavior and releases resources.
([Go os/signal][go-signal])

Required behavior:

1. On the first Ctrl-C, immediately write `Login canceled; cleaning up…` to
   stderr, cancel the root context, stop accepting callback/control events,
   and begin bounded cleanup.
2. Stop signal interception after the first interrupt so a second Ctrl-C
   takes the platform's normal immediate-exit path. Never leave the process
   trapping signals indefinitely.
3. Ask the managed browser to close gracefully; after the cleanup grace
   period, kill only that CLI-owned process.
4. Close loopback listeners before waiting for server shutdown. If an HTTP
   listener is used, call `Server.Shutdown` with a short, independent cleanup
   context; Go notes that `Shutdown` otherwise waits for connections until its
   context expires. ([Go net/http][go-http])
5. Never perform a provider logout on cancellation. The in-progress browser
   may not have a known principal, and the old committed session must remain
   usable.
6. Exit 130 for SIGINT. Bash and other Unix tooling conventionally report a
   fatal signal as `128 + signal number`; SIGINT is signal 2.
   ([Bash exit status][bash-exit])

Closing the staging browser window, choosing a provider-side cancel action, or
the login page reporting cancellation uses `login_canceled` and ordinary exit
1, not 130. Both paths preserve committed state and clean staging state.

Do not catch a panic and dump request bodies, headers, browser events, or
secret-bearing objects to stderr. A generic internal-error identifier is safer
than an automatic auth-flow stack attachment.

## Bounded deadlines

Use one total attempt deadline:

```text
default: 10 minutes
allowed --timeout range: 1 minute to 30 minutes
```

Ten minutes leaves room for password-manager use, SMS, MFA, and a provider
challenge while still bounding an abandoned browser. It is a local UX budget,
not a statement about REG.RU's session lifetime.

Derive all work from the smaller of the root deadline and these stage caps:

| Stage | Proposed cap | Timeout result |
| --- | ---: | --- |
| browser resolution and startup/control connection | 15 seconds | `browser_unavailable` |
| each anonymous manifest/schema or in-context HTTP request | 20 seconds | `provider_unreachable` before login; `outcome_unknown` only if a provider mutation was actually sent |
| post-login refresh and identity verification together | 30 seconds | `verification_timeout`; do not commit |
| graceful cleanup | 3 seconds | force-close the CLI-owned process, quarantine staging if deletion is incomplete |

Use `context.WithTimeout` for every blocking stage and always call its cancel
function. Go's `context` contract propagates cancellation to derived contexts
and distinguishes `Canceled` from `DeadlineExceeded`.
([Go context][go-context])

The login wait loop must select on `ctx.Done()`; it must not poll with an
uncancelable sleep. Outgoing HTTP requests use `NewRequestWithContext`.
Browser subprocesses use `CommandContext`. Any callback server has finite
header/read/write timeouts in addition to the root context.

The total deadline returns exit 124 and error code `timeout`, following the
well-established GNU `timeout` status. ([GNU timeout][gnu-timeout]) Stage
timeouts that make the environment unusable may instead return ordinary exit
1 with the more specific code above, but they never extend the total
deadline. `--force` cannot change any deadline.

## Session expiry and reauthentication

The current first-party account and Cloud authentication helpers refresh the
`login.reg.ru` session and track the current `screen_name`; the Cloud client
logs out/routes to auth when GraphQL returns `Unauthorized`. The observed
150-second refresh-staleness threshold is a frontend cache decision, not a
published portal session lifetime. ([Current account auth client][account-auth],
[current Cloud auth client][cloud-auth],
[current Cloud GraphQL client][cloud-graphql-client])

Therefore:

- never predict expiry from cookie dates or the frontend's 150-second value;
- before each private operation, refresh inside the selected profile and
  verify the principal fingerprint;
- classify a failed refresh, HTTP `401`, GraphQL `Unauthorized`, logout,
  renewed CAPTCHA/SMS challenge, or missing principal as `reauth_required`;
- retain the browser profile and derived metadata, but mark it
  `reauth-required`; do not delete a potentially recoverable session;
- do not silently open a browser from an arbitrary noninteractive command;
  return a stable remediation such as `run regru auth login --profile NAME`;
- do not automatically retry authentication failures or challenges.

An explicit `regru auth login` may then stage a fresh login. It commits only
after refresh and identity verification succeed.

## Account mismatch

REG.RU documents that one person can have multiple personal cabinets. Current
account and Cloud frontend code nevertheless expose one current
`screen_name`/account in a browser context, and Cloud environments are selected
separately with `Service-ID`. ([REG.RU multiple-account guidance][multiple-accounts],
[current account auth client][account-auth],
[current Cloud GraphQL client][cloud-graphql-client])

Bind each local profile to a keyed fingerprint of the authenticated portal
principal. The comparison value should include the most stable available
current-account identifiers, be HMACed with a local credential-store key, and
never use a raw unsalted login/contract hash that could be guessed offline.

After a fresh login:

- an unbound new profile may bind to the verified fingerprint;
- an existing profile with the same fingerprint may commit;
- an existing profile with a different fingerprint returns
  `account_mismatch`, closes/quarantines staging, and preserves the committed
  session, caches, environment selection, and imported credentials;
- output may say “the browser authenticated a different REG.RU account,” but
  must not print either raw login, email, contract number, or service ID;
- `--force` does not override this check.

Replacing an account binding needs a distinct, explicit future operation
(for example, `regru auth replace-account`) that can describe which local
caches and imported credentials will be detached. Do not overload
`--force`, because users commonly understand it as “retry/overwrite” rather
than “cross an account security boundary.”

## Private-contract drift

The current REG.Cloud panel publishes a no-cache module-federation manifest
with a build identity and content-hashed assets. The current GraphQL client
uses browser credentials and a `Service-ID` header, and private operations
return named union members such as `Unauthorized`. These are useful
compatibility signals, not semantic-version guarantees.
([Current panel manifest][panel-manifest],
[current Cloud GraphQL client][cloud-graphql-client])

Split compatibility into two scopes:

1. **Auth adapter.** Before committing a new session, require the known login
   success/refresh/identity semantics. A missing or changed auth request/result
   shape, unknown login state, missing principal, or unrecognized challenge
   fails `provider_incompatible` and preserves the committed profile.
2. **Capability adapters.** Probe each private Cloud/support operation
   independently by current manifest plus exact operation argument/result
   shapes. A drifted capability is disabled fail-closed. Unrelated capability
   drift may be reported after a valid session commits; it must not cause the
   CLI to discard a successfully verified login.

A build version or asset hash change is an alarm that triggers an exact shape
probe, not by itself proof of incompatibility. Conversely, an unchanged
version cannot authorize an unknown result. Unknown `__typename`, auth state,
CSRF behavior, identity field, or environment selector must never be guessed.

`--force` cannot bypass either compatibility scope. `--dry-run` may resolve
the local browser and report which probes would run, but under this contract
it performs no network access and therefore reports compatibility as
`not_checked`, never `compatible`.

## Secret-safe arguments, logs, and output

### Forbidden data paths

The following values must never be accepted as flags or positional arguments,
included in browser command arguments, or written to normal output:

- password, OTP, CAPTCHA response, recovery code;
- cookie names plus values, cookie database contents, CSRF values;
- bearer/API tokens, CloudVPS token, S3 access/secret keys;
- raw CDP request/response headers or storage dumps;
- callback nonce/state or login URL query string;
- raw login, email, contract number, `screen_name`, or `Service-ID` by default.

OWASP logging guidance says session identifiers, access tokens, passwords,
encryption keys, and other primary secrets should be removed, masked,
sanitized, hashed, or encrypted rather than recorded. Kubernetes' first-party
secret guidance likewise requires applications to avoid logging secret data
in cleartext after reading it. ([OWASP Logging Cheat Sheet][owasp-logging],
[Kubernetes secret practices][kubernetes-secrets])

### Go implementation rules

- Introduce distinct secret types (`Secret`, `CookieValue`, `Token`,
  `PrincipalID`) rather than passing bare strings across the adapter.
- Implement `slog.LogValuer` for every sensitive type so it returns a fixed
  redaction token. Go's `slog` documentation explicitly supports this use.
  ([Go slog][go-slog])
- Add a logger handler denylist for field names such as `authorization`,
  `cookie`, `set-cookie`, `csrf`, `token`, `password`, `secret`, `login`,
  `contract`, and `service_id`. Type-based redaction is primary; the denylist
  is defense in depth.
- Never log a parsed login URL with `URL.String()`. Go's `URL.Redacted`
  redacts only a password in URL userinfo, not sensitive query parameters;
  construct a safe origin/path-only value and discard `RawQuery` and
  `Fragment`. ([Go net/url][go-url])
- Do not include raw HTTP response bodies/headers, GraphQL variables, browser
  console logs, CDP events, `exec.Cmd.Args`, or `exec.ExitError.Stderr` in
  wrapped user errors.
- Verbose/debug mode changes event detail, never the redaction policy.
- Output opaque local profile IDs and boolean/state labels only. For example:

  ```json
  {
    "status": "authenticated",
    "profile": "p_7J4M2K",
    "account": "verified",
    "capabilities": {
      "cloud": "compatible",
      "support": "not_checked"
    }
  }
  ```

- Authentication audit events may record time, opaque profile ID, outcome,
  stage, duration bucket, and stable error code. They may not record the
  principal, endpoint query, credential, provider body, or browser storage.
- Disable automatic telemetry payload capture and crash-upload attachments
  for auth flows unless the payload passes the same structural redaction and
  is inspectable before transmission.

## Stable results and exit statuses

Keep exit statuses small and put detailed classification in the human error
and `--json` error code:

| Exit | Meaning | Example codes |
| ---: | --- | --- |
| 0 | requested state reached or dry-run completed | `authenticated`, `already_authenticated`, `dry_run` |
| 1 | operational or policy failure | `interactive_required`, `browser_unavailable`, `login_canceled`, `login_failed`, `reauth_required`, `account_mismatch`, `provider_incompatible`, `state_commit_failed` |
| 2 | invalid syntax/flags | `invalid_timeout`, `unknown_flag`, missing flag value |
| 124 | total login deadline exceeded | `timeout` |
| 130 | command interrupted by SIGINT | `canceled` |

Do not return success merely because the browser opened or the page appeared
authenticated. Success requires a fresh in-context refresh, recognized auth
result, matching/bound identity, and committed local state.

If login commits but an unrelated private capability is incompatible, return
exit 0 with an explicit capability warning/state. The authentication
destination was reached; later attempts to use that capability must fail
closed with `provider_incompatible`.

## Minimum verification matrix

The implementation should have hermetic tests for at least these cases:

| Case | Assertion |
| --- | --- |
| stdin pipe, no flags | no browser/network/write; `interactive_required` |
| stdin TTY, stdout pipe | browser allowed; stable stdout, progress only on stderr |
| `--no-input` with TTY | no browser/network/write; `interactive_required` |
| `--dry-run --no-input` in CI | local plan succeeds; no network/listener/browser/write |
| active session, default | read-only verification; no browser |
| active session, `--force` | fresh staging browser; old session usable until commit |
| `--force --no-input` | `interactive_required`; force does not bypass |
| browser missing / `ErrDot` / early exit | no staging commit; `browser_unavailable` |
| first Ctrl-C at every state-machine edge | immediate message, all contexts canceled, old state preserved, exit 130 |
| second Ctrl-C during cleanup | normal immediate exit; next run safely detects staging residue |
| browser window closed | `login_canceled`, bounded cleanup, exit 1 |
| total deadline | bounded return, exit 124, no commit |
| per-request deadline | no goroutine/listener/process leak; total deadline unchanged |
| refresh `401` / GraphQL `Unauthorized` / challenge | `reauth_required`; no automatic retry |
| same-account forced login | atomic commit to the new staged session |
| different-account forced login | `account_mismatch`; committed profile/caches unchanged |
| auth result or identity shape drift | `provider_incompatible`; no commit |
| unrelated capability drift | login commits; capability is disabled and reported |
| verbose/error/panic paths | captured argv/stdout/stderr/log/JSON contain none of the seeded secrets or raw identity values |
| URL with secrets in userinfo and query | safe formatter emits origin/path only |
| concurrent logins for one profile | exactly one holds the lock; no lost update |

Add leak tests that wait for browser processes, listener closure, goroutine
completion, and staging residue after success, cancellation, and timeout.
Fuzz the error/redaction formatter with seeded values in headers, URLs,
GraphQL bodies, nested errors, and `slog` attributes.

## Primary sources

- [Command Line Interface Guidelines][cli-guidelines]
- [GitHub CLI environment variables][gh-environment]
- [Terraform apply command][terraform-apply]
- [RFC 8252: OAuth 2.0 for Native Apps][rfc-8252]
- [Go `context` package][go-context]
- [Go `os/signal` package][go-signal]
- [Go `os/exec` package][go-exec]
- [Go `net/http` package][go-http]
- [Go `net/url` package][go-url]
- [Go `log/slog` package][go-slog]
- [Go `x/term` package][go-term]
- [Go command PATH security][go-path-security]
- [GNU Bash exit-status convention][bash-exit]
- [GNU `timeout` exit-status convention][gnu-timeout]
- [MITRE CWE-214][cwe-214]
- [OWASP Logging Cheat Sheet][owasp-logging]
- [Kubernetes good practices for Secrets][kubernetes-secrets]
- [REG.RU guidance acknowledging multiple personal cabinets][multiple-accounts]
- [Current REG.RU account auth client][account-auth]
- [Current REG.Cloud auth client][cloud-auth]
- [Current REG.Cloud panel manifest][panel-manifest]
- [Current REG.Cloud GraphQL client][cloud-graphql-client]

[cli-guidelines]: https://clig.dev/
[gh-environment]: https://cli.github.com/manual/gh_help_environment
[terraform-apply]: https://developer.hashicorp.com/terraform/cli/commands/apply
[rfc-8252]: https://www.rfc-editor.org/rfc/rfc8252.html
[go-context]: https://pkg.go.dev/context
[go-signal]: https://pkg.go.dev/os/signal#NotifyContext
[go-exec]: https://pkg.go.dev/os/exec
[go-http]: https://pkg.go.dev/net/http#Server.Shutdown
[go-url]: https://pkg.go.dev/net/url#URL.Redacted
[go-slog]: https://pkg.go.dev/log/slog#LogValuer
[go-term]: https://pkg.go.dev/golang.org/x/term#IsTerminal
[go-path-security]: https://go.dev/blog/path-security
[bash-exit]: https://www.gnu.org/software/bash/manual/html_node/Exit-Status.html
[gnu-timeout]: https://www.gnu.org/software/coreutils/timeout
[cwe-214]: https://cwe.mitre.org/data/definitions/214.html
[owasp-logging]: https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html
[kubernetes-secrets]: https://kubernetes.io/docs/concepts/security/secrets-good-practices/
[multiple-accounts]: https://help.reg.ru/support/domains/problema-s-domenom/pochemu-moy-domen-nedostupen-v-lichnom-kabinete
[account-auth]: https://www.reg.ru/user/account/1508.40f765beebd5cfa3df2d.js
[cloud-auth]: https://cloudvps-static.svc.reg.ru/panel/107.7ba232fd9b902061aea1.js
[panel-manifest]: https://cloudvps-static.svc.reg.ru/panel/mf-manifest.json
[cloud-graphql-client]: https://cloudvps-static.svc.reg.ru/panel/__federation_expose_panel.230377f66688d4eeb56c.js
