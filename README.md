# regru

`regru` is a human-friendly and agent-safe CLI for the REG.RU services used by
this project: CloudVPS, REG.RU S3, account and browser authentication, billing,
and an experimental support boundary. It is distributed as one Go binary and
keeps stable `--json` and `--plain` modes for scripts and agents.

The CLI is intentionally conservative. Mutations support `--dry-run` and
require terminal confirmation or `--force`; private provider contracts are
probed and fail closed instead of guessing or scraping rendered pages.

## Capability status

| Area | Backend | Current status |
| --- | --- | --- |
| Accounts | Local profile store | Available; normal config contains routing metadata only |
| Authentication | Dedicated Chrome/Chromium profile | Available; login, refresh, status, and local logout are browser-backed |
| CloudVPS | Documented v1/v2 APIs | Inventory, lifecycle, IP, key, snapshot, backup, plan, and image commands are implemented |
| S3 | REG.Cloud control plane plus signed S3 protocol | Bucket and configuration lifecycle is implemented; object transfer is out of scope |
| Billing | REG.API 2, CloudVPS, and a gated portal adapter | Documented reads/mutations are implemented; checkout is a visible-browser handoff, not a payment URL |
| Support | Experimental private adapter | Commands are visible but currently fail closed with an operation-specific uncaptured-contract reason |

Run `regru capability list` for locally configured capability state and
`regru capability probe` for bounded provider-facing verification. A command
remaining visible in help does not mean the selected account can execute it.

## Install

Download the binary for your OS and architecture from
[GitHub Releases](https://github.com/adinvadim/reg-ru-cli/releases), verify it
against `checksums.txt`, rename it to `regru` (or `regru.exe`), and place it on
your `PATH`.

To build from a clean checkout, install Go 1.26 or newer and run:

```sh
make check
make build
./bin/regru --version
```

`make dist VERSION=0.1.0` produces CGO-free macOS, Linux, and Windows binaries
for amd64 and arm64 plus SHA-256 checksums under `dist/`.

To uninstall, remove the binary. If you also want to discard local profiles
and browser sessions, remove the `regru` directory beneath your platform user
configuration directory only after signing out and confirming you no longer
need those profiles. Typical locations are `~/Library/Application Support/regru`
on macOS and `${XDG_CONFIG_HOME:-~/.config}/regru` on Linux.

## Quick start

Create a non-secret profile, select it, and bootstrap the portal session:

```sh
regru account add work --label "REG.RU work"
regru account use work
regru --account work auth login
regru --account work account doctor --json
regru --account work capability list
```

`auth login` opens a dedicated headed browser. Complete passwords, CAPTCHA,
and second factor in that browser; do not paste cookies or CSRF values into the
terminal. Operational commands do not open an interactive login implicitly.

The private user profile is `<UserConfigDir>/regru/config.toml`. A project may
contain only this non-secret selector:

```toml
schema_version = 1
account = "work"
```

See [Account profiles and credential-process contract](docs/profile-secret-contract.md)
for the complete user schema and account-selection precedence.

## Credentials and 1Password

The base binary has no built-in password-manager integration. A profile may
name an external helper that returns one short-lived credential bundle on
captured stdout:

```toml
[accounts.work.credential_process]
command = ["/Users/me/.local/bin/regru-1password-credentials", "--account", "my.1password.com", "--vault", "Service Vault", "--item", "REG.RU work"]
```

A targeted example is provided at
[`examples/credential-process/1password.sh`](examples/credential-process/1password.sh).
It requires the official `op` CLI, desktop-app integration, and `jq`. Create a
single item in the named account and vault with only the exact custom field
labels you need: `portal.login`, `portal.password`, `regapi.username`,
`regapi.password`, `cloudvps.token`, `s3.access_key_id`, and
`s3.secret_access_key`. Paired fields must be present together.

Install the example at a private absolute path, unlock 1Password, and verify
that `op whoami --account my.1password.com` succeeds before using `regru`.
The helper reads exactly the configured item and never enumerates vaults. Its
item name, account, vault, and path are routing metadata; never place a secret
value in the command array. The helper output is consumed and redacted by
`regru`, so do not run the helper directly in a terminal containing real
credentials.

For setup and sign-in details, follow the official
[1Password CLI documentation](https://developer.1password.com/docs/cli/get-started/).

## Common flows

Human-oriented commands use readable output and confirmations:

```sh
regru --account work auth status
regru --account work vps list
regru --account work --dry-run vps create --size PLAN --image IMAGE --region REGION
regru --account work billing invoice list
regru --account work --dry-run s3 bucket create UNIQUE_NAME --access private
```

Agent and script flows should select the account explicitly, disable input,
and request a stable output mode:

```sh
regru --account work --no-input --json account doctor
regru --account work --no-input --json capability probe
regru --account work --no-input --plain vps list
```

Use `--force` only for an already approved mutation. It skips the prompt but
never bypasses authentication, capability, drift, timeout, or reconciliation
checks. A result of `outcome_unknown` means the request may have committed and
must be reconciled before retrying.

## Architecture and contracts

The Cobra command layer owns parsing, confirmation, timeouts, and normalized
output. Typed provider executors own public CloudVPS/REG.API/S3 calls. Private
portal operations run through a per-account browser-session broker and typed
in-browser programs; private values stay inside that boundary.

The authoritative guides are:

- [CLI, output, safety, and exit-code contract](docs/cli-contract.md)
- [CloudVPS commands](docs/cloudvps.md)
- [S3 commands](docs/s3.md)
- [Billing and invoice commands](docs/billing.md)
- [Experimental support adapter](docs/support.md)
- [Domain terminology and established constraints](CONTEXT.md)

The visible command tree is discoverable with `regru --help` and any nested
`--help`. Shell completion is available through `regru completion
bash|zsh|fish|powershell`.

## Development and releases

`make check` runs formatting verification, vetting, unit tests, and the race
detector. CI additionally scans the repository for secrets and cross-compiles
every release target. Release tags matching `v*` must have a corresponding
`## X.Y.Z - YYYY-MM-DD` section in `CHANGELOG.md`; the release workflow uses
that section verbatim as the GitHub release notes and uploads the binaries and
checksums.

