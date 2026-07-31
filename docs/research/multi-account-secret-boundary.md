# Multi-account profiles with a password-manager-neutral secret boundary

Research date: 2026-07-31

## Question and changed constraint

This note proposes the smallest architecture for
[Task: implement multi-account and 1Password profiles][issue-4] after one
important scope change:

> `regru` must not know about 1Password or any other particular password
> manager. Logins and passwords may enter only through an explicit, bounded
> stdin/inherited-FD channel. After ingress they either remain command-scoped
> or cross an abstract reference/store boundary. They never become command
> arguments, environment variables, normal config values, or output.

The issue title is therefore stale. The implementation should be
password-manager-neutral; a concrete 1Password adapter, `op://` syntax, and
invocation of the `op` executable are outside this ticket.

This is an architecture and regression-test contract, not an implementation.
It uses the current repository as the primary source and checks the filesystem,
configuration, and CLI details against the official TOML, XDG, Cobra, pflag,
and Go documentation. No credential was read or written during this research.

## Repository findings

The current scaffold already has several correct seams:

- `--account` is a root persistent flag with `REGRU_ACCOUNT` fallback.
- Cobra commands are reconstructed for every `Execute` call and all process
  I/O is injected through `Options`.
- The command handler passes one typed `Operation` to one `Executor`.
- `--dry-run` stops before the executor, and tests assert that opaque
  positional arguments are not rendered.
- Browser login, confirmation, timeout, output, and error behavior already
  have regression coverage.

See [`internal/cli/root.go`](../../internal/cli/root.go),
[`internal/cli/runtime.go`](../../internal/cli/runtime.go),
[`internal/cli/types.go`](../../internal/cli/types.go), and
[`internal/cli/execute_test.go`](../../internal/cli/execute_test.go).

Four current details cannot simply be extended in place:

1. `resolveAccount` mutates one string and loses where the selection came from.
   It also cannot distinguish an omitted `--account` from an explicitly empty
   flag. pflag exposes `FlagSet.Changed` for exactly this distinction.
   ([pflag source][pflag-changed])
2. `Operation.Account` is only an alias. A profile needs a random, stable local
   ID so renaming an alias cannot attach it to another account's browser state
   or credentials.
3. `Options.In` is both the prompt reader and Cobra stdin. A secret pipe cannot
   safely share a reader with confirmation prompts without an explicit rule.
   Cobra supports an injected stdin through `SetIn`/`InOrStdin`, so tests can
   keep this boundary fully in process. ([Cobra API][cobra-api])
4. The single broad `Executor` is adequate for unavailable placeholders, but
   profiles, secret acquisition, and provider execution have different
   lifetimes. If raw material is added to `Operation` it can leak through
   formatting, dry-run plans, fakes, or future logging.

The existing CLI contract says normal configuration contains no secrets and
defines precedence as `flags > environment > project config > user config >
defaults`. It does not yet define file formats.
([`docs/cli-contract.md`](../cli-contract.md))

Existing portal research independently establishes one isolated persistent
browser context per REG.RU principal and treats Cloud `serviceId` as a child
selector, not another account.
([`portal-session-lifecycle.md`](portal-session-lifecycle.md)) Existing
bootstrap research also shows why credential families must remain distinct:
CloudVPS uses a bearer token, S3 uses an access/secret pair, and REG.API uses a
username plus password/signature and IP authorization.
([`portal-credential-bootstrap.md`](portal-credential-bootstrap.md),
[`credential-provisioning-capability.md`](credential-provisioning-capability.md))

## Decision

Implement four separate boundaries:

```text
selection sources
    │
    ▼
ProfileResolver ──► Profile { stable ID, alias, non-secret metadata, opaque refs }
                               │
              ┌────────────────┴────────────────┐
              ▼                                 ▼
   CredentialProvider                    PortalSessionBroker
   ingress → ref store → missing         opaque session handle/state
              │                                 │
              └────────────────┬────────────────┘
                               ▼
                         typed use case
                               │
                               ▼
                         provider adapter
```

