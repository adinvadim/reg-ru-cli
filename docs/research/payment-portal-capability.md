# REG.RU payment-portal capability

Research date: 2026-07-30

## Scope and evidence

This note asks whether a REG.RU bill can be turned into a stable external
checkout link suitable for a CLI. It uses only current first-party REG.RU
documentation and anonymous, read-only access to the public routes linked by
that documentation. No credentials or authenticated browser session were used,
and no order, bill, payment, or account state was created or changed.

The public help centre documents the product journey, while
[REG.API 2][api] documents the programmable bill fields. Neither is a contract
for the private HTTP calls made by the personal cabinet.

## Conclusion

REG.RU does **not** publish a stable `bill_id -> checkout URL` contract. The
documented API can return a bill ID and bill metadata, and it can change a
limited set of payment types, but no documented bill response contains a URL.
The documented cabinet journey requires an authenticated user to locate the
bill in **Balance → Bills**, invoke **Change payment method**, and choose an
available payment method interactively.

A CLI can safely expose:

- bill IDs, contents, amounts, payment types, and payment status from REG.API;
- the generic personal-cabinet landing page
  `https://www.reg.ru/user/account/` for the user to continue manually;
- the public quick-renewal page
  `https://www.reg.ru/domain/service/renewal` as a separate, anonymous renewal
  workflow, clearly labelled as **not** a checkout link for an existing bill.

It should not synthesize a bill-specific URL, scrape or replay cabinet
requests, or promise that opening the generic cabinet route will select the
right bill.

## What is actually mapped

### API order or service to bill

The published API establishes identifiers, not checkout links:

- `service/renew` can return `bill_id` and `status:
  only_bill_created` when an unpaid bill was created; its response schema has
  no URL. ([REG.API `service/renew`][service-renew])
- `service/get_bills` maps services to related bill numbers, but is documented
  for partners only. Its response is a list of bill IDs, again without a URL.
  ([REG.API `service/get_bills`][service-bills])
- `bill/get_not_payed` lists `bill_id`, date, amounts, payment type/status, and
  line items. Its documented response has no checkout or redirect field.
  ([REG.API `bill/get_not_payed`][unpaid])
- `bill/nop` checks the status of known bill IDs, but does not return a
  checkout destination. ([REG.API bill functions][bill-functions])

Thus a CLI can keep a stable order/service-to-bill relationship where the
documented methods permit it. There is no documented next edge from that bill
ID to a checkout URL.

### Bill to cabinet payment flow

The current support instructions say that an existing bill's payment method is
changed by first authenticating, opening **Balance**, navigating to **Bills**,
using the unpaid bill's three-dot menu, and choosing **Change payment method**.
The resulting page lets the user select one of the methods available to that
account and bill. ([change or delete a bill][change-bill])

Notably, the first-party help pages' **Balance** and **Bills** links both lead
to the same generic route, `https://www.reg.ru/user/account/`; they do not
publish a bill-specific route or a query parameter carrying `bill_id`.
([change or delete a bill][change-bill], [payment methods and
flow][payment-flow]) The checkout transition is described only as UI
navigation.

The order flow is similarly UI-oriented: create or renew an order, open the
shopping cart at `https://www.reg.ru/shopcart/view`, click **Proceed to
payment**, then select a method; only then is the bill issued and ready for
payment. ([payment methods and flow][payment-flow]) This is a cart-to-new-bill
flow, not a public route for paying an arbitrary existing API bill.

## Payment-method and external-URL behavior

`bill/change_pay_type` is the closest documented API operation. It accepts one
or more bill IDs, a new type, and currency. REG.RU says that some types can
cause a bill to be issued in the selected payment system and that `prepay`
pays immediately. However, the documented response contains only bill ID,
currency, amounts, payment type, and payment status—no URL, token, provider
name, expiry, or redirect metadata. ([REG.API
`bill/change_pay_type`][change-pay-type])

The current cabinet supports flows whose final interaction may occur outside
REG.RU, but their handoff is method-specific and interactive:

- card payment collects card details and may require an SMS confirmation;
- ЮMoney requires authentication at ЮMoney;
- SberPay sends a push to the user's phone;
- SBP displays a QR code to open in the user's bank app;
- cash payment continues on a payment-system site;
- Yandex Split requires Yandex authentication and a choice of card and
  instalments.

REG.RU documents those user actions but publishes none of the generated
provider URLs, QR payload formats, request parameters, link lifetimes, or
callback contracts. ([payment methods and flow][payment-flow]) Consequently,
even if an authenticated cabinet produces an external URL at runtime, the
public material does not make its shape a stable integration point.

