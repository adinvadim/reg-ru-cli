# Authenticated REG.RU portal capability observation

Research date: 2026-07-30

## Scope and guardrails

This note records a read-only observation of the current REG.RU personal
cabinet in an existing authenticated Chrome session. It complements the public
API research; it does not turn private cabinet behavior into a supported
contract.

No login credential, cookie, account identifier, balance, payment-card detail,
bill identifier, ticket identifier, ticket subject, or ticket content was
captured. No payment flow was started, no bill or support request was created,
and no account or service setting was changed.

## Support portal

The authenticated support area currently exposes separate views for open,
closed, and all requests. Ticket detail pages use opaque path components rather
than the human-readable ticket numbers shown in the list.

Starting a new request opens a multi-step composer whose first step associates
the request with a service. Observation stopped before selecting a service or
submitting any content.

Read-only browser network inspection found:

- session refresh through `login.reg.ru`;
- account-header data through `header-bff.svc.reg.ru`, including a count of
  answered tickets;
- a support composer JavaScript module served from `www.reg.ru`;
- a GraphQL WebSocket connection to `gql-acc.svc.reg.ru`;
- support phone data through `showcase-bff.svc.reg.ru`.

The observation did not capture or replay GraphQL operations, inspect message
payloads, test schema introspection, or exercise ticket creation, replies,
attachments, status changes, or pagination. The current UI therefore proves
that REG.RU has private session-authenticated support services, but it does not
establish a documented, stable ticket API suitable for a CLI.

## Billing portal

The authenticated balance area currently exposes account balance, saved
payment methods, bills, and operation history. The Bills view displays paid
bills with status, date, payment method, amount, and related services.

The observed account had no unpaid bill available for a read-only checkout
inspection. Paid rows did not expose a bill-specific payment link. This does
not prove that an unpaid bill lacks a runtime checkout transition; it only
means that the transition could not be characterized without creating or
locating an unpaid bill.

The observation deliberately stopped before **Top up**, payment-method
attachment, promo-code activation, withdrawal, or any other financial flow.

## Contract decision

The authenticated evidence supports the same conservative CLI boundary as the
public documentation:

1. Treat support and checkout channels in the personal cabinet as private,
   session-bound implementation details.
2. Do not construct ticket URLs from displayed numbers or expose observed
   opaque path components as durable identifiers.
3. Do not scrape or replay the cabinet GraphQL/BFF traffic as a supported API.
4. Keep support-ticket mutations capability-gated until REG.RU publishes a
   contract or explicitly supports an integration.
5. Map documented bill metadata through REG.API, then hand the user off to the
   generic authenticated cabinet for payment.
6. Do not promise `bill_id -> checkout URL` until an official contract exists.

## Remaining authorized characterization

The following questions remain open:

- the authenticated GraphQL operation names and schemas used for read-only
  ticket listing and details;
- the mutation contracts for ticket creation, replies, attachments, and status
  changes;
- CSRF, origin, session-expiry, rate-limit, and error behavior;
- the runtime transition from an unpaid bill to each available payment
  provider, including URL lifetime and replay/share behavior;
- whether REG.RU offers a supported partner interface for either capability
  outside the public documentation.

Answering the mutation and checkout questions would require a separate,
explicitly approved probe using disposable data or a provider-supported test
environment. They are not required for the safe first version of `regru`.
