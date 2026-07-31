# CLI output and error contract

Research date: 2026-07-31

## Scope and evidence standard

This note recommends the public command-line contract for the `regru`
foundation in [“Task: scaffold the regru CLI contracts”][issue-3]. It covers
human, `--json`, and `--plain` output; stdout/stderr ownership; structured
success and error results; exit status; completion; help; version; and
graceful cancellation.

The sources are primary: the Cobra documentation and tagged source, the Go
standard library, POSIX, the GNU command-line conventions, the JSON
specification, Semantic Versioning, and the first-party GitHub CLI manual.
Where those sources do not establish a universal convention, the text labels
the choice as **`regru` policy** rather than presenting it as an external
standard.

## Decision

`regru` should expose three deliberately different presentation contracts:

- Human output is the default and may improve over time. It may use tables,
  headings, wrapping, and color when the relevant stream is a terminal. It is
  not a scripting API.
- `--json` is the complete, versioned automation API. A successful command
  writes exactly one JSON result to stdout. A failed command writes no stdout
  and exactly one JSON error to stderr.
- `--plain` is a narrow line-oriented projection for shell pipelines. It has
  fixed, command-specific columns, but it is not a lossless replacement for
  JSON.

The two flags are global persistent flags and are mutually exclusive. An
explicit machine mode does not change with TTY detection, config, locale,
terminal width, or color settings. GitHub CLI independently demonstrates the
useful split between default line-oriented output and an explicit JSON mode,
while also treating debug output as stderr and providing prompt/color
switches. That is precedent for the high-level separation, not a schema for
`regru`. ([GitHub CLI formatting][gh-formatting], [GitHub CLI
environment][gh-environment])

Every handler returns typed domain data or a typed error. It never prints.
One presenter owns stdout/stderr, output mode, redaction, envelope encoding,
and exit classification. This is the satisfying simplification in this
contract: provider adapters, browser code, and Cobra handlers do not need to
know which presentation was requested.

## Stream ownership

POSIX specifies stderr for diagnostics and non-zero status for an
unrecoverable error. GNU's `--help` and `--version` conventions put both
successful meta-results on stdout. Cobra's current implementation likewise
sends help to `OutOrStdout`, exposes separate configurable stdout and stderr
writers, and normally sends usage to stderr. ([POSIX utility
conventions][posix-utilities], [GNU `--help`][gnu-help], [GNU
`--version`][gnu-version], [Cobra command source][cobra-command])

**`regru` policy:**

| Event | stdout | stderr |
| --- | --- | --- |
| Successful command result | Primary result only | Human-mode warnings/progress only |
| Failed command | Empty | One rendered error; JSON mode uses the error envelope |
| Prompt, confirmation, progress, browser-launch notice | Empty | Human mode only |
| `--help`, `help <command>` | Help text | Empty |
| `--version` | One version line | Empty |
| `completion <shell>` | Raw completion script | Empty |

Machine modes never emit ANSI escapes, spinners, prompts, progress, update
notices, or browser-launch chatter. A command that explicitly starts an
interactive browser, such as `auth login`, may still do so in JSON mode
because the command itself is the explicit request; the terminal streams
remain quiet until the final result.

Handlers must write through injected writers (`cmd.OutOrStdout()` and
`cmd.ErrOrStderr()` at the boundary), never package-global `fmt.Print*`,
`log.Print*`, `os.Stdout`, or `os.Stderr`. Cobra supports inherited input,
output, and error writers, which also makes exact channel tests
straightforward. ([Cobra command source][cobra-command])

Do not write primary output incrementally. Finish the provider operation and
serialize the result before the first stdout write. On any ordinary error,
stdout must therefore be empty. A short write or broken downstream pipe can
still leave an externally partial byte stream; no CLI can retract bytes the
OS already accepted.

## Human mode

Human output may use terminal-width-aware tables and concise prose. Color is
`auto` by default: only color a stream when that stream is a TTY. Honor
`NO_COLOR` and `CLICOLOR=0`; GitHub CLI documents both conventions.
`--color=always|auto|never` may override human mode, but
`--color=always` with `--json` or `--plain` is a usage error rather than an
invitation to corrupt machine output. ([GitHub CLI environment][gh-environment])

