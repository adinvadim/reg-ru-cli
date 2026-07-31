# Provider-neutral secret transport into `regru`

Research date: 2026-07-31

## Scope and evidence standard

This note decides how an external process can supply login names, passwords,
tokens, and similar credential fields to `regru` without putting their values
in command arguments, ordinary configuration, environment variables, terminal
output, or logs. The external process and its method of obtaining the values
are deliberately outside the contract.

The sources are primary specifications and first-party platform documentation:
POSIX, GNU Bash, Apple security guidance, Microsoft Win32 documentation, Linux
kernel-interface documentation, and the Go standard library. No real
credential, process environment, or secret store was read during this
research.

No local IPC mechanism protects a process from an administrator, a same-user
debugger with sufficient rights, a compromised producer or consumer, process
memory capture, or a crash dump. Here, “does not expose” means that the value
is not deliberately copied into `argv`, the process environment, a named
filesystem object, stdout/stderr visible to the user, or application logs.
The value necessarily exists briefly in the producer, the kernel pipe buffer,
and `regru` memory.

## Decision

Use one explicit, versioned **credential envelope on standard input** as the
only portable secret-ingress contract:

```text
credential-source --format regru.secret-input/v1 |
  regru <credential-ingest-command> --credentials-stdin
```

The producer's stdout is connected directly to `regru`'s stdin by an anonymous
pipe; it is not the terminal or a log destination. `regru`'s stdout and stderr
remain its normal result and diagnostic streams. Apple explicitly recommends
a pipe or standard input instead of a command line for passwords and warns
that environment variables may be readable by other processes. POSIX shells
inherit standard descriptors as modified by redirections, and Windows supports
the same model through inheritable anonymous-pipe handles assigned as a child
process's standard input. ([Apple secure tool invocation][apple-tools],
[Bash execution environment][bash-execution],
[Windows redirected child I/O][windows-redirected-io])

Do not accept credential values through:

- flags, positional arguments, or a command string;
- secret-valued environment variables;
- normal config files or profile metadata;
- a pathname supplied with `--credentials-file`;
- a named-pipe pathname;
- shell here-strings, command substitution into a variable, or `echo`/`printf`
  of a shell variable.

An inherited descriptor can be a later POSIX-only convenience, for example
`--credentials-fd 3`, but it must consume exactly the same envelope and must
not replace stdin as the cross-platform contract. Go's `ExtraFiles` maps
additional inherited files to descriptors `3+i` and is explicitly unsupported
on Windows. ([Go `os/exec`][go-exec])

## Credential envelope

Transport one UTF-8 JSON object followed by EOF:

```json
{
  "schemaVersion": "regru.secret-input/v1",
  "fields": {
    "login": "<secret>",
    "password": "<secret>"
  }
}
```

`fields` is a transport map, not a promise that every command accepts arbitrary
credentials. The selected `regru` command defines the exact required and
allowed field names—for example, a two-field login/password credential or a
single opaque token. Treat every value, including a login or access-key
identifier, as secret for output and logging purposes. Non-secret routing
information such as the local account alias, credential kind, and destination
belongs in flags or ordinary configuration, not in this envelope.

The reader contract should be intentionally strict:

1. `--credentials-stdin` is explicit and mutually exclusive with interactive
   credential entry and any future descriptor mode.
2. Read at most 64 KiB plus one detection byte and require EOF before any
   persistent or provider side effect. Reject an oversized, empty, partial, or
   trailing payload.
3. Require UTF-8, the exact schema version, exactly one `fields` object, no
   duplicate JSON keys, the command's exact field set, and string values.
   Reject unknown root keys and unknown credential fields.
4. Preserve field contents exactly as decoded. Do not trim whitespace or a
   final newline from a credential value; JSON framing already separates data
   from transport EOF.
5. Return only a stable error such as `invalid_secret_input`. An error may name
   a missing/unsupported field, but must not include input bytes, JSON excerpts,
   values, hashes, prefixes, lengths, or a reconstructed envelope.
6. Do not prompt on stdin in this mode. Any required mutation confirmation must
   already be satisfied by the command's non-secret automation contract;
   `regru` must not reopen `/dev/tty` behind the caller's back.
7. Close the ingress stream as soon as parsing finishes. Keep credential data
   out of global state, error values, tracing attributes, request/response
   dumps, telemetry, and spawned child processes.

JSON is recommended over newline-separated values because logins and passwords
can contain whitespace or newlines and because a versioned object can represent
different credential shapes without source-specific flags. The producer must
generate JSON with a real encoder; shell interpolation is not a safe serializer.