Cobra owns only grammar, channel selectors, and output. It resolves an account
profile before constructing an application input, but it never resolves,
formats, or stores a credential itself.

The production binary for this ticket should wire:

- the TOML/XDG profile repository;
- strict stdin and inherited-FD credential ingress;
- a `NoStore` implementation unless another store is explicitly supplied at
  composition time;
- the opaque portal-session boundary implemented by the browser-session
  ticket.

There must be no password-manager executable discovery, URI scheme, SDK,
special environment variable, or provider-specific config field in this
ticket. A future store can implement the same interface without changing
command handlers or provider clients.

This gives two valid modes:

1. **Command-scoped mode:** an external credential source writes a secret
   envelope to a pipe; `regru` consumes it for one command and discards it.
2. **Reference-backed mode:** a supplied `SecretStore` resolves opaque
   references or stores newly bootstrapped material and returns an opaque
   reference. Normal config contains only that reference.

CloudVPS and S3 can also be obtained just in time through the committed portal
session instead of being copied to a second store. With `NoStore`, browser
login must not fetch a credential merely to discard it. REG.API credentials
that cannot be recovered from the portal remain explicit ingress on each
operation until a store is supplied.

## Files and configuration

### User paths

Use `os.UserConfigDir()/regru/config.toml` as the cross-platform user config
path. On Unix this follows `$XDG_CONFIG_HOME` and defaults to
`$HOME/.config`; on macOS and Windows it uses the platform-native application
config directory. Go rejects an invalid relative XDG config path.
([Go `os.UserConfigDir`][go-user-config])

On Unix, persistent session/application state belongs under
`$XDG_STATE_HOME/regru`, defaulting to `$HOME/.local/state/regru`. XDG says
state is persistent across application restarts but not portable enough for
the data directory, which fits isolated browser profiles. XDG also requires
all base-directory environment values to be absolute; a relative value is
invalid and must be ignored. ([XDG Base Directory Specification][xdg])

There is no standard-library `UserStateDir`. Keep state path selection in a
small platform-specific function:

- Unix other than Darwin: XDG state path above;
- Darwin: `<UserConfigDir>/regru/state`;
- Windows: `%LocalAppData%/regru/state`.

The browser-session ticket owns the contents. Config code sees only an opaque
session reference. Directories should be private (`0700`) and regular config
files should be created with `0600` on Unix even though they contain no raw
secrets; references and account relationships are still private metadata.

### Project config

Discover the closest `.regru/config.toml` from the current directory upward.
Its schema is deliberately different and much smaller than user config:

```toml
schema_version = 1
account = "work"
```

Project config may select an existing user profile and later hold other
explicitly approved non-secret, non-routing project preferences. It must not
define profiles, endpoints, browser-session handles, or credential
references. This is a security boundary: a checked-out repository must not be
able to redirect credentials or replace their lookup reference.

Strict decoding should reject unknown project keys. This keeps a mistakenly
committed `[accounts]` or `[credentials]` table from being silently ignored.

### User config schema

Recommended v1:

```toml
schema_version = 1
default_account = "personal"

[accounts.personal]
id = "p_q7w2m6p4z9h3k5d8"
label = "Personal"
provider = "reg.ru"

[accounts.personal.portal]
session_ref = "opaque-session-reference"

[accounts.personal.cloud]
environment_id = "opaque-provider-service-id"

[accounts.personal.credentials]
cloudvps_ref = "opaque-store-reference"
regapi_ref = "opaque-store-reference"
s3_ref = "opaque-store-reference"

[accounts.work]
id = "p_m4r8v2c7n6k9t3x5"
label = "Work"
provider = "reg.ru"
```

Rules:

- `schema_version` is required and must equal `1`.
- Account aliases must match `^[a-z][a-z0-9_-]{0,62}$`. This makes command
  usage and TOML table names predictable.