Human failures use one concise line on stderr:

```text
regru: authentication expired; run `regru auth refresh --account prod`
```

Do not print full usage after an operational failure. For a syntax or flag
error, print the error plus a short help hint. `regru` should set Cobra's
`SilenceErrors` and `SilenceUsage` and render the one appropriate response at
the top level; Cobra exposes both controls specifically to suppress its
downstream error and usage output. ([Cobra command source][cobra-command])

## JSON mode

RFC 8259 defines a JSON object as unordered, recommends unique member names,
and requires UTF-8 for interoperable exchange. Consumers must not depend on
object-member order. `regru` should emit compact UTF-8 JSON with unique keys,
no BOM, and one trailing LF. ([RFC 8259][rfc-8259])

### Success envelope

```json
{
  "schemaVersion": "regru.cli/v1",
  "ok": true,
  "command": "vps list",
  "data": [],
  "warnings": []
}
```

### Error envelope

```json
{
  "schemaVersion": "regru.cli/v1",
  "ok": false,
  "command": "vps create",
  "error": {
    "code": "capability_unavailable",
    "message": "CloudVPS credentials are not available for account prod",
    "exitCode": 7,
    "retryable": false,
    "details": {}
  }
}
```

**`regru` policy:**

- The five success keys and four error keys shown above are always present.
  Empty collections encode as `[]` or `{}`, never `null`.
- `command` is the canonical command path without the binary name, flags, or
  user-provided operands. It must never reproduce secret-bearing argv.
- `code` is stable lowercase `snake_case` for programmatic branching.
  `message` is safe human context, not the stable identifier.
- `exitCode` exactly matches the process exit status. `retryable` says whether
  another attempt may be reasonable; `outcome_unknown` is always
  `retryable: false` because an ambiguous mutation must be reconciled rather
  than blindly repeated.
- `details` contains only documented, typed, redacted fields. Provider
  payloads, cookies, credentials, CSRF values, browser storage, opaque portal
  locators, raw request/response bodies, and stack traces never appear.
- `warnings` contains structured `{ "code": "...", "message": "..." }`
  objects. This keeps successful JSON parseable without stderr chatter.
- Provider identifiers are JSON strings even when they currently contain
  digits. Monetary quantities are decimal strings paired with an explicit
  currency. Domain timestamps use RFC 3339. These choices avoid numeric
  precision and locale ambiguity.

`schemaVersion` versions the CLI wire shape, not the provider API or binary.
Within `regru.cli/v1`, adding an optional command-specific `data`,
`details`, or warning field is compatible; renaming/removing a documented
field, changing its type, changing a documented enum's meaning, or changing
the envelope requires `regru.cli/v2`. Consumers must ignore unknown fields.
Software release versions follow Semantic Versioning independently.
([Semantic Versioning 2.0.0][semver])

JSON mode is all-or-nothing. On success stderr is empty; on failure stdout is
empty and stderr is exactly the error document. Debug output is therefore a
human-mode facility. This stronger rule is a `regru` policy, not a Cobra or
POSIX requirement, and is what makes `stdout` or `stderr` independently
decodable without scraping.

## Plain mode

`--plain` is for small shell pipelines such as extracting service IDs. Each
command documents a fixed projection and field order:

```text
<field-1>\t<field-2>\t...\n
```

**`regru` policy:**

- one logical record per LF-terminated line;
- fields separated by one ASCII TAB;
- no heading, padding, wrapping, pager, color, or terminal-width behavior;
- strings escape backslash, TAB, LF, and CR as `\\`, `\t`, `\n`, and `\r`;
- booleans are `true`/`false`, integers are base 10, timestamps are RFC 3339,
  and a missing nullable value is an empty field;
- an empty result is zero stdout bytes;
- nested or lossless data has no plain representation: document a useful
  scalar projection or require `--json`.

Column addition, removal, reordering, or semantic change is breaking. New
columns should not be appended casually; shell consumers commonly bind by
position. On failure, stdout is empty, stderr contains the human-safe error,
and the process status carries the stable broad class. A caller needing a
structured error must use `--json`.

## Exit status and error taxonomy

