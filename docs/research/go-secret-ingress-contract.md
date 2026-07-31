# Go CLI secret-ingress contract

Research date: 2026-07-31

## Scope and evidence

This note defines a provider-neutral Go API for receiving one login, password,
token, or other sensitive value through standard input or an already-open file
descriptor. It does not define secret storage, invoke a secret manager, or use
real credentials.

The sources are the Go specification, standard-library documentation and
source, and the Go project's `x/term` and `x/sys` packages. Numeric limits and
timeout values below are product policy, not limits imposed by Go.

## Decision

Treat non-interactive secret ingress as a small, byte-oriented protocol, not as
ordinary text input:

1. One source carries exactly one value and must reach EOF. Do not put two
   credentials into two lines of the same stream.
2. Accept only a regular file or pipe. Refuse a terminal before the first
   `Read`; interactive no-echo prompting is a separate API and an explicit
   user interaction.
3. Read into one fixed allocation, with a normalized-value limit of 64 KiB.
   Never use an unbounded `io.ReadAll`.
4. Remove at most one final LF or CRLF. Preserve every other byte, then reject
   empty, NUL-containing, or multiline input.
5. Give every read a finite budget, propagate cancellation, and return no
   partial value on any failure.
6. Keep the value as mutable bytes behind a redacting type. Never convert it to
   `string`, an error message, log attribute, JSON value, command argument, or
   environment variable.
7. Zero owned Go buffers on failure and explicit destruction, while promising
   only best-effort reduction of lifetime—not secure erasure of every copy in
   the process or OS.

`io.Reader` permits a call to return both bytes and an error, so the read loop
must process `n > 0` before interpreting `err`. `io.ReadAll` reads until EOF,
and `io.LimitReader` synthesizes EOF after its byte budget; either can support a
correct cap, but a fixed buffer avoids growth reallocations that can leave
additional secret-bearing arrays for the garbage collector.
([`io.Reader`, `ReadAll`, and `LimitReader`][go-io])

## Suggested package surface

```go
const (
    MaxValueBytes = 64 << 10
    DefaultReadTimeout = 60 * time.Second
    MaxReadTimeout = 5 * time.Minute
)

type Source struct { /* unexported: stdin or borrowed Unix fd */ }

func Stdin(f *os.File) Source
func FileDescriptor(fd int) Source // supported on Unix; typed unsupported error on Windows

func ReadOne(ctx context.Context, src Source, timeout time.Duration) (*Value, error)

type Value struct { /* unexported mutable storage */ }
func (v *Value) Bytes() []byte // borrowed view, valid only until Destroy
func (v *Value) Destroy()      // idempotent; zeroes the full owned allocation
```

`ReadOne` derives a child deadline equal to the earlier of the caller's
deadline and `now + timeout`. Zero selects the 60-second default; negative or
greater than five minutes is invalid. The CLI may choose a stricter timeout,
but there is no unbounded mode. Go contexts carry cancellation and deadlines,
but canceling a context only closes `Done`; it does not by itself interrupt an
arbitrary blocking `Read`. The source adapter therefore has to participate in
cancellation. ([Go `context`][go-context], [`os.File` deadlines][go-os-file])

`Source` is deliberately constructed rather than accepting a naked
`io.Reader`:

- it can duplicate a borrowed descriptor/handle and own the duplicate;
- it can perform a real terminal check;
- it can close only its own resource on cancellation;
- it can reject source types whose blocking behavior cannot meet the
  contract.

An internal test seam may accept an `io.ReadCloser`, but the public
OS-facing path should retain the stronger ownership rules. Do not call
`os.NewFile` directly on a caller-owned descriptor and then close it:
`NewFile` takes responsibility for a descriptor that can later be invalidated
by `Close` or finalization. ([`os.NewFile` and `File.Fd`][go-os-file])

### Value safety

`Value` should keep both the normalized length and the full allocation so
`Destroy` can clear bytes removed during normalization as well as the returned
value. `Bytes` should return `storage[:n:n]` to prevent an append from
overwriting hidden capacity, and its documentation must say that callers may
not retain it beyond `Destroy`.