The 64 KiB limit is a local product choice, not an operating-system limit.
Pipes are bounded byte streams: on Linux, a full pipe blocks writers and
applications must not rely on a particular capacity. Reading concurrently and
imposing an application limit avoids both deadlock and unbounded allocation.
([Linux pipes][linux-pipe])

## Comparison of transport mechanisms

| Mechanism | Exposure and lifecycle | Portability | Decision |
| --- | --- | --- | --- |
| Anonymous pipe to stdin | No pathname and no at-rest file; bytes live transiently in producer/consumer memory and a kernel pipe buffer. Descriptor lifetime bounds access. | Native on POSIX, macOS, Linux, and Windows; directly modeled by Go `Cmd.Stdin`. | **Canonical contract** |
| Additional inherited FD / `/dev/fd` | Same useful data path as an anonymous pipe when the FD is already inherited. The numeric FD or `/dev/fd/N` locator is not secret. Lifetime and accidental inheritance require care. | POSIX/Bash convenience; Go `ExtraFiles` does not support Windows. Bash process substitution may use `/dev/fd` **or a FIFO**. | Optional POSIX extension only |
| Named pipe / FIFO | Payload is normally kernel-buffered rather than stored as file contents, but a discoverable name creates an authorization and peer-authentication problem; opens may block and an unintended process can race to connect if permissions are wrong. | POSIX FIFOs and Windows named pipes have different naming and access-control models. Windows named pipes may be network-accessible and default ACLs are too broad for secrets. | Do not expose as a `regru` input contract |
| Environment variable | Copied into process environments and commonly inherited by descendants. Linux exposes the initial environment through `/proc/<pid>/environ` subject to ptrace access checks; Apple warns it may be visible to other processes. | Available everywhere, which also makes accidental propagation easy. | Reject secret-valued environment variables |
| Temporary file | Secret exists at rest, may survive a crash, and introduces permissions, cleanup, link/race, backup, indexing, and deletion-residual concerns. A path API invites long-lived plaintext credentials. | Safe permission semantics are platform-specific; Go's Unix mode bits do not implement Windows ACL isolation. | No `--credentials-file`; only an out-of-contract last resort owned by the producer |

### Command arguments and shell expansion

Arguments are a control plane, not a secret channel. Linux documents the
complete command line in `/proc/<pid>/cmdline`; Windows `CreateProcessW`
receives a command-line string; and Apple's secure-coding guidance explicitly
says not to pass sensitive data on the command line.
([Linux process command line][linux-proc-cmdline],
[Windows `CreateProcessW`][windows-create-process],
[Apple secure tool invocation][apple-tools])

Do not replace a direct pipeline with a here-string such as
`<<< "$(credential-source)"`. Bash command substitution first captures stdout
as a shell expansion and removes trailing newlines, while a here-string then
feeds the resulting shell word to stdin. Besides retaining another copy in the
shell, that changes the bytes and makes tracing/debug output more likely to
render the value. A direct `producer | regru ... --credentials-stdin` preserves
streaming and framing. ([Bash command substitution][bash-command-substitution],
[Bash here strings][bash-here-strings])

### Standard input and anonymous pipes

POSIX specifies standard descriptor 0 as the conventional readable standard
input and preserves open process attributes across `exec` except where
close-on-exec applies. Bash says an invoked command inherits the shell's file
descriptors as modified by redirection. ([POSIX `exec`][posix-exec],
[Bash execution environment][bash-execution])

On Windows, `CreatePipe` returns anonymous-pipe handles and lets the creator
control inheritance. Microsoft's redirected-child example assigns the read
handle to the child's standard input in `STARTUPINFO`, clears inheritance on
the parent's opposite handle, and closes unused handles so EOF is observable.
The pipe object is deleted after its last handle closes.
([Windows `CreatePipe`][windows-create-pipe],
[Windows redirected child I/O][windows-redirected-io])

These properties make stdin the narrowest shared primitive. It does not make
the producer safe: if the producer itself prints the value to its stderr,
debug log, transcript, or terminal before writing the pipe, `regru` cannot
undo that disclosure.

A pipeline also does not tell `regru` the producer's exit status. `regru`
should validate a complete envelope and EOF before committing anything, and a
Bash automation wrapper should enable `set -o pipefail` so the overall script
reports a producer failure. Bash documents that, without `pipefail`, a
pipeline's status is normally the last command's status. Even with
`pipefail`, a producer must follow the contract “emit a complete envelope only
on success”; the downstream process cannot retroactively undo a valid envelope
merely because the upstream process later exits nonzero.
([Apple shell-pipeline guidance][apple-shell-security])