Go documents zero as success, non-zero as error, recommends portable exit
codes in `[0, 125]`, and warns that `os.Exit` skips deferred functions. POSIX
shells reserve 126 and 127 for “not executable” and “not found” and report
signal termination above 128. GitHub CLI uses a small stable set and assigns
2 to cancellation and 4 to authentication-required; it explicitly warns
that commands can define additional codes. These sources favor a small
taxonomy, not a provider-status-per-exit-code design. ([Go `os.Exit`][go-os],
[POSIX shell exit status][posix-shell], [GitHub CLI exit
codes][gh-exit-codes])

**Recommended global statuses:**

| Status | Meaning | Representative stable error codes |
| ---: | --- | --- |
| 0 | Success | none |
| 1 | Execution/provider failure not classified below | `provider_error`, `network_error`, `rate_limited`, `internal` |
| 2 | Graceful cancellation | `cancelled`, `login_cancelled` |
| 3 | Invalid invocation or local input | `usage`, `invalid_input`, `non_interactive` |
| 4 | Authentication or selected-principal failure | `auth_required`, `auth_expired`, `reauthentication_required`, `account_mismatch` |
| 5 | Authenticated but not permitted | `permission_denied` |
| 6 | Requested resource not found | `not_found` |
| 7 | Capability unavailable or contract unsafe | `capability_unavailable`, `browser_unavailable`, `contract_drift`, `unsupported_operation` |
| 8 | State conflict or failed precondition | `conflict`, `precondition_failed` |
| 9 | Deadline exceeded with a known non-mutating outcome | `timeout` |
| 10 | A mutation may have committed but cannot be proved | `outcome_unknown` |

Provider-specific codes remain in `error.code` and map onto these broad
statuses. Do not allocate a new process status for every HTTP, GraphQL, BFF,
or REG.API result.

A local deadline before a request is sent is `timeout`/9. A read deadline is
also 9. A mutation deadline or connection loss after delivery may have begun
is `outcome_unknown`/10, not timeout/9. That distinction preserves the
previously established no-blind-retry contract.

## Cancellation and the top-level runner

Use `signal.NotifyContext` for `os.Interrupt` and pass that context with
`ExecuteContext`; Go documents that the returned context is cancelled when
the signal, parent cancellation, or stop function occurs, and Cobra makes the
execution context available to handlers. ([Go `signal.NotifyContext`][go-signal],
[Cobra command source][cobra-command])

The first Ctrl-C or browser-login cancellation should return the typed
`cancelled` error and status 2. A deadline is status 9 unless the mutation
outcome is unknown. A second signal may force OS termination and is outside
the graceful CLI contract.

Keep `os.Exit` in a tiny `main`: a `run` function should create and stop the
signal context, execute the command, render the result, and return an integer.
Only after `run` returns—and its defers have run—should `main` call
`os.Exit(code)`. This avoids the standard library's documented
defer-skipping behavior.

## Completion contract

Cobra supports generated completions for Bash, Zsh, Fish, and PowerShell,
including command, flag, and custom value completion. Its
`ShellCompDirectiveNoFileComp` prevents irrelevant filename suggestions.
GitHub CLI provides a strong first-party example of sending a generated
completion script to stdout for those same four shells.
([Cobra shell completion guide][cobra-completion], [Cobra completion
source][cobra-completion-source], [GitHub CLI completion][gh-completion])

Expose:

```text
regru completion bash
regru completion zsh
regru completion fish
regru completion powershell
```

The selected script is raw stdout, with empty stderr and status 0. Completion
is a meta-command, not normal result data: reject `--json` or `--plain` with
it as usage status 3 rather than wrapping or corrupting the script.

Completion generation and hidden completion callbacks must not load provider
credentials, require authentication, call a network, launch a browser,
prompt, mutate, or emit diagnostics. Complete static commands/flags and safe
local enums. Account profile names may be completed only from non-secret local
configuration; never complete tokens, secret references, resource secrets, or
portal locators. Use `ShellCompDirectiveNoFileComp` unless a flag genuinely
accepts a path.

## Help and version

