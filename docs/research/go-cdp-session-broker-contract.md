# Go/CDP runtime contract for browser-authenticated portal sessions

Research date: 2026-07-31

## Question and evidence boundary

This note recommends the smallest Go runtime that can safely own a headed
Chrome/Chromium process for
`Prototype: implement browser-authenticated portal sessions`. It covers
process launch, an ephemeral loopback CDP endpoint, target attachment,
JavaScript evaluation, browser-context network calls, and bounded shutdown.

The evidence is limited to Chrome/Chromium and CDP documentation and source,
Go documentation, and the source/module metadata of the compared Go
libraries. Repository-specific requirements come from
[`auth-tty-safety-contract.md`](auth-tty-safety-contract.md),
[`portal-session-lifecycle.md`](portal-session-lifecycle.md), and
[`cobra-foundation-architecture.md`](cobra-foundation-architecture.md).

No browser was authenticated and no provider data or session material was
read. Chrome's user-data directory contains cookies and other profile state,
and the runtime must treat the whole directory as secret. Chromium documents
that scope explicitly. ([Chromium user-data directory][chrome-user-data])

Product decision after this research: the dedicated Chrome profile is the
accepted local session boundary. It requires private OS ownership and
permissions, but no application-managed encryption, encrypted container, or
full-disk-encryption prerequisite. The Go baseline may move from 1.25 to 1.26.

## Decision

**Use `chromedp` as the CDP transport and generated protocol binding, behind a
small repository-owned process/session owner. Use `chromedp` v0.16.0 and its
pinned `cdproto`, raising the repository's accepted Go baseline from 1.25 to
1.26.**

That is the best fit because it already provides:

- a Go-native WebSocket CDP multiplexer;
- flattened target attachment and target-scoped command routing;
- generated `Runtime`, `Network`, `Target`, and `Browser` types;
- context-aware commands and a documented graceful `Browser.close` path; and
- access to the exact Chrome process it launched for a final force-kill.

`chromedp` v0.16.0 is the current tag and pins a July 2026 `cdproto`; its module
declares the accepted Go 1.26 baseline. Version v0.14.2 remains only a
documented comparison point, not the implementation fallback. Do not silently
select a version at build time. ([v0.16.0 module][chromedp-v016-mod],
[v0.14.2 module][chromedp-v014-mod])

Do **not** use `chromedp.DefaultExecAllocatorOptions`. They make an automation
test browser, not a normal human login browser: the current defaults include
headless mode, disabled extensions, disabled Safe Browsing updates,
`password-store=basic`, `use-mock-keychain`, and many browser-behavior changes.
Construct a short explicit option list instead. ([chromedp allocator
source][chromedp-allocate])

The repository-owned layer is necessary even though `chromedp` can launch a
browser itself. It must keep Chrome alive while individual calls time out,
sanitize startup errors, serialize shutdown, enforce the profile lock, and
turn command cancellation into graceful-close-then-kill rather than allowing
the default `exec.CommandContext` kill to run first.

## Why the endpoint is acceptably narrow

Chrome 136 and later ignore `--remote-debugging-port` and
`--remote-debugging-pipe` for the default Chrome profile. They require a
non-default `--user-data-dir`; Chrome recommends that isolation because the
debuggable profile has a separate encryption key. This matches the existing
one-profile-per-account contract. ([Chrome remote-debugging security
change][chrome-136])

Launch with:

```text
<absolute browser executable>
  --user-data-dir=<opaque locked profile directory>
  --remote-debugging-port=0
  --no-first-run
  --no-default-browser-check
  about:blank
```

No raw “extra browser flags” escape hatch belongs in the CLI or broker API.
In particular, callers cannot set a fixed port, a remote-debugging address,
proxy, certificate bypass, sandbox bypass, extension, or default profile.

Chrome's current desktop source parses port zero as an ephemeral port and
binds its remote-debugging socket first to `127.0.0.1`, falling back to `::1`;
it does not bind a wildcard address. After the kernel chooses the port, Chrome
writes the port and browser WebSocket path to `DevToolsActivePort` in the
selected user-data directory. The CDP documentation independently specifies
the same port-zero bootstrap. ([Chromium remote-debugging
server][chromium-loopback], [CDP endpoint bootstrap][cdp-bootstrap])

This avoids the usual reserve-a-port/close/reopen race. It is also why
`chromedp.Flag("remote-debugging-port", 0)` must **not** be used: the allocator
accepts only string or bool flag values and treats other types as invalid.
When the flag is absent, the allocator appends
`--remote-debugging-port=0` itself. ([chromedp allocator
source][chromedp-allocate])