- `id` is generated with `crypto/rand`, is unrelated to login/account data,
  and is immutable. IDs must be unique across profiles.
- `provider` is an enum owned by the application's provider registry, not a
  password-manager name.
- Login, contract number, email, password, bearer token, access-key ID, secret
  key, cookies, CSRF values, and identity fingerprints do not belong here.
- `*_ref` and `session_ref` are opaque bounded strings. Config parsing checks
  only length, UTF-8/control characters, and emptiness; it does not interpret
  a vendor URI or print a reference.
- Arbitrary network endpoints are omitted from v1. Provider adapters own
  production endpoints. Allowing project or profile config to redirect an
  authenticated request would turn non-secret configuration into an
  exfiltration primitive. Test endpoints remain injected dependencies.
- `account list`, `account show`, JSON, and plain output report only booleans
  such as `portalConfigured` and `regapiConfigured`, never references.

TOML is case-sensitive, maps unambiguously to a hash table, forbids defining a
key/table more than once, and permits nested tables such as the profile layout
above. ([TOML 1.0.0][toml]) Decode into typed structs with unknown fields
disallowed; `go-toml/v2` exposes `Decoder.DisallowUnknownFields`.
([go-toml API][go-toml]) As of the research date, the upstream repository is
active and its current release is v2.4.3 (2026-07-05), so it is a reasonable
single configuration dependency. ([go-toml releases][go-toml-release])
Do not add Viper for this small, security-sensitive merge: explicit typed
resolution is easier to audit.

Write config through a same-directory temporary file, sync/close it, and
rename it over the destination. Go documents that `os.WriteFile` can leave a
partially written file on mid-operation failure, so direct truncating writes
are not sufficient. ([Go `os` package][go-os])

### Selection precedence

Resolve only the account alias across sources:

```text
explicit non-empty --account
    > non-empty REGRU_ACCOUNT
    > closest project config account
    > user config default_account
    > no selection (account_required)
```

There is no implicit “first profile” and no prompt. A sole profile is not a
default until `account use` records it. An explicitly empty `--account=` is an
invocation error rather than permission to fall through; use
`cmd.Flags().Changed("account")` to distinguish it.

Profile definitions and references come only from user config. This is a
setting-specific interpretation of the documented precedence, not a generic
deep merge. Cobra documents persistent flags for cross-cutting values and
shows `flag > env > config > default` as a valid integration shape, but it
does not require Viper. ([Cobra flags guide][cobra-flags])

The resolver should return both `Profile` and a non-secret selection source
enum (`flag`, `environment`, `project`, `user-default`) for `account doctor`.
It must never include raw config fragments in errors.

## Application interfaces

Exact package names can follow implementation pressure, but the contracts
should look like this:

```go
package profile

type ID string
type Alias string

type Profile struct {
    ID       ID
    Alias    Alias
    Label    string
    Provider string
    Portal   PortalMetadata
    Cloud    CloudMetadata

    // References stay available to application services, not renderers.
    credentialRefs map[credential.Kind]credential.Reference
}

type Repository interface {
    Get(context.Context, Alias) (Profile, error)
    List(context.Context) ([]Profile, error)
    Add(context.Context, NewProfile) (Profile, error)
    Remove(context.Context, Alias) error
    SetDefault(context.Context, Alias) error
    Default(context.Context) (Alias, bool, error)
}
```

The selector loads user and project config and then asks the repository for
one profile. Operational inputs carry stable `Profile.ID` plus the alias
needed for human output; they do not carry a reference or secret:

```go
type Operation struct {
    Capability string
    Action     string
    ProfileID  profile.ID
    Account    profile.Alias
    Arguments  []string
}
```

Credential material is deliberately awkward to format:

```go
package credential

type Kind string
type Reference string

const (
    PortalPassword Kind = "portal-password"
    REGAPIPassword Kind = "regapi-password"
    CloudVPSToken  Kind = "cloudvps-token"
    S3KeyPair      Kind = "s3-key-pair"
)

// Material contains private byte slices. It has no String, GoString,
// MarshalJSON, MarshalText, or error implementation.
type Material struct {
    // unexported
}

// Lease guarantees one bounded lifetime and best-effort buffer clearing.
type Lease interface {
    Use(func(Material) error) error
    Close()
}

type StoreKey struct {
    ProfileID profile.ID
    Kind      Kind
}

type Store interface {
    Resolve(context.Context, Reference) (Lease, error)
    Put(context.Context, StoreKey, Material) (Reference, error)
    Delete(context.Context, Reference) error
    Probe(context.Context, Reference) (Status, error)
}

type Provider interface {
    Acquire(
        context.Context,
        profile.Profile,
        []Kind,
        ExplicitIngress,
    ) (Lease, error)
}
```

`Provider.Acquire` owns the policy:

1. use the explicitly supplied command-scoped ingress for matching fields;
2. resolve configured opaque references through `Store`;
3. ask the portal-session capability for a credential that is documented as
   retrievable just in time;
4. otherwise return a typed `credential_required` error.

It must not silently combine different sources for one atomic pair. S3 access
and secret keys come from the same ingress record or store reference.

`NoStore` reports `not_configured` from `Probe` and a typed
`secret_store_unavailable` from `Resolve`/`Put`; it never writes a plaintext
fallback. A future password manager, OS keychain, or agent sidecar implements
`Store` outside this ticket. The returned `Reference` remains opaque to
`regru`.

Store errors are classified before they cross into CLI rendering. Never render
an arbitrary store/adapter error string: helpers sometimes include command
arguments, response fragments, or item names in errors.

## Secret ingress protocol

### Channels

Add two mutually exclusive channel selectors to commands that can actually
consume credentials:

```text
--credentials-stdin
--credentials-fd N
```

The flag values select a channel; they do not contain secret data.
`--credentials-stdin` is cross-platform. `--credentials-fd` is an optional
Unix implementation for Bash and similar shells, and should return a clear
unsupported-platform error elsewhere. It accepts an already-open inherited
descriptor; `regru` never opens a user-supplied secret file path.

Examples with a generic external source:

```sh
credential-source regru/work |
  regru --account work --credentials-stdin --force auth login

regru --account work --credentials-fd 3 auth login \
  3< <(credential-source regru/work)
```

The external command writes the envelope to the pipe. No secret appears in
`os.Args`, the shell command text, normal stdout/stderr, or a `regru`
environment variable. `regru` has no knowledge of what produced the bytes.

FD ingress has the nicer interactive behavior because stdin remains a TTY for
confirmation. In stdin mode:

- no prompt may read from the same stream;
- a mutating command must use `--force` or fail before consuming credentials;
- `--no-input` retains its existing meaning and disables a fresh browser login;
- `--dry-run`, help, completion, and locally invalid invocations must not read
  the secret stream at all.

`auth login --credentials-stdin` is an explicit exception to the current
“stdin itself must be a TTY” guard: stdin is intentionally occupied by the
credential pipe, while interaction happens in the headed browser. It is
allowed only when `--no-input` is false, and the stdin-mode mutation rule above
still requires `--force`. A provider challenge still uses the browser; the
command must not fall back to prompting on the consumed pipe. Without the
explicit ingress flag, the existing stdin-TTY check remains unchanged.

An inherited descriptor must be closed after its one read. Reject descriptor
numbers `0`, `1`, and `2`, negative numbers, and unreasonable values.
Cancellation and the command deadline must bound the read; a pipe whose writer
never closes cannot hang the process indefinitely.

### Envelope

Use one strict JSON document with a 64 KiB maximum:

```json
{
  "schemaVersion": "regru.credentials/v1",
  "portal": {
    "login": "<private>",
    "password": "<private>"
  },
  "regapi": {
    "username": "<private>",
    "password": "<private>"
  },
  "cloudvps": {
    "token": "<private>"
  },
  "s3": {
    "accessKeyId": "<private>",
    "secretAccessKey": "<private>"
  }
}
```

Every field is optional at envelope level, but fields within a credential
family are all-or-nothing. Empty values are invalid; surrounding whitespace is
part of a secret and must not be trimmed. Unknown fields, duplicate object
members at any depth, multiple JSON documents, and trailing non-whitespace
data are invalid. A schema error reports only a stable code and, where useful,
a field *name* or byte offset—never the bad value or a source excerpt.

Read through `io.LimitReader` with one extra byte to detect oversize input.
([Go `io.LimitReader`][go-limit-reader]) Standard
`encoding/json.Decoder.DisallowUnknownFields` does not by itself reject
duplicate members, so perform a token-level duplicate-key check or use a
strict parser with both guarantees. Do not add a large configuration framework
for this envelope.

The ingress parser returns one `Lease`. Close it on success, provider failure,
timeout, cancellation, and panic-safe deferred cleanup. Clearing Go buffers is
best effort, not a promise that the runtime has made no copies. The enforceable
contract is that material never reaches arguments, environment, filesystem
config, formatting, logs, telemetry, output, crash attachments, or reusable
process-global state.

Portal login/password ingress is optional. It is handed to the browser-session
use case, which may fill only the verified `https://login.reg.ru` origin after
its compatibility check. CAPTCHA and second factor remain provider-page/user
interactions; this ingress is not a challenge bypass. The browser-session
ticket owns that behavior.

## Command UX

Keep the issue's intended management tree, but rename `account` consistently
instead of mixing “account” and “profile” in commands:

```text
regru account add NAME
regru account list
regru account show [NAME]
regru account use NAME [--project]
regru account remove NAME
regru account doctor [NAME]

regru capability list [--account NAME]
regru capability probe [--account NAME] [--credentials-stdin|--credentials-fd N]
```

Behavior:

- `account add` creates metadata and a random stable ID only. It never prompts
  for or reads a credential.
- `account list` needs no selected account. It emits alias, label, default
  status, and redacted capability/configuration booleans.
- `account show` uses its argument or normal selection precedence. It never
  displays refs, provider identity, or login.
- `account use NAME` atomically changes the user default.
  `account use NAME --project` writes only `account = "NAME"` to the closest
  project config, or creates `.regru/config.toml` in the current directory
  when none exists.
- `account remove` removes profile metadata and the CLI-owned portal state
  after existing mutation confirmation. It does **not** delete external-store
  entries automatically. Permanent credential deletion needs a future
  explicit store-owned command because reference removal and credential
  revocation are different actions.
- `account doctor` is redacted. It reports selected-source, config validity,
  portal state, reference presence/resolvability, and the next setup action.
  It does not contact REG.RU unless the user selects a separate probe.
- `capability list` reports configured/local states.
- `capability probe` performs bounded authenticated provider checks and may
  consume explicit ingress.

Operational commands continue to accept the global `--account`. Commands that
may need manual credentials also get the two ingress selectors. It is valid
for a command to succeed through a stored reference or portal session without
an ingress flag.

### One-login bootstrap handoff

`auth login` remains transactional:

1. resolve the stable profile;
2. optionally read a portal login/password lease from explicit ingress;
3. stage and verify a dedicated browser context;
4. verify that the authenticated principal matches the profile fingerprint;
5. atomically commit the opaque session handle;
6. discover downstream credential capability without rendering values;
7. if a writable `Store` was explicitly supplied and persistence was
   explicitly requested/confirmed, put each credential and atomically write
   returned opaque references;
8. otherwise leave retrievable CloudVPS/S3 credentials behind the portal
   session and fetch them just in time.

