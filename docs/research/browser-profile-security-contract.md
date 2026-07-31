# Dedicated Chrome profile security contract

Research date: 2026-07-31

## Scope and evidence standard

This note defines the safe, testable runtime contract for the dedicated headed
Chrome/Chromium profile in
[Prototype: implement browser-authenticated portal sessions][issue]. It covers
profile isolation, concurrent ownership, CDP discovery and exposure, shutdown,
logout, crash recovery, filesystem protection, and secret classification for a
cross-platform Go CLI.

The browser facts below come from current first-party Chrome/Chromium source,
the Chrome DevTools Protocol, Chrome's security announcement, and the official
Go and Windows APIs. Requirements marked **CLI policy** are conservative
product decisions derived from those facts; Chrome does not promise them on
behalf of `regru`.

This research did not launch an authenticated browser, read a profile, or
change provider state.

Product decision after this research: protect the dedicated profile like an
ordinary browser profile. Private OS ownership and permissions are required;
application-managed encryption, encrypted containers, and mandatory
full-disk-encryption checks are deliberately out of scope.

## Decision

Give every `regru` account profile one opaque, persistent, browser-owned user
data directory. A command may open that directory only while holding an
independent `regru` exclusive lock, and it must launch the browser executable
directly with:

```text
--user-data-dir=<absolute dedicated path>
--remote-debugging-port=0
```

The CLI discovers the fresh endpoint from `DevToolsActivePort`, connects only
to an explicit loopback address, validates the browser over the browser-level
WebSocket, and never persists or prints the endpoint. CDP lives only for the
bounded lifetime of an owning command/broker; it is not a background local
service.

The whole user data directory is secret material. Chrome says it contains
cookies and other profile/local state, and its 2025 security change explicitly
describes remote debugging as a cookie-extraction path. CDP itself has no
authentication and exposes data from open pages. A random port and browser GUID
reduce accidental attachment but are not an authorization boundary.
([Chromium user data directory][user-data-dir],
[Chrome remote-debugging security change][remote-debugging-change],
[Chromium DevTools HTTP handler][devtools-handler])

Shutdown and logout are different operations:

- **Stop** gracefully closes the owned browser and removes only ephemeral
  broker metadata. It preserves the profile so a later command can reuse the
  authenticated session.
- **Logout** first performs the provider's in-browser logout and verifies that
  an authenticated probe is rejected or identifies no principal. It then
  gracefully closes the browser and removes the dedicated profile. Deleting
  local files alone is not evidence that the server-side session was revoked.
- **Forget local session** may be a separate explicit recovery operation when
  provider logout is impossible. It must say that remote revocation is
  unverified.

This is intentionally stricter than “launch Chrome on a free port.” The
interesting security boundary is exclusive ownership of one secret profile
plus a short-lived unauthenticated control socket.

## Facts that shape the contract

### A user data directory is the isolation unit

Chromium documents that the user data directory contains history, bookmarks,
cookies, and per-installation local state, with individual profiles beneath it.
It supports overriding the directory with `--user-data-dir`; Chrome also notes
that two running instances cannot share the same user data directory.
([User data directory][user-data-dir])

Chrome 136 and later ignore `--remote-debugging-port` and
`--remote-debugging-pipe` against the default Google Chrome data directory.
They require a non-default `--user-data-dir`; Chrome describes the resulting
different encryption key as protection against attackers using remote
debugging to extract cookies. This makes the dedicated directory both a
functional requirement and a security invariant, not a convenience.
([Remote-debugging security change][remote-debugging-change],
[current remote-debugging gate][remote-debugging-server])

Chromium's storage policy also warns that an older browser may partially use
and modify files written by a newer one while leaving incompatible files
untouched. A persistent authentication profile therefore needs a recorded
browser product/channel and milestone and must reject a downgrade rather than
trying it.
([Chromium user-data storage compatibility][user-data-storage])

### Chrome enforces a process singleton, but it is not the broker lock

Chromium's `ProcessSingleton` is keyed by user data directory so no more than
one Chrome instance should run against that directory. Its implementation and
artifacts differ across Windows, Linux, and macOS, and its result space includes
`PROFILE_IN_USE` and `LOCK_ERROR`.
([Chromium ProcessSingleton][process-singleton])