GNU specifies `--help` and `--version` as successful, side-effect-free
meta-actions on stdout; Cobra automatically provides help and adds a top-level
version flag when the root command's `Version` field is non-empty. Cobra also
adds a `-v` shorthand for that generated flag if `-v` is otherwise free.
([GNU `--help`][gnu-help], [GNU `--version`][gnu-version], [Cobra user
guide][cobra-user-guide], [Cobra command source][cobra-command])

**`regru` policy:**

- `regru --help`, `regru help`, and `regru help <command>` write human help
  to stdout, write no stderr, return 0, and do not read config, authenticate,
  initialize clients, or perform network/browser work.
- `regru --version` writes exactly `regru <semver>\n`, writes no stderr,
  returns 0, and has the same no-I/O guarantee. Git tags may use Go's
  conventional `v` prefix; the displayed SemVer value need not.
- Help and version are always text meta-output. If `--json` or `--plain` is
  also present, the meta-action wins and the format flag is ignored. This
  follows the established expectation that help/version ignore normal
  operation options.
- Reserve `-v` for future verbosity, not version. Define `--version`
  explicitly or otherwise prevent Cobra from auto-claiming `-v`.
- Build metadata can be embedded at link time; it must not cause config,
  credential, browser, or network access. A development build reports a
  deterministic value such as `dev`, not an empty string.

## Cobra integration boundary

The root construction should establish these invariants once:

1. Inject stdin/stdout/stderr and the signal-aware context.
2. Parse and validate global mode flags before constructing service clients.
3. Set `SilenceErrors` and `SilenceUsage`.
4. Let `RunE` handlers return typed data/errors without rendering.
5. Map one typed error to one stable code/status at the root.
6. Redact, serialize fully, then perform the first output write.

Cobra's `SetOut`, `SetErr`, `SetIn`, `ExecuteContext`, version template, help
template, and silence controls are enough; no handler needs global process
state. ([Cobra command source][cobra-command])

## Minimum contract tests

Issue #3 should freeze at least these observable cases:

- default human, `--json`, and `--plain` success with exact stdout/stderr;
- conflicting output flags fail before handler/client construction;
- JSON success/error decode, carry `regru.cli/v1`, end in one LF, and contain
  no ANSI or secret test sentinel;
- every exit-status row maps to representative typed errors, especially
  auth expiry, account mismatch, browser unavailable, contract drift,
  cancellation, timeout, and outcome unknown;
- operational errors do not print usage; syntax errors print only the intended
  error/hint;
- help/version work without config and with injected failing client factories;
- completion scripts generate for all four shells and generation invokes no
  client, browser, prompt, or credential path;
- first cancellation reaches a blocking handler through `cmd.Context()` and
  resolves to status 2;
- a serialization failure and an ordinary provider failure leave stdout
  empty.

[issue-3]: https://github.com/adinvadim/reg-ru-cli/issues/3
[cobra-command]: https://github.com/spf13/cobra/blob/v1.10.2/command.go
[cobra-completion]: https://cobra.dev/docs/how-to-guides/shell-completion/
[cobra-completion-source]: https://github.com/spf13/cobra/blob/v1.10.2/completions.go
[cobra-user-guide]: https://github.com/spf13/cobra/blob/v1.10.2/site/content/user_guide.md
[gh-completion]: https://cli.github.com/manual/gh_completion
[gh-environment]: https://cli.github.com/manual/gh_help_environment
[gh-exit-codes]: https://cli.github.com/manual/gh_help_exit-codes
[gh-formatting]: https://cli.github.com/manual/gh_help_formatting
[go-os]: https://pkg.go.dev/os#Exit
[go-signal]: https://pkg.go.dev/os/signal#NotifyContext
[gnu-help]: https://www.gnu.org/prep/standards/html_node/_002d_002dhelp.html
[gnu-version]: https://www.gnu.org/prep/standards/html_node/_002d_002dversion.html
[posix-shell]: https://pubs.opengroup.org/onlinepubs/9699919799/utilities/V3_chap02.html#tag_18_08
[posix-utilities]: https://pubs.opengroup.org/onlinepubs/9699919799.2016edition/utilities/V3_chap01.html
[rfc-8259]: https://www.rfc-editor.org/rfc/rfc8259.html
[semver]: https://semver.org/spec/v2.0.0.html
