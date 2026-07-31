# Testable Cobra foundation architecture

Research date: 2026-07-31

## Question and evidence boundary

This note recommends the foundation for a Go/Cobra `regru` CLI whose command
contracts can be tested before the REG.RU adapters are complete. It covers:

- separation of Cobra handlers from public API, S3, browser/CDP, and private
  portal BFF implementations;
- dependency injection without package globals;
- deterministic command execution tests with separately captured stdout and
  stderr;
- cancellation and timeout propagation;
- a mechanically provable `--dry-run` guarantee;
- capability-gated command placeholders that fail closed.

The repository currently contains research and domain notes but no `go.mod`,
Go source, or executable scaffold. The structure below is therefore a
recommended contract, not a description of existing code.

Primary evidence is limited to the current Cobra documentation and source and
the Go project's documentation and standard library. Existing local REG.RU
research informs the adapter boundaries, especially the distinction between
published APIs and private browser-session/BFF operations, but is not treated
as a framework authority.

## Decision

Build one composition root that returns a fresh `*cobra.Command` for every
execution:

```go
func NewRoot(deps Dependencies) *cobra.Command
```

`main` should wire side-effect-free concrete adapters or factories, establish
the signal-cancelled context, and call `ExecuteContext`. Constructors must not
open a browser or contact a provider; those effects begin only in an allowed
live operation. Every command should be constructed by a function, use
`RunE`, parse flags into a command input, and call one injected use-case
interface. A Cobra handler must not import `net/http`, an S3 SDK, a CDP
implementation, GraphQL documents, or a provider wire type.

This goes beyond Cobra's example layout, but follows its intended seams:
Cobra documents a deliberately bare `main`, permits subcommands in their own
packages, returns operational errors through `RunE`, supplies the execution
context through `ExecuteContext`, and exposes `SetArgs`, `SetOut`, and
`SetErr` for controlled execution. Cobra's own tests execute commands by
setting all three and calling `ExecuteContext`.
([Cobra user guide][cobra-guide], [Cobra package API][cobra-api],
[Cobra command test helper][cobra-test-helper])

Use explicit constructor injection, not package-level `rootCmd`, `init`
registration, a service locator, or values hidden in `context.Context`.
Define small outbound interfaces in the package that consumes them and let
adapter packages return concrete types. This matches the Go project's advice
that interfaces generally belong on the consumer side and should be introduced
from actual use, not on the implementation side merely for mocking.
([Go interface guidance][go-interfaces])

## Recommended package structure

```text
cmd/
  regru/
    main.go                 # os/signal, concrete wiring, os.Exit only

internal/
  cli/
    root.go                 # NewRoot(Dependencies), global flags, command tree
    run.go                  # process runner, error rendering, exit-code mapping
    execute_test.go         # fresh-root execution helper
    output.go               # stable plain/JSON rendering
    errors.go               # CLI-facing error envelope and exit classes
    capabilities.go         # capabilities/status command
    billing.go              # Cobra constructors only
    cloudvps.go
    s3.go
    support.go

  app/
    capability/
      capability.go         # IDs, states, Gate, typed UnavailableError
    billing/
      balance.go            # use case, input/result, consumer-owned ports
      payment.go
    cloudvps/
    s3/
    support/

  provider/
    regapi/
      client.go             # concrete public REG.API transport
      billing.go            # app/billing port implementations
    cloudvps/
      client.go             # concrete published REST client
    s3/
      client.go             # concrete S3 data-plane adapter
    portal/
      session/
        session.go          # browser-context ownership contract
      cdp/
        controller.go       # browser/CDP lifecycle and in-browser execution
      bff/
        account.go          # private account operations + compatibility checks
        cloud.go            # private cloud operations + compatibility checks

  testkit/
    transport.go            # recording/poison RoundTripper
    execute.go              # only if external-package tests need it
```

Do not create generic `util`, `common`, `interfaces`, or one oversized
`client` package. The provider packages should be concrete implementations;
the interfaces they satisfy belong next to the use case that needs them. The
Go project explicitly recommends consumer-owned interfaces and warns against
pre-creating implementor-side interfaces for mocks.
([Go interface guidance][go-interfaces])

### Dependency direction

```text
cmd/regru
    │ constructs
    ▼
internal/cli ─────► internal/app/* ◄──── internal/provider/*
     Cobra             use cases          concrete adapters
     rendering         ports/models       wire protocols

internal/provider/portal/bff ─► internal/provider/portal/session
internal/provider/portal/cdp ──► internal/provider/portal/session
```