### Additional file descriptors and `/dev/fd`

An extra inherited descriptor is useful when stdin must carry unrelated data:

```text
regru <credential-ingest-command> --credentials-fd 3 3< <(
  credential-source --format regru.secret-input/v1
)
```

Only the descriptor number appears in `argv`. Bash supports descriptor
duplication and treats `/dev/fd/N` as an existing descriptor, but its process
substitution is implemented using either `/dev/fd` or a named FIFO depending
on the platform. This syntax is therefore a Bash/POSIX convenience, not a
portable security primitive by itself. ([Bash redirections][bash-redirections],
[Bash process substitution][bash-process-substitution])

If implemented, `--credentials-fd` should be build-tagged/documented for Unix,
accept only a bounded nonnegative descriptor number, reject `0`, `1`, and `2`,
read the same strict envelope, close the descriptor after use, and ensure it
is not inherited by later children. Prefer a descriptor already opened by the
parent; do not turn the feature into `--credentials-path /dev/fd/3`.

### Named pipes

A POSIX FIFO has a filesystem entry but stores exchanged data internally in
the kernel. It normally blocks opening until both reader and writer exist.
That pathname lets unrelated processes attempt to open the endpoint, so a
caller must create it atomically inside a private directory, set restrictive
permissions before use, verify peers where possible, handle blocking and
timeouts, then unlink it. ([Linux FIFOs][linux-fifo])

Windows named pipes require a security descriptor. Microsoft's documentation
says the default descriptor gives read access to Everyone and anonymous users,
and separately warns that named pipes can be reachable remotely unless access
is denied to network users. A correctly restricted DACL and local-only naming
can make them usable for a bespoke IPC protocol, but that is much more
platform-specific machinery than inherited stdin.
([Windows named-pipe security][windows-named-pipe-security],
[Windows named pipes][windows-named-pipes])

Because a named pipe needs naming, access control, connection authentication,
and timeout rules on every platform, accepting an arbitrary named-pipe path
would expand `regru`'s attack surface without improving the common pipeline
case.

### Environment variables

Environment variables are configuration, not a secret transport. POSIX
`execve` receives an explicit environment array; Go's `Cmd.Env` is a list of
`key=value` strings and defaults to the current process environment. On
Windows, `CreateProcessW` likewise takes an environment block and the new
process receives a copy. ([POSIX `exec`][posix-exec],
[Go `os/exec`][go-exec],
[Windows `CreateProcessW`][windows-create-process])

That copy is easy to propagate into children and diagnostic tooling. Linux
documents the initial environment in `/proc/<pid>/environ`, protected by a
ptrace access check, while Apple explicitly says environment variables may be
read by other processes. This is not necessarily public to every local user
on every hardened system, but it is a broader and longer-lived disclosure
surface than a deliberately inherited pipe.
([Linux process environment][linux-proc-environ],
[Apple secure tool invocation][apple-tools])

`regru` should not define `REGRU_PASSWORD`, `REGRU_TOKEN`, or an equivalent
secret-valued variable. A non-secret selector such as an account alias may
remain in the environment under the ordinary CLI precedence contract.

### Temporary files

Temporary files trade IPC simplicity for secret material at rest. Go's
`os.CreateTemp` creates a unique file with Unix mode `0600` before umask and
requires the caller to remove it, but Go also documents that on Windows only
the owner-write bit of `FileMode` affects the read-only attribute; the Unix
permission bits do not establish a private Windows ACL. The default temporary
directory is not guaranteed to exist or have accessible permissions.
([Go temporary files][go-os],
[Go cross-platform file modes][go-chmod])

Apple recommends avoiding sensitive temporary files where possible and
documents creation/link races, restrictive private directories, immediate
cleanup, and the risk of a crash or cleanup helper invalidating assumptions.
([Apple shell security][apple-shell-security])

Therefore `regru` should not accept a credential-file pathname. If a caller
that cannot stream is forced to stage a file, that is the caller's explicit
out-of-contract responsibility: it must use a platform-native private
directory/ACL, atomic exclusive creation, the shortest possible lifetime, and
best-effort cleanup on every exit path. Deleting a file is not a portable
guarantee that storage media, snapshots, backups, crash artifacts, or indexes
retain no copy.

## Go implementation guidance

For `regru` itself:

```go
// The command layer supplies os.Stdin only after --credentials-stdin was
// explicitly selected. The secret reader owns limits, strict decoding, and
// redaction; ordinary command handlers never log or format raw input.
err := ingestCredentials(ctx, io.LimitReader(os.Stdin, maxEnvelopeBytes+1))
```

