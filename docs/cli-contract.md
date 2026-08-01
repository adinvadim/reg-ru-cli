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
regru account add|list|show|use|remove|doctor
regru capability list|probe
regru vps list|get|ips|create|rename|start|stop|reboot|rebuild|resize|password-reset|clone|delete
regru vps action show|wait
regru vps ip list|show|add|ptr|remove
regru vps ssh-key list|add|rename|remove
regru vps snapshot list|create|remove
regru vps backup enable|disable|restore
regru vps plan list|show
regru vps image list|show
regru s3 service show
regru s3 service quota set
regru s3 bucket list|show|create|update|delete
regru s3 bucket policy|cors|lifecycle|website get|set|delete
regru s3 bucket versioning get|set
regru s3 credentials list|create|revoke
regru billing balance|history
regru billing invoice list|show|status|create|delete|payment-link
regru billing invoice payment-method list|set
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

CloudVPS commands use `--timeout` for each credential or HTTP request and
`--wait-timeout` for the complete asynchronous action wait. The wait defaults
to 10 minutes and is bounded to 1 second–24 hours. Mutations wait by default;
`vps --no-wait ...` returns the accepted action without polling. See the
[CloudVPS command guide](cloudvps.md).
REG.RU S3 commands use separate private-control-plane and signed-protocol
adapters; see the [S3 command guide](s3.md).
Billing commands preserve the boundary between REG.API invoices and CloudVPS
finance; see the [billing command guide](billing.md).
Support commands expose a typed experimental boundary but currently fail closed
with operation-specific reasons; see the [support command guide](support.md).

Account selection follows this precedence:

```text
--account > REGRU_ACCOUNT > project account > user default > no selection
```

User profiles live in the platform user-config directory. A project
`.regru/config.toml` may only select an existing account alias; it cannot
define profiles, endpoints, session handles, or credential routing. Secrets,
cookies, browser storage, CSRF values, and service credentials are never
accepted as flags, environment variables, or normal config.

## Browser-backed portal sessions

`auth login` opens a headed Chrome or Chromium process with a dedicated profile
owned by the selected local account. Authentication fields, CAPTCHA, and
second-factor challenges stay in that browser. A successful provider refresh
is reduced in an isolated browser world to an opaque keyed principal digest;
the CLI never receives the raw principal, cookies, CSRF values, or response
body.

Each login is staged in a new browser profile. It replaces the committed
session reference only after authentication and principal matching succeed, so
cancellation, timeout, contract drift, and account mismatch preserve the
previous session. `--force` requests a fresh staged login; without it, an
already active session is returned unchanged.

`auth status` and `auth refresh` both perform an authoritative provider refresh
inside the committed browser profile. This may extend the provider session.
Their stable state values are `not-established`, `active`, and `session-lost`;
the latter deliberately does not guess whether the provider invalidated the
session because of expiry, logout elsewhere, revocation, or another cause.
`auth logout` removes local browser state only after the provider confirms that
the session is gone. An ambiguous logout keeps the local reference and returns
`outcome_unknown`.

Normal config stores only an opaque `s_...` session reference. Browser state
lives beneath `<UserConfigDir>/regru/portal-sessions`, separately for every
account, with an OS-backed exclusive lock while in use. The directory is
private to the current OS user but is intentionally not wrapped in
application-managed encryption.

Portal-session failures use stable codes including `missing_browser`,
`login_cancelled`, `portal_profile_busy`, `authentication_expired`,
`account_mismatch`, `browser_session_interrupted`,
`private_contract_drift`, and `outcome_unknown`. Output contains the account
alias, reduced state, and currently verified capability names only, never the
opaque session reference or browser profile path.

## Credential process

An account may configure an external credential helper in the user-only
profile:

```toml
[accounts.work.credential_process]
command = ["/usr/local/bin/credential-helper", "get", "work"]
```

`regru` starts this argv directly, without a shell, only when a provider
adapter requests a credential. Help, completion, local validation, dry-run,
unimplemented commands, and adapters that do not request credentials never
start it. The helper has a 30-second maximum runtime, further bounded by the
command timeout.

The helper returns one strict, bounded JSON document on stdout using the
internal `regru.credential-process/v1` protocol. Its stdout is captured rather
than inherited, and its stderr is discarded. Failures are rendered as stable
`regru` error codes without helper output. The command and its arguments are
user-owned routing metadata, not secret values; credentials must never be
placed in them. A project config cannot define or replace the helper.

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
      "code": "network_error",
      "message": "CloudVPS rejected the request",
      "exitCode": 6,
      "retryable": false,
      "details": {
        "provider": "CloudVPS",
        "provider_code": "VALIDATION_ERROR",
        "http_status": 400
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
regru --account personal vps plan list --region openstack-msk3
regru --account personal --dry-run vps create --size cloud-2 --image ubuntu-24-04-amd64 --region openstack-msk3
regru --account personal --force vps action wait action-id --wait-timeout 20m
regru --account personal --force auth logout
regru completion zsh
```