The important rule is not the exact directory spelling but the import
direction:

- `cli` knows command grammar, flags, output, exit behavior, and app use cases;
- `app` knows domain inputs/results, capability requirements, and the minimal
  operations needed to fulfill a use case;
- `provider` knows authentication, URLs, HTTP/S3/GraphQL schemas, browser
  execution, compatibility probes, and response decoding;
- no provider package imports Cobra or prints user output;
- no use case knows whether its port is backed by HTTP, S3, CDP, or a fake.

### Keep CDP, session, and BFF concerns separate

Private portal support must not become a general-purpose HTTP client with
exported cookies.

- `portal/session` owns the abstract lifetime of one isolated authenticated
  browser context and its selected REG.RU account/environment.
- `portal/cdp` owns browser process/context control, navigation, interactive
  login handoff, and cancellation of browser work. It does not contain account
  GraphQL documents or map provider results into CLI domain models.
- `portal/bff` owns private operation names, request/response decoding,
  manifest or schema compatibility checks, redaction, and fail-closed drift
  behavior. It receives a session-bound executor; it does not own Cobra
  commands and must not export raw cookies.

This preserves the local research boundary: the portal BFF/GraphQL surface is
private and session-lived, while REG.API, CloudVPS REST, and S3 have distinct
published or compatibility contracts. See
[`portal-session-lifecycle.md`](portal-session-lifecycle.md),
[`portal-credential-bootstrap.md`](portal-credential-bootstrap.md), and
[`capability-matrix-portal-refresh.md`](capability-matrix-portal-refresh.md).

## Constructor and interface shape

The root receives typed dependencies, not an untyped registry:

```go
type Dependencies struct {
    Billing     BillingCommands
    CloudVPS    CloudVPSCommands
    S3          S3Commands
    Support     SupportCommands
    Capabilities capability.Gate
    Clock       func() time.Time
}
```

Prefer narrower dependency values per command constructor over passing the
whole root `Dependencies` everywhere:

```go
type BalanceRunner interface {
    Run(context.Context, billing.BalanceInput) (billing.BalanceResult, error)
}

func NewBalanceCommand(run BalanceRunner, render Renderer) *cobra.Command
```

The interface is owned by the consuming `cli` or `app` package and contains
only the method that command/use case needs. Concrete adapters may expose
additional methods without widening every fake or command dependency. Small
interfaces are idiomatic in Go, but the stronger rule here is to create one
only where a real consumer requires substitution.
([Effective Go interfaces][effective-go-interfaces],
[Go interface guidance][go-interfaces])

Each `New…Command` must return a new command and new flag variables. Avoid
package-level mutable flags, singleton commands, and `init`-time
`AddCommand`. Cobra commands retain parsed flags, args, output writers,
context, parent links, and execution state; fresh construction makes tests
independent and permits multiple in-process executions.

## Command handler contract

A leaf `RunE` should do only this:

1. validate already-parsed flags and positional arguments;
2. map them to a typed application input;
3. if `--dry-run`, render a local plan and return;
4. require the declared capability using an injected, non-networked gate;
5. derive the operation timeout from `cmd.Context()`;
6. call exactly one injected use case;
7. render its result through `cmd.OutOrStdout()`;
8. return a typed error; do not print it in the handler.

Use `RunE`, because Cobra defines it as the work hook that returns an error and
documents catching that error at command execution.
([Cobra `RunE` API][cobra-rune], [Cobra error guide][cobra-errors])

Handlers and renderers must write success data only to
`cmd.OutOrStdout()` and diagnostic/progress data only to
`cmd.ErrOrStderr()`. Do not use `fmt.Print*`, `log.Print*`, `os.Stdout`, or
`os.Stderr` below the process runner. Cobra exposes separate output and error
writers and separate `SetOut`/`SetErr` injection points.
([Cobra output API][cobra-output])

Set `SilenceErrors: true` and `SilenceUsage: true` on the root and make the
process runner the single owner of final error rendering and exit-code
mapping. This prevents Cobra and the application from printing the same error
twice, and prevents runtime/provider failures from dumping usage text.
Argument/flag errors may still carry an explicit `ShowUsage` classification
that the runner handles once. Cobra documents `SilenceErrors` and
`SilenceUsage` as independent controls.
([Cobra silence controls][cobra-silence])

## Execution and test helper

Cobra's own helper demonstrates the supported pattern: create a buffer, call
`SetOut`, `SetErr`, `SetArgs`, and execute with `ExecuteContext`.
([Cobra command test helper][cobra-test-helper])