Use `[]byte` for bounded ingress and overwrite owned buffers on all exits as a
best effort. Avoid converting the entire envelope to a Go `string`, including
it in an `error`, or retaining it beyond the request. This does not promise
cryptographic memory erasure: Go strings are immutable, decoders and HTTP
libraries may allocate copies, and the garbage collector controls object
lifetime. The enforceable contract is non-propagation to arguments,
environment, files, output, logs, telemetry, and children.

When a Go program launches `regru`, connect a reader to `Cmd.Stdin` or use
`StdinPipe`; do not append credential values to `Args` or `Env`. Go documents
that a non-file `Cmd.Stdin` is copied to the child over a pipe and that
`os/exec` does not invoke a shell unless the caller explicitly launches one.
([Go `os/exec`][go-exec])

Operational requirements:

- read, validate, and close the secret stream before spawning any other
  process;
- never log the `Cmd`, envelope, HTTP authorization/header/form body, or raw
  parse error;
- keep stdout machine-readable and stderr diagnostic-only, with neither
  containing credential values;
- honor context cancellation and a finite read deadline where the underlying
  pipe API supports it; otherwise ensure the producer/process lifetime is
  bounded by the caller;
- make persistent credential storage a separate adapter decision. The ingress
  contract ends after it hands typed fields to that adapter and says nothing
  about how the external producer obtained them.

## Platform result

- **macOS:** canonical Bash/zsh pipeline to stdin works. Apple's own security
  guidance prefers pipes/stdin over command-line or environment transport.
  Extra-FD mode is feasible but shell-specific.
- **Linux:** canonical pipeline works. `/proc` makes `argv` and the initial
  environment inspectable subject to system permissions, reinforcing the
  decision not to use either. Extra-FD mode is feasible.
- **Windows:** native anonymous pipes can be inherited as child standard
  input, and Go's `Cmd.Stdin` abstracts that mechanism. Do not require
  `/dev/fd`, Unix permission bits, or Go `ExtraFiles`. A calling shell or
  program must emit UTF-8 JSON bytes; its own encoding and logging behavior is
  outside `regru`.

The satisfying simplification is that `regru` needs no source-specific
integration at all: one bounded byte stream, one strict envelope, one redaction
boundary. Source adapters can evolve independently and pipe the same contract.

[apple-tools]: https://developer.apple.com/library/archive/documentation/Security/Conceptual/SecureCodingGuide/SecurityDevelopmentChecklists/SecurityDevelopmentChecklists.html
[apple-shell-security]: https://developer.apple.com/library/archive/documentation/OpenSource/Conceptual/ShellScripting/ShellScriptSecurity/ShellScriptSecurity.html
[bash-execution]: https://www.gnu.org/software/bash/manual/bash.html#Command-Execution-Environment
[bash-command-substitution]: https://www.gnu.org/software/bash/manual/bash.html#Command-Substitution
[bash-here-strings]: https://www.gnu.org/software/bash/manual/bash.html#Here-Strings
[bash-process-substitution]: https://www.gnu.org/software/bash/manual/html_node/Process-Substitution.html
[bash-redirections]: https://www.gnu.org/software/bash/manual/html_node/Redirections.html
[go-chmod]: https://pkg.go.dev/os#Chmod
[go-exec]: https://pkg.go.dev/os/exec
[go-os]: https://pkg.go.dev/os#CreateTemp
[linux-fifo]: https://man7.org/linux/man-pages/man7/fifo.7.html
[linux-pipe]: https://man7.org/linux/man-pages/man7/pipe.7.html
[linux-proc-cmdline]: https://man7.org/linux/man-pages/man5/proc_pid_cmdline.5.html
[linux-proc-environ]: https://man7.org/linux/man-pages/man5/proc_pid_environ.5.html
[posix-exec]: https://pubs.opengroup.org/onlinepubs/9799919799/functions/exec.html
[windows-create-pipe]: https://learn.microsoft.com/en-us/windows/win32/api/namedpipeapi/nf-namedpipeapi-createpipe
[windows-create-process]: https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-createprocessw
[windows-named-pipe-security]: https://learn.microsoft.com/en-us/windows/win32/ipc/named-pipe-security-and-access-rights
[windows-named-pipes]: https://learn.microsoft.com/en-us/windows/win32/ipc/named-pipes
[windows-redirected-io]: https://learn.microsoft.com/en-us/windows/win32/procthread/creating-a-child-process-with-redirected-input-and-output
