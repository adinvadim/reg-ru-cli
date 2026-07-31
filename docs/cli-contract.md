# `regru` CLI contract

Status: foundation contract established by the Wayfinder ticket
[Task: scaffold the regru CLI contracts](https://github.com/adinvadim/reg-ru-cli/issues/3).
Resource-specific flags and provider result schemas remain owned by their
implementation tickets.

## Purpose and usage

`regru` is a human-friendly and agent-safe CLI for the REG.RU services used by
this project. It is distributed as one Go binary.

```text
regru [global flags] <command> [subcommand] [arguments]
```

The visible command tree is:

```text
regru auth login|status|refresh|logout
regru vps list|get|ips|create|start|stop|reboot|delete
regru s3 bucket list|get|create|configure|delete
regru s3 credentials list|create|revoke
regru billing balance|invoices|invoice|checkout
regru support ticket list|get|create|reply|attach|close|reopen
regru completion bash|zsh|fish|powershell
```

Commands remain visible in help and completion before their adapters are
implemented. A live unavailable command fails closed with a structured
`capability_unavailable` error and exit code 7.

## Global flags

| Flag | Contract |
| --- | --- |
| `--account NAME` | Select one local account profile. Falls back to `REGRU_ACCOUNT`; no command prompts for an account name. |
| `--json` | Emit exactly one versioned JSON success or error envelope. Mutually exclusive with `--plain`. |
| `--plain` | Emit stable line-oriented text. Mutually exclusive with `--json`. |
| `--no-input` | Disable all prompts and interactive login. Read-only commands may still run. |
| `-n`, `--dry-run` | Validate locally and render a redacted mutation plan with zero executor, provider, browser, DNS, or network calls. |
| `-f`, `--force` | Satisfy the confirmation requirement for a requested mutation. It never bypasses capability, authentication, identity, drift, timeout, or redaction checks. |
| `--no-color` | Disable ANSI color. Color is also disabled for non-TTY stderr, `NO_COLOR`, or `TERM=dumb`. |
| `--timeout DURATION` | Bound one network operation. Default `30s`; must be greater than zero and no more than `5m`. |
| `-h`, `--help` | Show command help. |
| `--version` | Print the binary version to stdout. |

`auth login` additionally has `--login-timeout`, defaulting to 10 minutes and
bounded to 1–30 minutes.

Future non-secret configuration follows this precedence:

```text
flags > environment > project config > user XDG config > defaults
```

The profile/config implementation ticket owns the project and XDG file
formats. Secrets, cookies, browser storage, CSRF values, and service
credentials are never accepted as flags.

## Streams and output modes

Primary success data goes to stdout. Diagnostics, progress, browser-launch
notices, prompts, and final errors go to stderr. Cobra handlers do not print
errors; the process runner renders each final error exactly once.

Human output may evolve for readability. `--plain` and the JSON schema are
automation contracts.

JSON success:

```json
{
  "schemaVersion": "regru.cli/v1",
  "ok": true,
  "command": "auth status",
  "data": {},
  "warnings": []
}
```

JSON error:

```json
{
  "schemaVersion": "regru.cli/v1",
  "ok": false,
  "command": "vps list",
  "error": {
    "code": "capability_unavailable",
    "message": "this capability is not implemented in the current build",
    "exitCode": 7,
    "retryable": false,
    "details": {
      "capability": "cloudvps.instances"
    }
  }
}
```

Plain success is one stable record per line. Plain errors use
`code<TAB>message` on stderr.

## Exit codes

| Exit | Meaning |
| ---: | --- |
| 0 | Success or a completed local dry-run |
| 1 | Unexpected/internal failure |
| 2 | Invalid invocation or flag value |
| 3 | Missing or invalid local configuration |
| 4 | Interaction or confirmation required |
| 5 | Authentication or account-identity failure |
| 6 | Network/provider failure |
| 7 | Capability unavailable or not implemented |
| 8 | Private provider contract drift |
| 10 | A mutation may have committed but cannot yet be verified |
| 124 | Operation deadline exceeded |
| 130 | Interrupted by Ctrl-C |

More precise automation decisions use the stable error `code`, not only the
process exit value.

## Interaction and mutation safety

Fresh browser login requires a TTY on stdin and fails before acquiring a
browser or adapter when `--no-input` is set or stdin is not a TTY. Normal
noninteractive commands never launch a browser implicitly.

Mutations require a TTY confirmation or `--force`. `--dry-run` occurs before
confirmation and before the injected executor, so it cannot make a provider,
portal, S3, browser, BFF, or CDP call. Dry-run output reports only the account
alias, operation name, and argument count; it does not echo opaque identifiers
or paths.

The process root is canceled on Ctrl-C. The first interrupt propagates through
the command context; signal interception is then removed so a second Ctrl-C
uses the platform's immediate exit behavior. All operation waits are bounded.

## Examples

```sh
regru --help
regru --version
regru --account personal auth status --json
regru --account personal --no-input billing balance --plain
regru --account personal --dry-run vps delete vps-id
regru --account personal --force auth logout
regru completion zsh
```