That protects Chrome's files; it does not serialize two `regru` commands before
Chrome starts, bind ownership to an account operation, or make stale broker
metadata trustworthy. `regru` therefore needs its own OS-backed exclusive lock
per opaque profile ID. It must never “repair” concurrency by deleting Chrome's
`Singleton*` files: those belong to Chrome, and removing them while a browser
is alive can turn a clean busy failure into profile corruption.

### Port zero and `DevToolsActivePort` are the supported discovery path

The CDP documentation states that `--remote-debugging-port=0` chooses an open
port and writes the browser endpoint to stderr and to `DevToolsActivePort` in
the browser profile folder. Chromium writes two fields: the selected port on
the first line and the browser target path/GUID on the second.
([CDP discovery documentation][cdp],
[Chromium endpoint-file writer][devtools-handler])

ChromeDriver removes an old `DevToolsActivePort` before launch and refuses to
continue when it cannot do so, explicitly identifying a still-attached user
data directory as a likely cause. It waits for a newly populated file while
also checking whether the Chrome process exited. This is the correct model for
`regru`; selecting a port with a bind-close-launch sequence would create a
time-of-check/time-of-use race.
([ChromeDriver launcher][chromedriver-launcher])

The endpoint file is not an atomic readiness oracle. The reader must tolerate
missing, empty, partially written, and temporarily malformed contents until a
bounded startup deadline. It must stop immediately if the owned browser
process exits. A valid file has a decimal port in `1..65535` and a browser path
matching `/devtools/browser/<nonempty bounded token>`; extra data is a failure,
not something to pass into a URL.

### Loopback is necessary, not sufficient

Current desktop Chrome's remote-debugging server tries
`127.0.0.1:<port>` and then `[::1]:<port>`; it does not create a wildcard
listener. Chromium's own switch documentation says the default is loopback and
warns that the protocol performs no authentication. The DevTools handler's
traffic annotation says debugging data includes data on open pages.
([current Chrome socket factory][remote-debugging-server],
[Chromium remote-debugging switch][remote-debugging-switch],
[DevTools HTTP handler][devtools-handler])

**CLI policy:** connect to `127.0.0.1` first and `::1` second, never to a host
read from stderr, DNS, profile data, or provider content. Do not rely on
`--remote-debugging-address` as the sole control: current regular Chrome binds
loopback in its socket factory, while that switch is documented for other
Chromium shells. Browser allowlisting and integration tests must prove that
every supported executable exposes only loopback.

The browser WebSocket URL is formed from the locally read second line, not from
an arbitrary `/json/version` host. The CLI should fetch `/json/version` only
from that selected loopback port, require a browser-level
`webSocketDebuggerUrl`, compare its path to the file, and call
`Browser.getVersion` before treating startup as successful. The product and
protocol version are compatibility inputs; they are not account identity.
([CDP HTTP endpoints][cdp], [CDP Browser domain][browser-domain])

### Graceful close is part of profile integrity

CDP defines `Browser.close` as “Close browser gracefully.” Chromium separately
tracks clean, forced, and crashed exits, and deliberately retains crash state
across some restarts so the user can restore data. A force-kill is therefore
not equivalent to a clean close.
([CDP Browser domain][browser-domain],
[Chromium exit-type service][exit-type])

**CLI policy:** shutdown sends `Browser.close`, waits for the browser process
tree and CDP listener to disappear, and only then releases the broker lock. A
bounded escalation may terminate the exact owned process tree, but it records
an unclean stop and preserves the profile for recovery. On Windows, a Job
Object can manage child processes as a unit; on Unix, use an owned process
group/session. Never kill by executable name or enumerate and kill unrelated
Chrome processes.
([Windows Job Objects][job-objects], [POSIX process groups][process-groups],
[Go command cancellation][go-exec])

## Secret and metadata classification