The project helper should improve on Cobra's combined buffer by preserving the
stdout/stderr contract:

```go
type Execution struct {
    Stdout string
    Stderr string
    Err    error
}

func execute(ctx context.Context, deps Dependencies, args ...string) Execution {
    root := NewRoot(deps)
    var stdout, stderr bytes.Buffer

    root.SetOut(&stdout)
    root.SetErr(&stderr)
    root.SetArgs(args)

    err := root.ExecuteContext(ctx)
    return Execution{
        Stdout: stdout.String(),
        Stderr: stderr.String(),
        Err:    err,
    }
}
```

Do not mutate `os.Args` or redirect process-global stdout/stderr in command
unit tests. Do not reuse a root between table rows. Test the process runner
separately from command execution: command tests assert returned typed errors;
runner tests assert final stderr text and exit code.

At minimum, table tests should cover:

- help and version on stdout with empty stderr;
- invalid args/flags and whether usage is shown exactly once;
- successful plain and JSON output;
- operational error: empty stdout, typed error, no duplicate text;
- fresh execution of the same command with different flags;
- capability unavailable, unverified, and supported states;
- cancelled parent context and expired operation timeout;
- `--dry-run` and unavailable capability with poison transports.

## Context and timeout contract

At process startup:

```go
signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
defer stop()

err := root.ExecuteContext(signalCtx)
```

`NotifyContext` cancels the derived context on a listed signal or parent
cancellation, and its `stop` function must be called to release resources.
([Go `signal.NotifyContext`][signal-context])

Cobra states that `ExecuteContext` installs the supplied context and that
handlers retrieve it through `cmd.Context()`.
([Cobra context API][cobra-context])

Every operation interface and adapter method should accept
`context.Context` as its first parameter. Do not store a command context in
`Dependencies`, a use case, an HTTP client, a browser session, or another
long-lived struct. The Go `context` documentation says to pass it explicitly
across the call chain and not store it in structs.
([Go context package][go-context],
[Go contexts and structs][go-context-structs])

A root persistent `--timeout` may define the maximum duration of one command
operation. Derive it inside the leaf execution path:

```go
opCtx, cancel := context.WithTimeout(cmd.Context(), timeout)
defer cancel()
```

Always call the returned cancel function; Go documents that failing to do so
retains the child and timer resources until the parent is cancelled.
([Go `WithTimeout`][go-with-timeout])

All outgoing HTTP requests must use `http.NewRequestWithContext(opCtx, …)`.
The standard library specifies that this context controls connection
acquisition, request transmission, and reading response headers and body.
([Go HTTP request context][http-request-context])

Use an injected, reused `*http.Client` with a finite hard safety timeout, but
let the per-operation context express the user-visible deadline. Go documents
that clients should be reused, are concurrency-safe, and that
`http.Client.Timeout` covers connection, redirects, and response-body reads.
([Go HTTP client][http-client])

Timeout means the local wait ended; it must not be translated into “provider
operation failed” for a mutation whose commit outcome is ambiguous. Preserve
the app-level `outcome-unknown` distinction established in
[`private-portal-resilience-contract.md`](private-portal-resilience-contract.md).

## Exact `--dry-run` guarantee

Define `--dry-run` as:

> Parse and validate local inputs, render the deterministic request/operation
> plan with secrets redacted, and make zero provider, portal, browser, DNS, or
> other network calls.

This is intentionally stronger than “do not send a mutating request.” It
makes the promise adapter-independent and mechanically testable.

Consequences:

- the dry-run branch occurs before capability probing, network-capable client
  use, browser/session acquisition, BFF manifest queries, remote validation,
  and use-case execution; constructing a passive injected value is harmless,
  but constructors are forbidden from performing any of those effects;
- the plan is derived only from flags, positional args, already-loaded local
  configuration metadata, and static command metadata;
- secret values are represented by source/presence labels, never rendered;
- a plan may say “requires `billing.change-payment-type`, current remote
  availability not checked”;
- if a useful preview needs provider reads, expose a separately named
  `plan`/`--preview` mode whose help explicitly says it may make read-only
  requests. Do not weaken `--dry-run`.

### Required proof

Inject an `http.Client` whose `Transport` is a recording/poison
`http.RoundTripper`; inject analogous poison BFF executor, S3 adapter, and CDP
session factory. Execute every dry-runnable command with `--dry-run`, then
assert:

```text
HTTP RoundTrip calls     == 0
S3 adapter calls         == 0
BFF executor calls       == 0
CDP/session acquisitions == 0
stdout contains redacted plan
stderr is empty
error is nil
```