`user/get_reseller_url` is not an exception. Despite its name, it retrieves a
partner-configured return URL for the `srv_seowizard` external service and only
for `refill` or `refund`; it is unrelated to REG.RU bills or checkout.
([REG.API `user/get_reseller_url`][reseller-url])

## Authentication and session boundary

The documented existing-bill workflow explicitly begins with authentication
to the REG.RU site. The account page then exposes balance, saved payment
methods, bills, and account history. ([change or delete a bill][change-bill],
[personal-cabinet overview][cabinet])

REG.API credentials and the personal-cabinet browser session are separate
documented mechanisms. REG.API uses its own authenticated requests; no
first-party source documents a way to exchange API credentials, an API
signature, or an API client certificate for a cabinet session or a
bill-specific browser link. Therefore a CLI that opens the generic cabinet
route must expect the browser to establish or reuse its own login session.

Payment can introduce an additional provider session or device confirmation.
The CLI must not assume that a REG.RU session is sufficient to complete
ЮMoney, card 3-D Secure/SMS, SberPay, SBP, cash, or Yandex Split flows.

## Anonymous renewal is a separate contract

REG.RU publishes a quick-renewal entry point at
`https://www.reg.ru/domain/service/renewal`. It accepts a domain or service
identity and can be used without access to the personal cabinet for domains,
hosting, VPS/Dedicated, and Cloud Servers. ([payment methods and
flow][payment-flow], [quick domain renewal][quick-renewal])

This is not a fallback mapping for an existing bill:

- the input documented on the public page is the service/domain, not a bill ID;
- the price can differ from cabinet renewal;
- only a physical person can pay anonymously;
- the resulting payment is not shown in the cabinet's **Bills** section and
  does not trigger the normal contact-email notification.

Those differences make it unsafe for a CLI to present quick renewal as “pay
bill.” At most it is an independent `open renewal page` capability.

## What cannot be concluded without an authenticated session

The following facts cannot be observed from anonymous public material:

- whether the current cabinet has an internal bill-detail or checkout
  deep-link, and its exact path or query parameters;
- whether such a route accepts a raw `bill_id`, requires account-scoped opaque
  IDs, or rejects bills not selected in the current UI flow;
- the cookies, CSRF tokens, one-time tokens, or referrer state required by the
  transition;
- the actual redirect hosts, URL formats, expiry, replayability, and
  shareability for each payment provider;
- whether an API-created unpaid bill behaves identically to a cabinet-created
  bill in every payment method;
- which payment methods the cabinet will offer for a particular account type,
  bill contents, currency, residency, or current provider availability;
- how return, cancellation, timeout, and already-paid/deleted-bill states are
  represented in browser navigation.

An authenticated, read-only browser/network inspection could answer what the
cabinet does **today** for a test bill. It still would not turn undocumented
private routes into a supported or stable external contract. Stability would
require first-party documentation or an explicit provider commitment.

## CLI decision

Treat the boundary as `bill_id -> generic human handoff`, not `bill_id ->
checkout URL`:

1. Show the documented bill metadata and status.
2. Offer to open `https://www.reg.ru/user/account/`.
3. Tell the user to continue through **Balance → Bills** and select the bill.
4. Keep quick renewal as a separately named operation that starts from a
   service/domain, never from a bill ID.
5. Do not automate provider checkout or store/surface a runtime payment URL as
   if it were durable.

[api]: https://www.reg.ru/reseller/api2doc
[bill-functions]: https://www.reg.ru/reseller/api2doc#bill_functions
[unpaid]: https://www.reg.ru/reseller/api2doc#bill_get_not_payed
[change-pay-type]: https://www.reg.ru/reseller/api2doc#bill_change_pay_type
[service-renew]: https://www.reg.ru/reseller/api2doc#service_renew
[service-bills]: https://www.reg.ru/reseller/api2doc#service_get_bills
[reseller-url]: https://www.reg.ru/reseller/api2doc#user_get_reseller_url
[payment-flow]: https://help.reg.ru/support/finansovyye-voprosy/oplata-schetov-i-uslug/sposoby-oplaty-kak-vystavit-i-oplatit-schet
[change-bill]: https://help.reg.ru/support/finansovyye-voprosy/oplata-schetov-i-uslug/kak-izmenit-sposob-oplaty-ili-udalit-schet
[cabinet]: https://help.reg.ru/support/lichnyy-kabinet/registratsiya-i-kontaktnyye-dannyye/znakomstvo-s-lichnym-kabinetom-reg-ru
[quick-renewal]: https://www.reg.ru/domain/service/renewal