Implement `fmt.Formatter` on the value type so every formatting verb and flag
produces `[REDACTED]`; `Stringer` alone does not cover every formatting path.
Implement `slog.LogValuer` with a redacted constant, and make JSON/text/binary
marshaling fail with a static error. These are defense in depth: code still
must not pass a `Value` to logging or presentation APIs. Go's formatting rules
give `Formatter` precedence, and `slog` explicitly documents `LogValuer` as a
redaction hook. ([Go `fmt`][go-fmt], [Go `slog`][go-slog])

Use static typed errors whose `Error` methods contain only a safe operation and
code:

| Stable code | Condition |
| --- | --- |
| `secret_source_terminal` | source is a terminal |
| `secret_source_unsupported` | unsupported descriptor kind or Windows fd mode |
| `secret_input_empty` | zero bytes after final terminator removal |
| `secret_input_too_large` | normalized value would exceed 64 KiB |
| `secret_input_multiline` | CR or LF remains after normalization |
| `secret_input_nul` | any NUL byte is present |
| `secret_input_cancelled` | parent context canceled |
| `secret_input_timeout` | read budget expired |
| `secret_input_read_failed` | other read failure |

Errors must never retain the input buffer or format a partial value. A wrapped
OS error is acceptable only if its type is known not to contain read data; the
public `Error()` remains static. `errors.Is` can still expose cancellation and
deadline identity without embedding sensitive material in text.
([Go `errors.Is`][go-errors])

## Wire format and bounded read

Let `M = 65536`. Allocate exactly `M + 3` bytes: `M` possible value bytes, up
to two terminator bytes, and one overflow-detection byte. Read until either EOF
or the buffer fills:

- if `M + 3` bytes arrive, fail immediately as `secret_input_too_large`,
  close the owned source, and do not drain it;
- on a read error after any bytes, clear the complete allocation and return a
  read/cancellation error, never the partial value;
- on EOF, remove exactly one terminal `\n`; if the new final byte is `\r`,
  remove that too;
- reject if the normalized value is empty, longer than `M`, contains NUL, or
  contains any remaining CR or LF;
- otherwise transfer ownership of the allocation to `Value`.

This accepts both `printf`-style producers (no newline) and conventional
single-line producers (LF or CRLF) without applying `TrimSpace`. Spaces, tabs,
non-ASCII bytes, and invalid UTF-8 are preserved. Encoding requirements belong
to the downstream protocol, not to ingress. Bare final CR is rejected rather
than guessed to be a terminator.

NUL rejection is intentional even though Go strings can contain NUL: the value
will commonly cross text, shell, OS, or foreign-function boundaries where NUL
semantics differ. Rejecting it at ingress keeps the one-value contract
unambiguous.

Do not use `bufio.Scanner` as the contract boundary. Its token-oriented split
semantics and configurable token limit are unnecessary here; the protocol is
the complete EOF-delimited stream, with one explicitly normalized final line
ending.

## TTY and interactive separation

Use `term.IsTerminal(int(f.Fd()))`, not `FileInfo.Mode()&os.ModeCharDevice`.
A character device is not necessarily a terminal. The Go `x/term` package
defines `IsTerminal` for this purpose and notes that `os.Stdin.Fd()` need not
be zero on non-Unix systems. ([Go `x/term`][go-term])

Rules:

- `--...-stdin` or `--...-fd` is always non-interactive. If its source is a
  terminal, return `secret_source_terminal` before reading or prompting.
- An interactive fallback is allowed only when the user did not select a
  non-interactive source, command policy allows input, and the relevant
  terminal is present. It uses a separate function based on
  `term.ReadPassword`, which reads without local echo and omits the newline.
- A malformed, empty, timed-out, or canceled non-interactive source never falls
  back to a prompt.
- Never silently open `/dev/tty` or a console merely because redirected stdin
  failed.

This distinction prevents an automation error from turning into a hanging
prompt and prevents a user from typing a secret into an ordinary echoed stdin
reader.

## Cancellation and descriptor ownership

The adapter duplicates the selected OS resource before reading, marks the
duplicate close-on-exec, and closes it on every exit. It sets the context
deadline with `SetReadDeadline` where supported and arranges for cancellation
to set an immediate deadline or close the owned duplicate. Stop and join any
cancellation helper before returning; do not leave a goroutine blocked in
`Read`.