| Material | Classification | Handling |
| --- | --- | --- |
| Entire user data directory, including `Local State`, `Default/`, Cookies, Web Data, Local Storage, Session Storage, IndexedDB, service workers, cache, session-restore data, and crash/log artifacts | **Secret** | Dedicated protected state root; never config, logs, fixtures, diagnostics, backup export, or issue text |
| Cookie values, authorization/CSRF tokens, WebSocket credentials, request/response bodies, DOM values, screenshots, downloaded files | **Secret** | Remain in browser context or explicitly approved secret/output boundary; never routine CDP/network logging |
| `DevToolsActivePort` contents, CDP port, browser GUID/path, full WebSocket URL | **Ephemeral control capability; treat as secret** | Read only under profile lock; memory only; redact; remove after confirmed browser exit |
| Chrome stdout/stderr | **Potentially secret** | Never inherit into normal CLI output; bounded protected capture or discard; redact the `DevTools listening on ...` line |
| Account login, email, contract, provider principal, raw identity fingerprint | **Private/secret under project rules** | Never use in paths, lock names, process arguments, or errors |
| Opaque stable local profile ID and browser product/milestone | Private metadata | May live in private config/state; output only where the established profile contract permits |
| Browser PID, start time, clean/unclean state | Private operational metadata | Protected broker state; not proof of ownership on its own |

Chrome/OS cookie encryption is defense in depth, not the `regru` storage
contract. File permissions do not provide encryption at rest, and Chrome's
statement about a different key does not establish uniform protection across
Chrome/Chromium brands and platforms. The accepted product boundary is the
dedicated, privately owned browser profile itself; `regru` does not add its own
encryption layer or require full-disk encryption.

Do not enable verbose Chrome logging, net-export, performance logging, tracing,
or automatic fixture capture in production. Any explicitly requested
diagnostic artifact inherits the secret classification of the profile and
requires a separate opt-in redaction/export design.

## Testable requirements

### Storage and account isolation

1. **BP-001 — opaque path.** Derive the profile directory only from the stable
   random local profile ID beneath one trusted state root. The path contains no
   alias, login, email, contract number, provider ID, or user-supplied path
   segment.
2. **BP-002 — one-to-one mapping.** Two account IDs always resolve to different
   user data directories; renaming an account alias does not change its
   directory.
3. **BP-003 — dedicated/non-default.** Refuse a path equal to, inside, or
   resolving through a symlink/reparse point to a detected Chrome/Chromium
   default user data directory. Refuse the CLI config directory and workspace
   as a profile root.
4. **BP-004 — no traversal.** Resolve all managed files relative to an opened
   trusted root; reject symlinks/reparse points in the managed path before
   deletion or permission repair.
5. **BP-005 — browser affinity.** Store private metadata for the browser
   product, channel/path identity, and last successful milestone. Refuse a
   product/channel change or milestone downgrade until an explicit migration
   exists.

### Filesystem protection

6. **BP-010 — Unix permissions.** Create state/profile directories with
   `0700` and broker-created regular files with `0600`, then verify effective
   mode and owner before every launch. Fail closed on group/other access or an
   unexpected owner; do not silently continue after failed repair.
7. **BP-011 — Windows ACL.** Create or verify a protected DACL allowing the
   current user and required system principals, with no broad `Everyone`,
   `Users`, or inherited read access. `os.Chmod` is not an implementation:
   Go documents that Windows uses only its owner-write bit, while Win32
   otherwise inherits the parent's ACL.
   ([Go `os.Chmod`][go-os], [Windows file security][windows-file-security])
8. **BP-012 — unsuitable storage.** Refuse a state root whose filesystem
   cannot enforce the platform's required permissions/ACL, and warn or fail per
   project policy for a network/shared filesystem. Do not place persistent
   profiles in the general temporary directory.
9. **BP-013 — post-create audit.** Audit the entire existing profile tree for
   unsafe ownership and broad permissions before reuse. Chrome-created
   descendants may have stricter permissions; none may be broader than the
   protected root's effective boundary.

### Exclusive ownership and startup

10. **BP-020 — broker lock first.** Acquire a non-blocking OS-backed exclusive
    lock for the opaque profile before inspecting/deleting
    `DevToolsActivePort`, launching Chrome, performing CDP work, logout, or
    profile deletion.
11. **BP-021 — busy is safe.** A second command for the same profile returns a
    structured `portal_profile_busy` error with no attach, kill, lock-file
    deletion, or endpoint disclosure. Different profiles may run concurrently.
12. **BP-022 — stale metadata is not a lock.** PID/start-time files are
    diagnostic metadata only. An OS-released lock after a crash can be
    reacquired; PID reuse alone never authorizes killing or attaching.
13. **BP-023 — Chrome locks are owned by Chrome.** Never delete or rewrite
    `SingletonLock`, `SingletonSocket`, `SingletonCookie`, or platform
    equivalents. If Chrome reports the profile in use or the launched process
    exits before readiness, return `portal_profile_in_use`/`browser_start_failed`.