`http.Client.Transport` is explicitly the mechanism that makes individual
requests, and `RoundTripper` is the standard one-transaction interface, so a
poison transport provides a hard boundary: any accidental HTTP dispatch fails
the test at the point of dispatch.
([Go HTTP client transport][http-client],
[Go `RoundTripper`][round-tripper])

Use `httptest.Server` for adapter contract tests that should exercise actual
HTTP serialization and decoding. The standard library defines it as a local
loopback HTTP server for end-to-end tests. Use a poison `RoundTripper` for
negative guarantees such as dry run, because “the test server received zero
requests” does not detect traffic mistakenly sent somewhere else.
([Go `httptest.Server`][httptest-server])

## Capability-gated placeholders

Command visibility, product support tier, implementation readiness, credential
readiness, and runtime availability are different facts. Do not collapse them
into a boolean or put them all in one enum.

Recommended local model:

```go
type SupportTier string
type Availability string

const (
    TierSupported    SupportTier = "supported"
    TierExperimental SupportTier = "experimental-private"
    TierUnsupported  SupportTier = "unsupported"
    TierNotBuilt     SupportTier = "not-built"
)

const (
    Available              Availability = "available"
    NotConfigured          Availability = "not-configured"
    Unverified             Availability = "unverified"
    TemporarilyUnavailable Availability = "temporarily-unavailable"
    ProviderDisabled       Availability = "provider-disabled"
)

type Status struct {
    Tier         SupportTier
    Availability Availability
    Reason       string
}
```

These are CLI model values, not REG.RU wire enums. The more detailed
capability matrices in
[`capability-matrix-regapi-refresh.md`](capability-matrix-regapi-refresh.md)
and
[`capability-matrix-cloud-s3-refresh.md`](capability-matrix-cloud-s3-refresh.md)
should remain authoritative for product-specific mappings.

Register destination-approved command names even when an operation is not yet
built or currently unavailable, so help and completion expose a stable command
contract. The leaf keeps its intended `Use`, `Short`, args, flags, examples,
output schema name, and required capability ID, but its `RunE` returns a typed
`capability.UnavailableError` before acquiring or invoking any adapter.

The error must carry:

```text
capability ID
support tier
availability
stable reason code
human remedy
whether retry may help
```

Plain output may explain the remedy; JSON output must retain stable fields.
Never substitute dummy provider data, silently open a browser, rotate/create a
credential, or reinterpret auth/transport failure as `unsupported`.

The gate rejects `not-built` and `unsupported` tiers unconditionally. An
`experimental-private` tier additionally requires the explicit project-level
opt-in chosen for experimental functionality. For an otherwise allowed tier,
the gate then evaluates availability. This keeps a status such as
`experimental-private + available` representable instead of confusing
stability with account readiness.

Recommended execution order:

```text
parse/validate
  ├─ dry-run → render local plan → return
  └─ live
       ├─ gate rejects → typed error → return (zero adapter calls)
       └─ gate allows → derive timeout → invoke use case → render
```

The injected gate must be a local snapshot. Remote capability discovery is a
separate explicit `regru capabilities refresh` operation, because hidden
probing would violate the dry-run and zero-call unavailable-placeholder
guarantees.

Use Cobra's `Hidden` field only for genuinely internal/withdrawn command
contracts, not for ordinary runtime capability gating. Hiding unavailable
commands makes scripts and documentation dependent on the current account or
machine. Cobra exposes command grouping and hidden commands, but the choice to
keep destination-approved placeholders visible is a project product-contract
decision.
([Cobra command fields][cobra-command-fields],
[Cobra command grouping][cobra-groups])

### Placeholder proof

Run every unavailable/not-built placeholder against the same poison
dependencies used for dry-run tests and assert:

```text
all adapter call counts == 0
stdout is empty
returned error matches capability.UnavailableError
capability/tier/availability/reason are stable in JSON rendering
help still documents flags and output contract
```

## Adapter test layers

### Command tests

Construct fresh roots and fake the narrow application interface. Assert
grammar, input mapping, context propagation, output, and errors. These tests
must not know HTTP paths, GraphQL operations, cookies, or S3 wire shapes.

### Use-case tests

Use consumer-side fakes to assert orchestration, capability checks, mutation
ordering, outcome classification, and redaction. Avoid Cobra entirely.

### Public HTTP adapter tests

Use `httptest.Server` to assert method, URL, headers, encoded body, response
variants, timeout/cancellation, and redaction. The server records requests and
returns fixtures. Go supplies `httptest.Server` specifically for local
end-to-end HTTP tests.
([Go `httptest.Server`][httptest-server])