`File.SetReadDeadline` applies to pending reads, but Go documents that only
some file kinds support it: ordinary files usually do not, while pipes
usually do. `File.Close` is publicly guaranteed to cancel pending I/O only for
files that support deadlines. Therefore:

- pipes must use a pollable/cancelable platform adapter and pass a blocking
  cancellation test;
- ordinary regular files may be read synchronously with context checks before
  and after each read, but Go cannot guarantee prompt cancellation of a kernel
  or filesystem stall;
- non-terminal character devices and unknown descriptor kinds are refused,
  because a hard read bound cannot be promised;
- on any I/O error, inspect `ctx.Err()` first so close/deadline artifacts are
  mapped to `secret_input_cancelled` or `secret_input_timeout`.

The current Go Unix `internal/poll` implementation can evict pending pollable
I/O on close, but explicitly does not wait for an operation on a blocking fd.
The current Windows implementation calls `CancelIoEx` when closing a pipe.
Those source facts justify platform tests; they are not a substitute for the
narrower public `os.File` guarantee.
([Unix poll source][go-poll-unix], [Windows poll source][go-poll-windows])

## Unix file descriptors and Windows handles

The CLI contract should be:

| Platform | stdin | additional numeric fd |
| --- | --- | --- |
| Unix-like | Supported | Supported for `fd >= 3` through a duplicated, close-on-exec descriptor |
| Windows | Supported | Not supported in v1; return `secret_source_unsupported` |

On Unix, duplicate the selected descriptor with `x/sys/unix`, immediately set
close-on-exec, and wrap only the duplicate in `os.File`. Resolve and duplicate
the fd before starting other goroutines so numeric descriptor reuse cannot
change the target. Keep stdin as the explicit stdin source rather than spelling
it as fd 0.

Duplication alone does not make an inherited Unix descriptor deadline-aware:
`os.NewFile` only attempts poller integration when that descriptor is already
non-blocking. The platform adapter must deliberately provide cancelable reads
(for example, readiness polling with a context wake-up, or carefully managed
non-blocking mode) and prove them in integration tests; it must not assume that
wrapping a duplicated blocking fd makes `SetReadDeadline` work.
([`os.NewFile`][go-os-file])

On Windows, `os.File.Fd` is an OS handle, not a small POSIX fd, and
`os/exec.Cmd.ExtraFiles` is explicitly unsupported. Go can duplicate Windows
handles with `DuplicateHandle`, but exposing a decimal `--...-fd` would be a
misleading and unsafe cross-platform promise: handle width, inheritance,
pollability, and cancellation differ. A future `--...-handle` protocol would
need an explicit inheritable-handle launcher and Windows-specific integration
tests; it should not be inferred from the Unix flag.
([Go `os/exec.Cmd.ExtraFiles`][go-exec], [Go `x/sys/unix`][go-unix],
[Go `x/sys/windows`][go-windows], [`os.File.Fd`][go-os-file])

For shell integration, one stdin producer is portable:

```text
producer | regru command --value-stdin
```

On Unix, multiple values require separate inherited descriptors, one value per
descriptor:

```text
regru command --login-fd 3 --password-fd 4 \
  3< <(login-producer) 4< <(password-producer)
```

The CLI never launches these producers, parses shell syntax, or accepts their
output through command substitution. The producer must write only the value to
its stdout, close it, and keep diagnostics on stderr. Operators must disable
shell tracing around the invocation. This protocol passes bytes through pipes;
it does not place them in `argv`, environment variables, command text, or CLI
output.

## Zeroing: useful but limited

The Go specification guarantees that `clear(s)` sets slice elements through
`len(s)` to their zero value. It does not guarantee process-wide secure
erasure, pinning, swap exclusion, or removal of copies previously made by the
runtime, compiler, kernel pipe buffers, terminal, producer, downstream client,
crash dump, or foreign library. ([Go specification: `clear`][go-clear])

Accordingly:

- allocate one fixed scratch buffer and validate by indexing/slicing in place;
- avoid `string(value)`, `bytes.Trim*` copies, `fmt`, JSON, reflection, and
  `append` into unknown-capacity destinations;
