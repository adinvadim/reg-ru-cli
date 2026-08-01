# Project Context

This is the single source of truth for the project's domain terminology, constraints, and established decisions.

## Product boundary

### Account profile

A local, non-secret alias that selects exactly one REG.RU principal plus its
provider routing metadata. A profile may point to an external
`credential_process` and an opaque browser-session reference, but it never
contains raw credentials, cookies, CSRF values, or provider content.

### Capability state

`configured` means the local profile has the routing material an adapter may
need. It does not prove provider authorization or private-contract validity.
`verified` requires a bounded provider-facing probe. An unavailable capability
stays visible in help and fails closed with a stable reason.

### Credential process

A user-owned executable invoked directly, without a shell, at most once per
CLI invocation and only when an adapter asks for a credential. It returns one
strict `regru.credential-process/v1` document on captured stdout. Its storage,
authentication, and acquisition mechanism remain outside the base binary.

### Mutation outcome

`committed` means a recognized provider success; `rejected` means a recognized
provider refusal; `not-sent` means dispatch provably did not start; and
`outcome_unknown` means the request may have committed. An ambiguous mutation
is never retried blindly, and confirmation or `--force` is not reconciliation.

## Portal authentication

### Portal principal

The one REG.RU account identity authenticated inside a browser session. A
portal principal is not a Cloud environment and cannot be changed by selecting
a service, contract number, header, or URL parameter.

### Browser session

The provider-managed authenticated state for exactly one portal principal
across the REG.RU first-party origins. Different local account profiles never
share a browser session.

### Staged login

A new interactive authentication attempt that has not replaced the profile's
committed browser session. Cancellation, timeout, identity mismatch, or
private-contract drift discards the staged login and leaves the committed
session unchanged.

### Session lost

A previously active browser session that the provider no longer recognizes.
This state deliberately does not claim whether the cause was expiry, logout,
revocation, or another provider-side invalidation.

### Cloud environment

An environment selected beneath an authenticated portal principal. Selecting a
Cloud environment does not authenticate or switch the portal principal.

## Billing

### REG.API invoice

A bill returned by REG.API 2 with a stable `bill_id`, payment state, and—when
listed—currency, amounts, and service items. It is not a CloudVPS refill.

### CloudVPS refill

A credit record from CloudVPS `/v1/billing_history`. It has no published bill
identifier, currency field, payment status, or supported join to a REG.API
invoice. Amount-and-date similarity never establishes such a join.

### Checkout handoff

A confirmed transition into a user-visible, session-bound REG.RU payment
journey. It is not a shareable payment link. Private locators such as
`bill_sid` stay inside the browser adapter and never enter CLI output or
configuration.

## Infrastructure

### CloudVPS action

An asynchronous provider operation returned by the CloudVPS lifecycle API.
Mutations wait for it by default; `--no-wait` returns the accepted action, and
a timed-out wait can be resumed without resending the mutation.

### S3 control plane

The authenticated REG.Cloud surface that owns service activation, bucket
lifecycle, quotas, privacy mode, and key-set metadata. It is separate from the
signed S3 protocol and is probed as a private contract before use.

### S3 data plane

The signed AWS-compatible endpoint used only for verified bucket
configuration operations. Object transfer, sync, presigning, and backup-client
behavior are outside this product.

### Support adapter

An experimental private boundary whose ticket commands remain visible but
fail closed until each exact authenticated operation has a captured, typed,
redacted contract. A working portal session alone never makes a support
operation available.