14. **BP-024 — direct launch.** Execute the concrete browser binary directly,
    without a shell and, on macOS, without `open`. Pass an absolute dedicated
    `--user-data-dir` and `--remote-debugging-port=0`; pass no credential,
    account identity, provider token, fixed CDP port, or secret environment
    variable.
15. **BP-025 — fresh endpoint.** While holding the lock, remove only a regular
    `DevToolsActivePort` file from the managed root before launch. Refuse a
    symlink, directory, permission failure, or other unexpected type.
16. **BP-026 — bounded readiness.** Poll for a new, complete endpoint file
    until a configurable startup deadline while concurrently watching the
    process. Missing/partial content is retryable inside the deadline; invalid
    complete content, process exit, or timeout is structured and actionable.

### CDP containment and validation

17. **BP-030 — strict parse.** Accept only a decimal port `1..65535` and a
    bounded `/devtools/browser/<token>` path. Reject hostnames, schemes,
    queries, fragments, control characters, extra lines, traversal, and
    oversized files.
18. **BP-031 — literal loopback.** Dial only `127.0.0.1` and then `::1`.
    Never use `0.0.0.0`, `[::]`, `localhost` DNS resolution, or an address from
    browser/provider output.
19. **BP-032 — endpoint identity.** `/json/version` and the WebSocket must use
    the discovered port; the returned WebSocket path must equal the freshly
    read browser path. A successful `Browser.getVersion` is required before
    any session probe.
20. **BP-033 — no endpoint persistence.** The port, GUID, and WebSocket URL
    never enter config, result objects, JSON/plain output, error details,
    telemetry, or normal logs. After the browser exits, remove the regular
    endpoint file while still holding the lock.
21. **BP-034 — exposure lifetime.** Start CDP only for an owning bounded
    operation/broker. A completed command gracefully closes Chrome; no
    detached debug-enabled Chrome remains as the normal success path.
22. **BP-035 — supported binary proof.** Integration tests for every supported
    browser/OS assert that the listener is present on exactly one loopback
    family and absent on all wildcard/non-loopback interfaces. An unknown fork
    or changed behavior fails compatibility checks.
23. **BP-036 — narrow CDP surface.** Do not enable general Network/Log tracing
    by default. CDP calls and evaluated scripts are allowlisted and typed;
    provider-controlled strings are data, never script or method names.

### Stop, logout, deletion, and recovery

24. **BP-040 — graceful stop.** Send `Browser.close`, wait for the exact owned
    process tree and listener to exit, then remove ephemeral metadata and
    release the lock. Preserve the persistent profile.
25. **BP-041 — bounded escalation.** If graceful close exceeds its deadline,
    terminate only the launched process group/Windows Job Object, classify the
    stop as unclean, preserve the profile, and return a structured cleanup
    warning/error. Never kill all Chrome processes.
26. **BP-042 — remote logout first.** `auth logout` executes the current
    provider logout in the same browser context and requires a fresh
    unauthenticated probe before local deletion. A transport loss after logout
    submission is `logout_outcome_unknown`, not success.
27. **BP-043 — local purge second.** Only after confirmed browser exit and
    while holding the broker lock may logout rename the exact managed profile
    directory to a same-filesystem tombstone and delete it. Any path-boundary
    or ownership mismatch aborts deletion.
28. **BP-044 — deletion honesty.** Report logical removal, not cryptographic
    erasure. Filesystem journals, SSD remapping, snapshots, backups, crash
    reports, and provider-side sessions may retain data.
29. **BP-045 — explicit local forget.** When provider logout cannot be
    confirmed, an explicit separate operation may close and purge local state,
    but its result says `remote_revocation_unverified`; ordinary `logout`
    does not silently downgrade to it.
30. **BP-046 — crash recovery.** On startup after an unclean stop, reacquire
    the broker lock, verify no owned browser/process tree is still alive,
    discard only stale broker endpoint metadata, and relaunch the same profile
    on a new random port. Never restore or attach to the old port.
31. **BP-047 — post-crash state.** Treat the provider session as `unknown`
    until an in-browser authenticated probe succeeds and its principal
    fingerprint matches the selected profile. Mismatch is fail-closed and
    never auto-rebinds the profile.
