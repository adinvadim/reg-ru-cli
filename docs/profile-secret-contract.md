# Account profiles and secret-input contract

Status: established by the Wayfinder ticket
[Task: implement provider-neutral multi-account profiles](https://github.com/adinvadim/reg-ru-cli/issues/4).

## Boundary

The base `regru` binary has no concrete secret-store integration. It does not
discover or invoke a credential producer, interpret a vendor reference, or
fall back to plaintext storage. External tooling may provide command-scoped
credentials through an anonymous pipe:

```sh
credential-source --format regru.secret-input/v1 |
  regru --account work --credentials-stdin --no-input billing balance
```

The producer writes one strict JSON document and closes the pipe:

```json
{
  "schemaVersion": "regru.secret-input/v1",
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

Input is explicit, capped at 64 KiB, strict-schema, duplicate-key rejecting,
and command-scoped. Credential values never enter arguments, environment
variables, profile config, errors, results, logs, or a child process. Owned
buffers are cleared on release as a best-effort lifetime reduction, not as a
cryptographic erasure promise.

## User profiles

The user file is `<UserConfigDir>/regru/config.toml`. It contains local aliases,
random stable IDs, labels, provider metadata, and optional opaque session/store
references:

```toml
schema_version = 1
default_account = "personal"

[accounts.personal]
id = "p_aaaaaaaaaaaaaaaaaaaaaaaaaa"
label = "Personal"
provider = "reg.ru"
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
authenticated endpoints or replace credential/session references.

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
opaque references, environment IDs, or provider identity. `capability list`
reports local configured/not-configured state; `capability probe` is the
bounded provider-facing verification seam.