Loopback is not authentication. Any process running as the same local user may
be able to discover the port and control the open pages; Chromium's own
traffic annotation says the endpoint exposes debugging data, including data
from open pages. Therefore:

- create the profile parent and staging directories with user-only
  permissions and hold the per-profile lock before launch;
- never print, log, persist in normal config, or return the WebSocket URL,
  port, browser GUID, `DevToolsActivePort` contents, or PID;
- do not configure `chromedp.CombinedOutput`; translate startup failures to
  stable structured errors because raw Chrome stderr can include local paths;
- accept the endpoint only from the CLI-owned process/profile and only for its
  lifetime; and
- close the endpoint by closing that browser process before releasing the
  profile lock.

([Chromium DevTools HTTP handler][chromium-devtools-handler])

## Process-owner contract

### Resolution and launch

Resolve the browser before staging state is created:

1. Accept only an explicitly configured absolute path, or search a small
   platform allowlist with `exec.LookPath`.
2. Reject `exec.ErrDot`, non-regular files, and non-executable files.
3. Execute the path directly with `exec.CommandContext`; never invoke a shell.
4. Pass no login, account, cookie, CSRF, token, callback state, or provider
   request in arguments or environment.

`CommandContext` normally sets `Cmd.Cancel` to `Process.Kill` and leaves
`WaitDelay` unset. A repository-owned `ModifyCmdFunc` should preserve
`chromedp`'s platform process-parent behavior, set a finite `WaitDelay`, and
retain hard kill as the last fallback. The graceful first step is the CDP
`Browser.close` command, which Chrome documents as “Close browser gracefully.”
([Go `os/exec`][go-exec], [CDP Browser.close][cdp-browser-close],
[chromedp platform launch][chromedp-platform-launch])

The implementation must explicitly set `Flag("no-sandbox", false)`. Current
`chromedp` otherwise adds `--no-sandbox` automatically when the CLI is run as
root. A headed credential-bearing login with Chrome's sandbox disabled is not
an acceptable fallback; fail with `missing_browser`/`browser_unavailable`
instead. ([chromedp allocator source][chromedp-allocate])

Recommended allocator options:

```go
[]chromedp.ExecAllocatorOption{
    chromedp.ExecPath(browserPath),
    chromedp.UserDataDir(profileDir),
    chromedp.NoFirstRun,
    chromedp.NoDefaultBrowserCheck,
    chromedp.Flag("no-sandbox", false),
    chromedp.WSURLReadTimeout(startupTimeout),
    chromedp.ModifyCmdFunc(configureOwnedChromeCommand),
}
```

No option is copied from `DefaultExecAllocatorOptions`, and no `Headless`,
`IgnoreCertErrors`, `DisableGPU`, `ProxyServer`, or `CombinedOutput` option is
added.

### Context ownership

Do not make Chrome's allocator context a direct child of the Cobra command
context. If that context is canceled, `exec.CommandContext` may kill Chrome
before `Browser.close` can flush the profile.

Use three layers:

```text
command context
  watches user/SIGINT/total deadline
  -> asks Session.Close to begin

browser-owner context
  outlives command cancellation during bounded cleanup
  -> owns allocator, browser process, browser WebSocket, first page target

per-call context
  inherits the chromedp values from browser-owner context
  + mirrors the caller's cancellation/deadline
  -> owns only one navigate/evaluate/probe operation
```

The first `chromedp.Run` both starts Chrome and owns its lifetime, so it must
use the long-lived browser-owner context. `chromedp` itself warns that placing
a short timeout on the first `Run` stops the entire browser. Enforce the
15-second startup cap with a timer that cancels the owner only if startup has
not completed; stop that timer after the first target has attached.
([chromedp Run documentation][chromedp-run])

Later calls use derived per-call contexts. Canceling a request then stops that
CDP command without killing the browser or invalidating the persistent
session.

### Shutdown

`Session.Close` is idempotent and serialized:

1. mark the session closing and reject new calls;
2. cancel network listeners and per-call work;
3. derive a short cleanup context from
   `context.WithoutCancel(browserContext)` so it retains the chromedp value
   but not the canceled command signal;
4. call `chromedp.Cancel(cleanupContext)`, which sends `Browser.close` for the
   browser-owning context and waits for Chrome to exit;