32. **BP-048 — repeated crash quarantine.** Repeated startup/profile crashes
    move the profile to `repair-required`; do not loop, auto-delete, or copy its
    cookies into a fresh profile. Preserve it for explicit local forget or a
    future safe migration.

## Required integration and fault tests

The prototype is not complete with mocked CDP alone. Its test matrix should
include:

| Scenario | Expected result |
| --- | --- |
| Two goroutines/processes start the same profile | Exactly one obtains the broker lock; the other returns `portal_profile_busy` before touching Chrome |
| Two different account profiles start together | Separate directories, ports, browser GUIDs, cookies/storage, and locks |
| Stale `DevToolsActivePort` before launch | Removed under lock; only a newly written endpoint is accepted |
| Endpoint file missing, empty, one-line, partially written, oversized, symlinked, or malformed | Retry only incomplete content within deadline; otherwise fail closed without dialing attacker-controlled input |
| Browser exits before endpoint readiness | Immediate structured launch failure including safe exit classification, no endpoint/path leak |
| Port file points to a non-listening or wrong CDP endpoint | Compatibility/start failure; no fallback to arbitrary local port |
| Supported Chrome on IPv4 and an environment forcing IPv6 fallback | Dial succeeds only on the corresponding literal loopback |
| Socket inspection on Windows, macOS, Linux | No wildcard or non-loopback listener |
| Second raw Chrome manually uses the same directory | No attach or lock deletion; structured profile-in-use/start failure |
| Ctrl-C/timeout during login | Graceful `Browser.close`; bounded escalation; lock eventually released; profile preserved |
| CLI process is killed mid-session | OS lock releases; next run ignores stale metadata, gets a new CDP endpoint, and re-probes session identity |
| Browser renderer crash | Target/session failure is reported; browser ownership remains intact; no automatic destructive cleanup |
| Browser main-process crash | Profile marked unclean/unknown; endpoint is not reused; relaunch requires lock and identity probe |
| Browser milestone downgrade or product/channel switch | Refused before launch |
| Unix profile mode `0755`, wrong owner, or symlink in managed path | Repair if safely possible under policy, otherwise fail closed |
| Windows inherited ACL grants another normal user read access | Fail before browser launch; `os.Chmod` does not count as repair |
| Successful stop | Browser/process tree and listener gone; profile retained; endpoint metadata removed |
| Confirmed provider logout | Unauthenticated probe, clean browser exit, exact profile tombstoned/deleted |
| Ambiguous logout response | No success claim and no implicit local purge; return `logout_outcome_unknown` |
| Force-kill required | Profile retained, unclean state recorded, no claim of graceful cleanup |
| Errors/logging/JSON snapshots | No profile path containing identity, CDP URL/GUID/port, cookies, storage, DOM, request bodies, or browser stderr |

Real-browser tests should use a synthetic local origin and non-sensitive test
cookies/storage. They must not check real REG.RU credentials or session
material into fixtures.

## Failure modes and structured outcomes

| Failure | Safe classification | Required behavior |
| --- | --- | --- |
| Browser not found/not executable | `browser_unavailable` | No profile mutation beyond safe directory/lock setup |
| Enterprise policy/default-dir rule disables CDP | `browser_debugging_disabled` | Explain supported dedicated-profile requirement; do not weaken policy |
| Profile broker lock held | `portal_profile_busy` | No attach, kill, or stale-file cleanup |
| Chrome singleton/profile already in use | `portal_profile_in_use` | Preserve Chrome artifacts and profile |
| Unsafe owner/mode/DACL/filesystem | `portal_profile_permissions_unsafe` | Fail before launch |
| Browser product/channel mismatch or downgrade | `browser_profile_incompatible` | No launch; require explicit migration/local forget |
| Browser exits during startup | `browser_start_failed` | Include only exit category and browser product, never raw stderr/profile path |
| Endpoint discovery timeout/malformed file | `browser_debug_endpoint_unavailable` | Graceful close/escalation of owned tree; preserve profile |
| CDP connects but version/path validation fails | `browser_debug_endpoint_mismatch` | Close owned browser; never try other ports |
| CDP disconnects/target crashes | `browser_session_interrupted` | Mutation outcome rules remain authoritative; preserve profile and mark session unknown |
| Principal differs from selected profile | `portal_account_mismatch` | Stop before private work; never rewrite binding automatically |
| Graceful close deadline exceeded | `browser_cleanup_incomplete` | Kill exact owned tree if allowed, preserve profile, mark unclean |
| Provider logout response lost | `logout_outcome_unknown` | No retry unless provider contract proves idempotence; offer human verification/local forget |
| Local profile purge incomplete | `local_session_cleanup_incomplete` | Keep tombstone/reference private; retry only exact managed path |
| Repeated profile crash | `browser_profile_repair_required` | Quarantine; no auto-delete or cookie export |

