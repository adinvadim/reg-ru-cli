# Changelog

## Unreleased

- Add stable CloudVPS backup status output and verify enable or disable postconditions without replaying ambiguous mutations.
- Silently reconcile browser and CLI-egress public IPv4 addresses with the REG.API allowlist after portal login and refresh, with redacted non-blocking failure warnings.
- Show the authenticated REG.RU provider login separately from the local profile alias in human, JSON, and plain auth status output.
- Reconcile interrupted support mutations with independent reads and wait for rendered ticket history before returning details.
- Enable experimental browser-backed support ticket list, detail, create, reply, and close operations with typed drift probes and one-attempt mutation reconciliation.
- Preserve REG.Cloud S3 service and bucket sizes as provider-supplied strings in inventory output.
- Keep interactive portal login alive when REG.RU navigates the staged browser during authentication.
- Scaffold the `regru` Go CLI with stable output, error, safety, authentication, timeout, completion, and capability-placeholder contracts.
- Add provider-neutral multi-account profiles, strict user/project TOML boundaries, redacted account and capability commands, and lazy external credential-process helpers.
- Add isolated browser-backed portal login, refresh, status, logout, principal binding, and typed in-browser BFF/GraphQL execution.
- Add typed CloudVPS v1/v2 clients and VPS, action, IP/PTR, SSH-key, snapshot, backup, plan, and image commands with safe mutation waiting.
- Add REG.RU S3 service, bucket, configuration, quota, privacy, and key-set commands with split portal/S3 adapters, in-memory credentials, drift probes, and read-after-write reconciliation.
- Add source-discriminated REG.API and CloudVPS billing reads, invoice status and mutations, exact-decimal finance models, and fail-closed private checkout gates.
- Add value-redacted portal invoice enrichment and a confirmed, non-shareable visible-browser checkout handoff while keeping unavailable bill-specific method enumeration gated.
- Add a typed experimental support command boundary with deferred message input, redacted dry runs, and precise fail-closed reasons for every uncaptured private ticket capability.
- Add install, configuration, architecture, and capability documentation plus reproducible local checks, secret-scanning CI, cross-platform builds, and changelog-backed GitHub releases.
- Add bounded, provider-facing capability probes with partial redacted results and stable reasons for missing credentials, authentication loss, contract drift, and unavailable adapters.