In the base `NoStore` build, step 7 is absent. No plaintext file fallback is
allowed. A failed login, mismatch, canceled store write, config-write failure,
or contract-drift error leaves the old committed session and refs untouched.
If `Store.Put` succeeds but the config transaction fails, attempt a bounded
compensating `Store.Delete`; report only an opaque cleanup status if that
fails.

The explicit request to persist newly bootstrapped credentials is separate
from `--force`: `--force` satisfies a confirmation but must not silently turn
command-scoped ingress into persistent storage.

## Changes to the current CLI seam

The least disruptive migration is:

1. Add `profile.Repository`, `profile.Selector`, `credential.Provider`, and
   `credential.Store` to `Options`/the composition root.
2. Preserve `Executor` temporarily for unavailable provider placeholders, but
   change `Operation` to carry stable `ProfileID` and alias.
3. Replace `appRuntime.resolveAccount`/`requireAccount` with a resolver that is
   called only for commands annotated as profile-dependent. Account discovery
   commands must run without a selected profile.
4. Split “may use a headed browser” from “may prompt on stdin” in the
   interactive preflight. The current single `InputIsTTY` condition cannot
   represent an explicit credentials-stdin browser login.
5. Keep Cobra flag parsing and output rendering in `internal/cli`; put TOML
   and XDG code in `internal/config` or `internal/profile`, and secret envelope
   code in `internal/credential/ingress`.
6. Never add a `Secrets map[string]string` field to `Operation`, `Result`,
   `CLIError.Details`, `context.Context`, or a package global.
7. Replace `AccountMismatch(expected, actual string)` with a redacted form
   whose details include only selected alias and `identity_match: false`.
   The current factory would be unsafe if `actual` became a provider login.

## Regression-test matrix

All rows should execute against injected readers, repositories, stores,
sessions, clocks, and provider fakes. No test needs a real account or secret
manager.

| Area | Cases and required proof |
| --- | --- |
| Selection precedence | Flag beats env/project/user; env beats project/user; project beats user; user default works; explicit empty flag is usage error; empty env falls through; unknown alias is configuration error; no source is `account_required`; no prompt occurs. |
| Profile identity | Two aliases resolve to distinct stable IDs and state/refs; alias rename preserves ID; duplicate alias and duplicate ID fail; deleting/re-adding an alias receives a new ID; account mismatch output never includes observed login/contract/fingerprint. |
| User paths | Absolute XDG config/state values work; empty values use defaults; relative XDG values are rejected/ignored per platform policy; Darwin/Windows paths use documented platform locations; project discovery chooses the closest ancestor deterministically. |
| TOML strictness | Missing/wrong schema version, duplicate keys/tables, unknown fields, invalid alias, invalid/oversize ref, credential value in config, and forbidden project tables all fail closed. Valid two-account config round-trips without changing stable IDs. |
| Config writes | Add/use/remove are atomic; a simulated write/rename failure leaves original bytes intact; created Unix modes are private; concurrent modification is detected rather than overwritten; no secret sentinel appears in temp or final files. |
| Account command UX | `list` and `add` work without selection; `show` argument beats normal selection; user/project `use` touch only their target; remove needs TTY confirmation or force; doctor reports status/next action but not refs or raw identity. |
| Ingress channel | stdin and FD are mutually exclusive; unsupported FD platform is typed; invalid FD is rejected before provider work; FD is closed once; stdin mode never prompts; mutation plus stdin requires force before reading; FD mode may retain TTY confirmation. |
| Envelope parser | Valid subsets work; empty and half-pairs fail; 64 KiB boundary works; oversize, EOF, canceled read, timeout, malformed JSON, duplicate fields, unknown fields, trailing bytes, and multiple documents fail with generic redacted errors. |
| No unnecessary reads | Help, completion, version, invalid args, unknown account, local validation failure, and dry-run do not read ingress, resolve a store ref, open a browser, or call an executor/provider. |
| Store lifecycle | `NoStore` never writes; explicit ingress wins only for supplied kinds; refs resolve by stable profile ID; every lease closes once on success/error/cancel; S3 pair is atomic; `Put` then failed config commit triggers cleanup; one account can never resolve another account's refs. |
| One-login transaction | Failed/canceled/mismatched login changes neither committed session nor refs; `NoStore` does not retrieve-and-drop portal credentials; store persistence requires an explicit request plus confirmation; partial store/config failures preserve the previous committed profile. |
| Redaction | Put distinctive sentinels in every secret field and in mocked underlying errors. Assert absence from human/plain/JSON stdout and stderr, help, dry-run, config, error details, `%v`, `%+v`, `%#v`, logs, and recorded fake operations. Assert refs and session handles are also absent from normal output. |
| Compatibility | Existing help/version/output-mode/confirmation/timeout/cancellation/capability-placeholder tests continue to pass. `--account` examples retain their current spelling and `regru.cli/v1` envelopes remain stable unless a deliberate schema change is made. |

