# Redacted REG.RU account capability matrix

Research date: 2026-07-31

## Purpose and evidence boundary

This report resolves
[Research: authenticate and map REG.RU capabilities](https://github.com/adinvadim/reg-ru-cli/issues/2)
without retaining a login, account number, service ID, token, key, cookie,
balance, bill, bucket name, server name, or support content.

The report combines:

- targeted 1Password metadata and exact-field shape reads in one persistent
  tmux session;
- one `user/get_balance` and one `bill/get_not_payed` read-only REG.API 2
  request for each service-vault profile;
- one existing authenticated REG.RU/REG.Cloud browser context, correlated to a
  service-vault profile by a one-way in-memory digest;
- read-only CloudVPS GET requests and shape-only portal observations; and
- three independent primary-source contract refreshes.

No provider setting, credential, resource, bill, payment, or support request
was created, changed, rotated, submitted, or deleted. A visible S3
**Create key set** control was identified and deliberately left untouched.

## Capability model

Contract stability and runtime availability are independent:

```text
contract:
  supported | experimental-private | unavailable

availability:
  available | available-empty | not-configured | auth-blocked
  service-locked | human-observation-required | contract-error
  temporarily-unavailable
```

`unsupported` or `unavailable` is reserved for affirmative contract evidence.
A missing credential, source-IP denial, absent session, empty result, rate
limit, or schema failure must not be relabelled as unsupported.

The account hierarchy is:

```text
PortalProfile
  isolated browser context
  REG.API authorization
  CloudEnvironment[]
    CloudVPS capability
    S3 control-plane capability
    S3 protocol credential capability
```

A Cloud `Service-ID` is an opaque environment selector below a portal
principal, not another account identity.

## Current redacted matrix

Aliases are local to this report and reveal no provider identifier.

| Profile | Portal | REG.API 2 | CloudVPS | S3 | Billing and support |
| --- | --- | --- | --- | --- | --- |
| Service-vault profile A | Login fields are present and well-formed; no active browser context was available. `human-observation-required` | Both documented reads reached REG.API and returned `ACCESS_DENIED_FROM_IP`. Authentication is `rejected/source-ip-denied`; balance and unpaid bills are `auth-blocked`. | `human-observation-required`; no dedicated token field exists in the local login item. | `human-observation-required`; no dedicated access/secret fields exist in the local login item. | Supported generic cabinet handoffs are available after login; private machine capabilities remain `human-observation-required`. |
| Service-vault profile B | Existing browser context is active and matched the profile without retaining the principal. | Both documented reads reached REG.API and returned `ACCESS_DENIED_FROM_IP`. Authentication is `rejected/source-ip-denied`; balance and unpaid bills are `auth-blocked`. | `available`. One Cloud environment exposed a non-empty, unmasked, token-like field. Balance, server inventory, plans, and images all passed the read-only probes below. | Control plane is `available`: object storage and bucket inventory exist. Protocol credentials are `not-configured`: no key rows or existing-key links were present, while a separate create-key control was visible. Signed `ListBuckets` was not attempted. | Authenticated cabinet access is available. Published billing reads remain REG.API-gated; generic checkout and support handoffs are supported. Private billing/support operations remain experimental and independently capability-gated. |
| Previously discovered private-vault profiles | Prior scoped discovery established two additional REG.RU cabinet profiles. Their exact fields were not broadened into this service-account read. `human-observation-required` | `human-observation-required` | `human-observation-required` | `human-observation-required` | `human-observation-required` |

The last row is intentionally not an implementation blocker. `regru` must
discover capabilities per configured profile at runtime, and
[Task: run authorized end-to-end verification](https://github.com/adinvadim/reg-ru-cli/issues/10)
already requires `auth login` and `account doctor --json` for every configured
profile. That ticket is the correct place to complete additional
human-authenticated account observations after the CLI exists.

## Live read-only probe record

### Local credential shapes

The service vault contains two matching LOGIN items. Each contains one
email-shaped username and one concealed password. Neither contains a separate
CloudVPS bearer token, REG.API alternate password/signature/certificate, or S3
access/secret key field.

### REG.API 2

For each service-vault profile:

- `POST user/get_balance` returned HTTP 200 with `result: error` and
  `ACCESS_DENIED_FROM_IP`;
- `POST bill/get_not_payed` returned the same structured result; and
- no balance, bill, account identifier, or error parameter was retained.

This proves transport reachability and a source-IP authorization gate. It does
not prove that the password is wrong, that a client certificate is absent, or
that either documented capability is unsupported.

### CloudVPS

For service-vault profile B:

| Probe | Result retained |
| --- | --- |
| `GET /v1/balance_data` | HTTP 200; root `balance_data` object with the documented field names. No values retained. |
| `GET /v1/reglets` | HTTP 200; `reglets` array present and non-empty. No server fields retained. |
| `GET /v2/plans?region=msk1&page=1&items_per_page=10` | HTTP 200; non-empty `plans` array and `metadata` pagination object. |
| `GET /v2/images?region=msk1&page=1&items_per_page=10` | HTTP 200; non-empty `images` array and `metadata` pagination object. |

An initial contract-negative check also confirmed that `/v1/balance` is not
the current route and that v2 catalogue requests reject missing mandatory
pagination/region parameters. The successful probes above use the current
OpenAPI contract.

### S3

The authenticated control plane for profile B showed an active object-storage
surface and existing bucket inventory. The access-key surface had zero table
rows, zero existing-key links, and a distinct create-key control. Therefore:

- control-plane inventory is `available`;
- S3 protocol authentication is `not-configured`;
- a signed `ListBuckets` result cannot be claimed; and
- key creation is a separate credential mutation, not a read fallback.

## Resolution

Implementation can proceed with a dynamic, two-axis capability matrix:

1. Model public contract stability separately from per-profile availability.
2. Probe each documented read capability directly and cache the result; do not
   turn one auth failure into a global unsupported verdict.
3. Scope portal sessions to one principal and Cloud credentials/capabilities to
   one selected environment.
4. Import only complete, non-empty existing credentials after the required
   disclosure/persistence confirmation. Never create, reset, or rotate a
   credential to satisfy a read-only discovery flow.
5. Keep REG.API billing reads supported but source-IP/certificate gated.
6. Keep generic billing and support handoffs supported. Treat browser-session
   brokerage and the accepted support adapter as experimental/private.
7. Treat stable bill-specific checkout links and unobserved support
   list/detail/reply/status/download contracts as unavailable.
8. Complete the remaining real-account matrix during authorized end-to-end
   verification rather than hard-coding today's user account inventory into
   the product contract.

## Supporting research

- [REG.API 2 authentication and read-capability matrix refresh](capability-matrix-regapi-refresh.md)
- [CloudVPS and REG.RU S3 capability-matrix refresh](capability-matrix-cloud-s3-refresh.md)
- [Authenticated portal capability-boundary refresh](capability-matrix-portal-refresh.md)
- [Credential field visibility contract](credential-field-visibility-contract.md)
- [Private portal resilience contract](private-portal-resilience-contract.md)
