# Account profiles and credential-process contract

Status: established by the Wayfinder ticket
[Task: implement provider-neutral multi-account profiles](https://github.com/adinvadim/reg-ru-cli/issues/4).

## Boundary

The base `regru` binary has no concrete secret-store integration. It does not
discover a credential store, interpret a vendor-specific reference, or fall
back to plaintext storage. Instead, the user profile may name a generic
external credential helper:

```toml
[accounts.work.credential_process]
command = ["/usr/local/bin/credential-helper", "get", "work"]
```

Normal commands remain conventional:

```sh
regru --account work --no-input billing balance --json
```

When an adapter first requests a credential, `regru` starts the configured
argv directly, without shell parsing. The helper may obtain credentials by any
means outside `regru`; the binary knows only the process contract. The helper
writes one strict JSON document to its captured stdout:

```json
{
  "schemaVersion": "regru.credential-process/v1",
  "fields": {
    "regapi.username": "<private>",
    "regapi.password": "<private>"
  }
}
```

Supported field families are `portal.login` plus `portal.password`,
`regapi.username` plus `regapi.password`, `cloudvps.token`, and
`s3.access_key_id` plus `s3.secret_access_key`. Paired families are
all-or-nothing. Every value, including login names and access-key identifiers,
is private.

The output is capped at 64 KiB, strict-schema, duplicate-key rejecting, and
command-scoped. The helper is lazy, runs at most once per invocation, and has a
30-second maximum runtime further bounded by the command timeout. Its stdout is
never inherited by the terminal, its stderr is discarded, and failures expose
only stable `regru` errors.

Credential values never enter `regru` arguments, environment variables,
profile config, errors, results, logs, or another child process. The helper
command is routing metadata and must not contain secret values. Owned buffers
are cleared on release as a best-effort lifetime reduction, not as a
cryptographic erasure promise.

## User profiles

The user file is `<UserConfigDir>/regru/config.toml`. It contains local aliases,
random stable IDs, labels, provider metadata, optional opaque session
references, and optional credential-process routing:

```toml
schema_version = 1
default_account = "personal"

[accounts.personal]
id = "p_aaaaaaaaaaaaaaaaaaaaaaaaaa"
label = "Personal"
provider = "reg.ru"

[accounts.personal.credential_process]
command = ["/usr/local/bin/credential-helper", "get", "personal"]
```

Writes use a private directory, a mode-0600 same-directory temporary file,
sync/close, and atomic rename. Unknown fields and unsupported schema versions
fail closed.

The closest project `.regru/config.toml` has a deliberately smaller schema:

```toml
schema_version = 1
account = "work"
```

It can select an existing alias only. A checked-out project cannot redirect
authenticated endpoints or replace credential, helper, or session routing.

## Selection and commands

Selection is deterministic:

```text
explicit --account
  > REGRU_ACCOUNT
  > closest project account
  > user default_account
  > account_required
```

An explicit empty `--account=` is invalid, and no command silently selects the
only configured profile.

`account list`, `account show`, and `account doctor` expose aliases, labels,
provider name, and configured booleans only. They never render stable IDs,
opaque references, the credential-process command, environment IDs, or
provider identity. `capability list` reports local configured/not-configured
state; `capability probe` is the bounded provider-facing verification seam.