5. when the grace deadline expires, kill only the PID returned by
   `chromedp.FromContext(browserContext).Browser.Process()`;
6. wait only for the remaining bounded process/pipe deadline, then quarantine
   staging state if a clean exit cannot be proved; and
7. release the profile lock last.

The current `chromedp.Cancel` implementation sends `Browser.close`, then
cancels the owning context and waits for the allocator. It can wait for the
allocation channel after its passed context expires, so invoke it in the
owner's cleanup goroutine and retain the explicit PID kill/deadline outside
that call; do not assume the library timeout alone proves bounded shutdown.
([chromedp cancellation source][chromedp-cancel])

Unexpected browser exit, lost WebSocket, or target crash closes `Done`,
cancels in-flight calls, and maps to a sanitized browser/session error. The
runtime never searches by process name and never signals a browser it did not
launch.

## Target, evaluation, and network contract

### Target attachment

The broker owns exactly one initial page target for login and probes.
`chromedp.NewContext` plus the first `Run` discovers a page target, attaches
with `Target.attachToTarget(flatten=true)`, creates the target executor, and
enables `Runtime` and `Network` among other page domains. This is protocol
behavior the library already gets right; do not reimplement session-ID
multiplexing. ([chromedp target attachment][chromedp-target-attach], [CDP
Target.attachToTarget][cdp-target])

Do not expose arbitrary target IDs. A session may create a controlled child
page later, but it never attaches to a pre-existing personal Chrome process or
target. Popup/new-target handling must remain inside the broker and retain the
same profile ownership.

At startup, run a no-secret compatibility probe:

1. `Browser.getVersion`;
2. attach to the owned page target;
3. `Runtime.evaluate("1")` by value; and
4. `Network.enable`.

Probe the actual methods instead of trusting only a protocol version string.
The CDP tip-of-tree contract explicitly offers no backward-compatibility
guarantee. Provider manifest/schema probes remain separate and are still
required before a private operation. ([CDP compatibility warning][cdp-tot],
[CDP Runtime][cdp-runtime], [CDP Network][cdp-network])

### JavaScript evaluation

Raw arbitrary JavaScript must not cross the CLI/use-case boundary. The CDP
package may expose an internal fixed-program executor to typed portal
adapters:

```go
type ProgramID string // closed internal allowlist, not user input

type PageExecutor interface {
    RunJSON(
        ctx context.Context,
        program ProgramID,
        args json.RawMessage,
        result *json.RawMessage,
    ) error
}
```

Each `ProgramID` maps to source embedded in the binary and code-reviewed with
its adapter. Arguments are JSON-marshaled separately; never construct source
with string concatenation. Results are returned by value with a per-program
maximum size and are never logged. The executor verifies the current
top-level origin immediately before the program runs.

`Runtime.evaluate` maintains remote objects unless they are returned by value
or explicitly released. It also defaults
`allowUnsafeEvalBlockedByCSP` to true. Use by-value results, avoid long-lived
remote objects, and explicitly set the CSP-bypass option to false unless a
specific reviewed program has a documented reason to require the privileged
behavior. ([CDP Runtime.evaluate][cdp-runtime])

### Browser-context HTTP calls

For session-bound BFF/GraphQL HTTP, use a fixed `fetch` program executed in
the correct first-party page:

- require HTTPS and an exact internal allowlist of REG.RU origins;
- always set `credentials: "include"` inside the browser;
- derive CSRF material inside the browser program and never return it;
- disallow caller-supplied `Cookie`, `Authorization`, `Origin`, `Host`,
  `Referer`, or browser-debugging headers unless a typed adapter owns a
  separately reviewed rule;
- enforce a caller deadline with `AbortController`;
- cap request and response sizes before returning bytes to Go; and
- return only status, an allowlisted subset of response headers, and the
  bounded body needed by the typed adapter.

This keeps cookies, Web Storage, and CSRF values in the browser. It also keeps
the broker smaller than a general browser proxy. WebSocket support should be a
later typed program, not a generic raw socket API.

Use `Network` events only when a compatibility probe must observe a request the
first-party page itself initiates. Filter by exact origin/path/method and
request ID. Never expose or log `requestWillBeSentExtraInfo`, raw headers,
cookies, post bodies, or unrelated page traffic. Read a body only after
`loadingFinished`, cap it, and stop on `loadingFailed`.