The redaction suite should scan byte output, not only parsed JSON fields. It
should also make fake store/provider errors contain the sentinel, proving that
error classification does not accidentally render wrapped causes.

## Scope boundary for the ticket

In scope:

- deterministic multi-account selection and commands;
- strict user/project config;
- stable local profile IDs and isolated session/reference association;
- provider-neutral stdin/FD ingress and strict envelope parsing;
- abstract `Store`/reference boundary plus safe `NoStore`;
- redacted doctor/capability views and the regression matrix above;
- browser-login handoff contract needed by the session ticket.

Out of scope:

- 1Password, `op`, `op://`, Keychain, Secret Service, Credential Manager, or
  any other concrete store;
- invoking an arbitrary configured resolver command;
- plaintext secret files or environment-secret fallback;
- storing credentials automatically merely because a store is available;
- CAPTCHA or second-factor automation;
- changing the established provider-specific credential and portal-session
  contracts.

This split preserves the useful result of the original ticket—multiple
accounts and reusable secret references—without coupling the binary or config
format to one password manager. It also gives Bash and other automation a
simple path today: produce one bounded envelope on a pipe, consume it once,
and keep every secret out of arguments and output.

## Primary sources

- Current repository:
  [`docs/cli-contract.md`](../cli-contract.md),
  [`internal/cli/root.go`](../../internal/cli/root.go),
  [`internal/cli/runtime.go`](../../internal/cli/runtime.go),
  [`internal/cli/types.go`](../../internal/cli/types.go), and
  [`internal/cli/execute_test.go`](../../internal/cli/execute_test.go)
- [TOML v1.0.0 specification][toml]
- [XDG Base Directory Specification 0.8][xdg]
- [Cobra: Working with Flags][cobra-flags]
- [Cobra Go API][cobra-api]
- [pflag `Changed` source][pflag-changed]
- [Go `os` package][go-os]
- [Go `io` package][go-limit-reader]
- [`go-toml/v2` API][go-toml] and [upstream releases][go-toml-release]

[issue-4]: https://github.com/adinvadim/reg-ru-cli/issues/4
[toml]: https://toml.io/en/v1.0.0
[xdg]: https://specifications.freedesktop.org/basedir/0.8/
[cobra-flags]: https://cobra.dev/docs/how-to-guides/working-with-flags/
[cobra-api]: https://pkg.go.dev/github.com/spf13/cobra
[pflag-changed]: https://github.com/spf13/pflag/blob/v1.0.9/flag.go#L534
[go-user-config]: https://pkg.go.dev/os#UserConfigDir
[go-os]: https://pkg.go.dev/os
[go-limit-reader]: https://pkg.go.dev/io#LimitReader
[go-toml]: https://pkg.go.dev/github.com/pelletier/go-toml/v2
[go-toml-release]: https://github.com/pelletier/go-toml/releases/tag/v2.4.3
