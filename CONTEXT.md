# Project Context

This is the single source of truth for the project's domain terminology, constraints, and established decisions.

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