`chromedp.ListenTarget` invokes listeners synchronously on its event path and
warns that issuing an action from the callback can deadlock. A listener may
perform only a non-blocking enqueue of a redacted event key; a separate
goroutine performs any bounded `Network.getResponseBody` call.
([chromedp listeners][chromedp-listeners], [CDP Network events][cdp-network])

## Narrow repository surface

Keep the low-level `PageExecutor` private to
`internal/provider/portal/{cdp,bff}`. Billing, support, and Cloud adapters
should receive a session-bound executor but the Cobra layer should see only
the existing operation/use-case ports.

Recommended broker surface:

```go
type OpenSpec struct {
    BrowserPath string        // already resolved absolute path
    ProfileRef  profile.Ref   // opaque reference, never provider identity
    Mode        OpenMode      // staged login or committed-session reuse
    StartupCap  time.Duration
    CleanupCap  time.Duration
}

type Broker interface {
    Open(context.Context, OpenSpec) (Session, error)
}

type Session interface {
    Status(context.Context) (Status, error)
    Executor() PageExecutor // internal provider packages only
    Done() <-chan struct{}
    Close(context.Context) error
}
```

`OpenSpec` deliberately has no user-data path, CDP URL/port, raw Chrome flags,
remote target, proxy, cookie, storage, or secret field. The broker resolves
`ProfileRef` beneath its owned storage root and takes the lock. `Status`
contains only lifecycle/capability state and an opaque identity-match result,
never login, contract, service ID, URL, port, PID, or browser output.

`PageExecutor` is concurrent-read safe only if explicitly implemented that
way. The initial implementation should serialize navigation and evaluated
programs per page, while allowing cancellation. Provider mutations retain
their existing higher-level serialization and confirmation rules.

## Options considered

| Option | Primary-source findings | Decision |
| --- | --- | --- |
| `chromedp` + its pinned `cdproto` | Current v0.16.0 was tagged from a July 2026 commit and has current generated CDP bindings. It supplies launch, target/session multiplexing, evaluation, network domains, loss detection, process access, and graceful close. Its automation defaults are unsuitable, and Go 1.26 is required. | **Choose**, with explicit launch options and a repository-owned owner/cleanup layer. |
| `go-rod/rod` v0.116.2 | The latest release is from July 2024. Its default launcher is headless, enables a force-kill “leakless” helper, may auto-download a browser, disables several browser protections/features, and uses a mock keychain. Its browser also applies a default device emulation unless disabled. These are useful automation conveniences but are the wrong defaults and a larger surface for a credential-bearing CLI. | Do not choose for this broker. |
| Direct `os/exec` + WebSocket + handwritten CDP | Gives exact process ownership and could use only a WebSocket dependency, but the repository would own request IDs, concurrent response matching, flattened session routing, event backpressure, protocol errors/types, disconnect races, and generated-protocol drift. That is security-critical code `chromedp` already provides. | Reject as falsely minimal. |
| ChromeDriver/WebDriver | Adds a separately versioned executable and another lifecycle/protocol layer, while this ticket still needs CDP-like network/evaluation behavior and direct ownership of a dedicated Chrome profile. | Reject as non-minimal. |

Rod's defaults and lifecycle are visible in its own launcher/browser source.
([Rod launcher][rod-launcher], [Rod browser][rod-browser], [Rod
module][rod-mod])

## Dependency health check

Checked on 2026-07-31 using the Go module proxy, repository tags/commits, and
GitHub's Advisory API:

| Candidate | Release/activity signal | Compatibility | Advisory query |
| --- | --- | --- | --- |
| `github.com/chromedp/chromedp` | v0.16.0 points at `7963c203ed54`, committed 2026-07-14; the repository is active and not archived | v0.16.0 requires Go 1.26; v0.14.2 requires Go 1.24 | no reviewed advisory returned for the module |
| `github.com/go-rod/rod` | latest release v0.116.2, 2024-07-12; 2025–2026 main-branch changes observed were documentation/sponsorship rather than runtime releases | requires Go 1.21 | no reviewed advisory returned for the module |
| `github.com/gorilla/websocket` for a direct client | latest release v1.5.3, 2024-06-14; last observed code commit 2025-03-19 | requires Go 1.12 | historical GHSA-3xh2-74w9-5vxm affected versions before 1.4.1, not v1.5.3 |

([chromedp tags][chromedp-tags], [chromedp commits][chromedp-commits],
[Rod releases][rod-releases], [Rod commits][rod-commits], [gorilla
releases][gorilla-releases], [gorilla advisory][gorilla-advisory])