- clear the full allocation on every failure and in `Value.Destroy`;
- require the immediate consumer to avoid retaining `Bytes()` and to destroy
  the value as soon as the provider request has copied or consumed it;
- do not describe `Destroy` as guaranteed secure memory wiping.

The sharp point is lifetime reduction, not a cryptographic erasure claim.

## Test matrix

Use synthetic sentinels only; assertions must never print the test value on
failure.

| Area | Cases and required result |
| --- | --- |
| EOF framing | `abc`, `abc\n`, and `abc\r\n` all yield `abc`; reads fragmented one byte at a time behave identically |
| Preservation | leading/trailing space, TAB, non-ASCII, and invalid UTF-8 are preserved byte-for-byte |
| Empty | zero bytes, `\n`, and `\r\n` return `secret_input_empty` |
| Multiple values | `a\nb`, `a\rb`, `a\n\n`, and bare final CR return `secret_input_multiline` |
| NUL | NUL at the start, middle, and end returns `secret_input_nul` |
| Boundaries | exactly `M`, `M+LF`, and `M+CRLF` accepted; normalized `M+1` and any arrival of `M+3` raw bytes rejected |
| Overflow behavior | an infinite/large writer is not drained; reader closes after the overflow byte and writer observes closure |
| Reader semantics | custom reader returning `(n>0, io.EOF)` succeeds; `(n>0, other error)` clears and returns no partial value; repeated `(0, nil)` becomes a static no-progress/read error |
| TTY | pseudo-terminal/Windows console is refused before the first read; pipe and regular file are accepted; non-TTY character device is unsupported |
| Cancellation | already-canceled context performs no read; cancellation of a blocked pipe returns promptly, closes only the owned duplicate, and leaves no helper goroutine |
| Timeout | silent open pipe returns `secret_input_timeout` within tolerance; completion just before deadline succeeds; no unbounded timeout is accepted |
| Ownership | original stdin/fd remains open; duplicate closes once on success, parse failure, cancellation, and timeout; forced fd-number reuse cannot change a claimed source |
| Failure hygiene | after every failure the entire fixed allocation is zero; returned error text and structured fields contain neither full nor partial sentinels |
| Value hygiene | every `fmt` verb/flag and `slog` rendering is `[REDACTED]`; JSON/text/binary marshaling fails statically; `Destroy` zeroes normalized and removed-terminator bytes and is idempotent |
| Unix integration | child process passes synthetic values on fd 3 and fd 4; stdin plus extra fd works; fd 0/1/2 and negative/overflow flag values are rejected by source parsing |
| Windows integration | redirected stdin pipe succeeds; console stdin is refused; timeout/cancel of a blocked pipe is bounded; numeric fd source returns unsupported |
| Portability | unit tests and `go test -race` run on native OS; build/test at least `linux`, `darwin`, and `windows` adapters in CI |

The Windows blocked-pipe cancellation test is release-critical because the
public `os.File.Close` guarantee is conditional on deadline support, while the
current implementation has additional pipe-specific behavior. If that test
cannot be made reliable, Windows stdin ingress must document cancellation as
best effort or move to a deliberately created overlapped named-pipe protocol;
it must not silently claim the Unix guarantee.

[go-io]: https://pkg.go.dev/io#Reader
[go-context]: https://pkg.go.dev/context
[go-os-file]: https://pkg.go.dev/os#File
[go-term]: https://pkg.go.dev/golang.org/x/term
[go-fmt]: https://pkg.go.dev/fmt
[go-slog]: https://pkg.go.dev/log/slog
[go-errors]: https://pkg.go.dev/errors#Is
[go-clear]: https://go.dev/ref/spec#Clear
[go-poll-unix]: https://go.dev/src/internal/poll/fd_unix.go
[go-poll-windows]: https://go.dev/src/internal/poll/fd_windows.go
[go-exec]: https://pkg.go.dev/os/exec#Cmd
[go-unix]: https://pkg.go.dev/golang.org/x/sys/unix
[go-windows]: https://pkg.go.dev/golang.org/x/sys/windows