### Transport-negative tests

Use a custom `RoundTripper` to:

- count or reject every dispatch;
- inspect whether the outgoing request carries the command context;
- block until `req.Context().Done()` and return that error;
- prove dry run and rejected gates issue no HTTP transaction.

`RoundTripper` is the standard interface for one HTTP transaction, and Go
requires implementations to be safe for concurrent use.
([Go `RoundTripper`][round-tripper])

### Portal/CDP/BFF tests

Keep three distinct fakes:

- session factory: counts browser-context acquisitions;
- CDP executor: records navigation/evaluation requests without BFF knowledge;
- BFF executor: records operation identifiers and redacted variables without
  browser-process knowledge.

Contract tests for private BFF operations must use pinned, redacted fixtures
and assert fail-closed behavior for unknown typename, missing field, operation
manifest mismatch, or incompatible schema. They must never use real cookies or
live credentials in normal unit tests.

## Scaffold acceptance criteria

The foundation is ready when all of the following are true:

1. `cmd/regru/main.go` is a thin composition/exit wrapper and contains no
   command definitions or provider logic.
2. `NewRoot(Dependencies)` returns a fresh command tree and all leaf commands
   use constructor-local flag variables and `RunE`.
3. Cobra handlers import neither provider adapter packages nor network/CDP/S3
   libraries.
4. Success output flows only through the Cobra stdout writer; final errors are
   rendered once by the process runner on stderr.
5. One reusable execution helper captures stdout and stderr separately and
   always calls `ExecuteContext`.
6. Context cancellation reaches the fake use case, HTTP request, and
   portal/CDP executor; timeout tests do not sleep for wall-clock-scale
   durations.
7. Every `--dry-run` test proves zero calls across HTTP, S3, BFF, and
   CDP/session dependencies.
8. Every unavailable/not-built placeholder proves zero adapter calls and
   returns a structured capability error while remaining documented in help.
9. Concrete provider packages return concrete clients; narrow interfaces are
   declared by their consuming `app`/`cli` packages.
10. No test mutates `os.Args`, redirects process-global output, contacts
    REG.RU, or depends on a developer browser/session.

## Sources

- [Cobra user guide][cobra-guide]
- [Cobra package API][cobra-api]
- [Cobra's command execution test helpers][cobra-test-helper]
- [Go Code Review Comments: contexts and interfaces][go-interfaces]
- [Effective Go: interfaces][effective-go-interfaces]
- [Go `context` package][go-context]
- [Go blog: Contexts and structs][go-context-structs]
- [Go `os/signal` package][signal-context]
- [Go `net/http` package][http-client]
- [Go `net/http/httptest` package][httptest-server]

[cobra-guide]: https://github.com/spf13/cobra/blob/main/site/content/user_guide.md
[cobra-api]: https://pkg.go.dev/github.com/spf13/cobra
[cobra-test-helper]: https://github.com/spf13/cobra/blob/main/command_test.go#L43-L86
[cobra-rune]: https://pkg.go.dev/github.com/spf13/cobra#Command
[cobra-errors]: https://github.com/spf13/cobra/blob/main/site/content/user_guide.md#returning-and-handling-errors
[cobra-output]: https://pkg.go.dev/github.com/spf13/cobra#Command.SetOut
[cobra-silence]: https://pkg.go.dev/github.com/spf13/cobra#Command
[cobra-context]: https://pkg.go.dev/github.com/spf13/cobra#Command.ExecuteContext
[cobra-command-fields]: https://pkg.go.dev/github.com/spf13/cobra#Command
[cobra-groups]: https://github.com/spf13/cobra/blob/main/site/content/user_guide.md#grouping-commands-in-help
[go-interfaces]: https://go.dev/wiki/CodeReviewComments#interfaces
[effective-go-interfaces]: https://go.dev/doc/effective_go#interfaces_and_types
[go-context]: https://pkg.go.dev/context
[go-context-structs]: https://go.dev/blog/context-and-structs
[go-with-timeout]: https://pkg.go.dev/context#WithTimeout
[signal-context]: https://pkg.go.dev/os/signal#NotifyContext
[http-request-context]: https://pkg.go.dev/net/http#NewRequestWithContext
[http-client]: https://pkg.go.dev/net/http#Client
[round-tripper]: https://pkg.go.dev/net/http#RoundTripper
[httptest-server]: https://pkg.go.dev/net/http/httptest#Server