Errors should carry stable codes and safe remediation, not raw filesystem
paths, process arguments, endpoint contents, browser stderr, provider
responses, or identity values.

## Cross-platform implementation notes

- Go's `os.Mkdir` modes are applied before Unix `umask`, so creation must be
  followed by verification. On Windows, Go's permission bits do not implement
  confidentiality; use Windows security descriptors/DACL APIs.
  ([Go `os` package][go-os], [Windows `CreateDirectory`][create-directory])
- A per-profile file lock must be an OS-held handle whose exclusivity
  disappears when the owning process dies. The adjacent metadata file may
  record an opaque owner nonce, PID, and process start time for diagnostics,
  but recovery trusts lock acquisition and process ownership, not those
  values.
- On Windows, assign the directly created browser process to a Job Object
  before allowing it to run where practical; children normally inherit the
  job. On Unix, start a new owned process group/session. These are containment
  tools for escalation, not the normal shutdown path.
- Use a custom `exec.Cmd.Cancel` that requests graceful CDP shutdown before
  force-kill, and set a finite `WaitDelay`; Go's default `CommandContext`
  cancellation kills only the command process and may leave descendants or
  unflushed profile state.
- Keep browser stdout/stderr pipes bounded. Chrome also prints the complete
  debug WebSocket endpoint to stderr, so inherited terminal output would
  violate the secret-output contract even before a provider page logs
  anything.
- The state root is persistent application state, not cache: deleting it logs
  the user out locally. Normal config stores only the established opaque
  session reference/non-secret routing metadata, never the browser directory
  or endpoint.

## Explicit non-guarantees

- Loopback does not protect against malicious code running as the same OS user,
  local endpoint races by a process able to modify the protected directory, or
  a compromised browser binary.
- A random port and browser GUID are not authentication.
- Private filesystem permissions are not encryption at rest and do not defeat
  privileged administrators, backups, snapshots, or malware in the user's
  security context.
- Graceful browser close does not log out of REG.RU.
- Local profile deletion does not prove provider-side revocation or secure
  erasure.
- Chrome profile formats and tip-of-tree CDP are not stable across arbitrary
  downgrades/forks. The CLI must probe supported browser versions and fail
  closed on drift.

[issue]: https://github.com/adinvadim/reg-ru-cli/issues/15
[user-data-dir]: https://chromium.googlesource.com/chromium/src/+/main/docs/user_data_dir.md
[user-data-storage]: https://chromium.googlesource.com/chromium/src/+/main/docs/user_data_storage.md
[remote-debugging-change]: https://developer.chrome.com/blog/remote-debugging-port
[remote-debugging-server]: https://chromium.googlesource.com/chromium/src/+/main/chrome/browser/devtools/remote_debugging_server.cc
[remote-debugging-switch]: https://chromium.googlesource.com/chromium/src/+/HEAD/content/shell/common/shell_switches.h
[process-singleton]: https://chromium.googlesource.com/chromium/src/+/main/chrome/browser/process_singleton.h
[cdp]: https://chromedevtools.github.io/devtools-protocol/
[browser-domain]: https://chromedevtools.github.io/devtools-protocol/tot/Browser/
[devtools-handler]: https://chromium.googlesource.com/chromium/src/+/main/content/browser/devtools/devtools_http_handler.cc
[chromedriver-launcher]: https://chromium.googlesource.com/chromium/src/+/main/chrome/test/chromedriver/chrome_launcher.cc
[exit-type]: https://chromium.googlesource.com/chromium/src/+/main/chrome/browser/sessions/exit_type_service.h
[go-os]: https://pkg.go.dev/os
[go-exec]: https://pkg.go.dev/os/exec#Cmd
[windows-file-security]: https://learn.microsoft.com/en-us/windows/win32/fileio/file-security-and-access-rights
[create-directory]: https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-createdirectory
[job-objects]: https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects
[process-groups]: https://pubs.opengroup.org/onlinepubs/9799919799/functions/setpgid.html