The recommendation is based on maintained protocol/lifecycle code, not
popularity. Pin exact module versions and sums. Add a scheduled/manual
dependency check because CDP's tip-of-tree surface is not stable, and keep the
runtime capability probe even after upgrades.

## Implementation acceptance checks

The prototype should not be considered complete until tests prove:

1. the launch argv contains only the explicit safe switches, a dedicated
   opaque user-data directory, port zero, and no secrets;
2. headed mode and Chrome sandboxing remain enabled;
3. a fake/missing/early-exit browser produces sanitized structured errors and
   no raw stderr, profile path, or endpoint;
4. endpoint bootstrap cannot use a stale profile from another process and no
   fixed/wildcard listener is configured;
5. target attachment, by-value evaluation, and one bounded in-page `fetch`
   work against a local integration origin;
6. canceling one call does not kill the session;
7. command cancellation invokes graceful close, then kills only the owned PID
   after the cleanup cap;
8. unexpected browser exit cancels every call and closes `Done`;
9. two account profiles never share a user-data directory, process, target,
   endpoint, storage, or executor; and
10. logs/JSON/fixtures contain no CDP URL, port, browser GUID, PID, cookie,
    CSRF value, raw request header/body, local storage, provider identity, or
    profile path.

Persistent reuse may ship with the dedicated Chrome user-data directory as the
accepted local session boundary. Enforce private ownership and permissions on
each supported OS, but do not add application-managed encryption, encrypted
containers, or a full-disk-encryption release gate.

[chrome-user-data]: https://chromium.googlesource.com/chromium/src/+/main/docs/user_data_dir.md
[chrome-136]: https://developer.chrome.com/blog/remote-debugging-port
[chromium-loopback]: https://chromium.googlesource.com/chromium/src/+/HEAD/chrome/browser/devtools/remote_debugging_server.cc#67
[chromium-devtools-handler]: https://chromium.googlesource.com/chromium/src/+/main/content/browser/devtools/devtools_http_handler.cc
[cdp-bootstrap]: https://chromedevtools.github.io/devtools-protocol/#how-do-i-access-the-browser-target
[cdp-browser-close]: https://chromedevtools.github.io/devtools-protocol/tot/Browser/#method-close
[cdp-target]: https://chromedevtools.github.io/devtools-protocol/tot/Target/#method-attachToTarget
[cdp-runtime]: https://chromedevtools.github.io/devtools-protocol/tot/Runtime/#method-evaluate
[cdp-network]: https://chromedevtools.github.io/devtools-protocol/tot/Network/
[cdp-tot]: https://chromedevtools.github.io/devtools-protocol/tot/
[go-exec]: https://pkg.go.dev/os/exec
[chromedp-v016-mod]: https://github.com/chromedp/chromedp/blob/v0.16.0/go.mod
[chromedp-v014-mod]: https://github.com/chromedp/chromedp/blob/v0.14.2/go.mod
[chromedp-allocate]: https://github.com/chromedp/chromedp/blob/v0.16.0/allocate.go
[chromedp-platform-launch]: https://github.com/chromedp/chromedp/blob/v0.16.0/allocate_linux.go
[chromedp-cancel]: https://github.com/chromedp/chromedp/blob/v0.16.0/chromedp.go#L242-L298
[chromedp-run]: https://pkg.go.dev/github.com/chromedp/chromedp@v0.16.0#Run
[chromedp-target-attach]: https://github.com/chromedp/chromedp/blob/v0.16.0/chromedp.go#L427-L471
[chromedp-listeners]: https://pkg.go.dev/github.com/chromedp/chromedp@v0.16.0#ListenTarget
[rod-launcher]: https://github.com/go-rod/rod/blob/v0.116.2/lib/launcher/launcher.go
[rod-browser]: https://github.com/go-rod/rod/blob/v0.116.2/browser.go
[rod-mod]: https://github.com/go-rod/rod/blob/v0.116.2/go.mod
[chromedp-tags]: https://github.com/chromedp/chromedp/tags
[chromedp-commits]: https://github.com/chromedp/chromedp/commits/main/
[rod-releases]: https://github.com/go-rod/rod/releases/tag/v0.116.2
[rod-commits]: https://github.com/go-rod/rod/commits/main/
[gorilla-releases]: https://github.com/gorilla/websocket/releases/tag/v1.5.3
[gorilla-advisory]: https://github.com/advisories/GHSA-3xh2-74w9-5vxm
