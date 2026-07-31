# REG.RU support integration contract refresh

Research date: 2026-07-30

## Scope

This is an independent, anonymous, read-only refresh for
[Grilling: choose the REG.RU support integration boundary](https://github.com/adinvadim/reg-ru-cli/issues/22).
It compares the current first-party evidence with
[Research: characterize private support operation contracts](https://github.com/adinvadim/reg-ru-cli/issues/20)
and its resolution. No authenticated account, private data, upload, or
mutating request was used.

Evidence grades used below:

- **A — published programmatic contract:** an official API reference, schema,
  authentication grant, or explicit integration authorization.
- **B — published product contract:** official help or rules for the supported
  customer workflow, but not a machine interface.
- **C — deployed implementation evidence:** publicly served first-party
  frontend code; useful for observing the current website, but unversioned and
  neither a compatibility promise nor authorization for another client.
- **D — negative discovery evidence:** no relevant source found in the checked
  official documentation and site-search surfaces. This narrows confidence but
  cannot prove that no private or separately negotiated interface exists.

## Refreshed finding

No stable, published, or explicitly authorized programmatic contract was found
for ticket list, detail, create, reply, attachments, status, pagination, or
download.

The strongest evidence is the current [REG.API 2 reference][regapi] (**A**).
Its documented method inventory contains no support/ticket category or
function. The current [REG.API introduction][regapi-intro] still directs
programmers to that reference; its broad description of access to cabinet
functions does not supply missing support operations, schemas, or
authorization.

REG.RU does publish a supported human workflow (**B**). The current
[support-contact guide][contact] says to create a request through the support
form and says that requests are collected under **Заявки в поддержку**. It also
says support accepts contacts only through the methods listed there. The
[cabinet guide][cabinet] says that section stores existing requests and can
create a new one. The current [support-service rules][rules] define the ticket
system as sending and receiving requests through an electronic form in the
customer account panel. These sources establish the website journey, not a
wire contract.

The deployed frontend evidence is unchanged in the material respects checked
(**C**):

- The support page still serves the same content-hashed
  [support-request bundle][support-bundle] cited by the earlier research
  (`knowledge-base-main.1d65b0a5ec7920d5b7e6.js`, last modified 2026-07-02).
  It exposes private relative routes for temporary upload/removal, ticket
  creation, notification toggling, service lookup, phone data, and alerts.
- The current account build still serves the same [account main bundle][account-main],
  [GraphQL transport bundle][account-gql], and [support-card chunk][account-support]
  checked in the earlier report. The visible support data remains aggregate
  new/all-ticket counts plus a website link to `/support/tickets/index`, not
  ticket records or a typed support API.
- No list/detail/conversation query, reply mutation, status-transition
  mutation, ticket-pagination schema, or attachment-download contract was
  found in those inspected public assets. This is not proof that REG.RU has no
  backend for them; it means no contract is available from this evidence.

A fresh search limited to first-party REG.RU properties found no dedicated
support API reference, OpenAPI document, published authentication method, or
explicit permission for programmatic use of the private `/support/...`
website routes (**D**).

## Operation-level result

| Operation | Published or explicitly authorized machine contract | Best current evidence |
| --- | --- | --- |
| List / pagination | **Not established** | Human Open/Closed/All UI and aggregate counts only (**B/C**) |
| Detail / conversation | **Not established** | Opaque website route exists; no identifier or response contract (**C**) |
| Create | **Not established** | Private multipart website route; no idempotency, retry, compatibility, or authorization contract (**C**) |
| Reply | **Not established** | Human message exchange is supported; no machine operation was found (**B/D**) |
| Upload / attach / download | **Not established** | Private temporary upload flow for create; UI limits and opaque `file_id`; no reply/download contract (**C**) |
| Read / change status | **Not established** | Human Open/Closed views; no wire enum or transition graph (**B/D**) |

The absence of an explicit authorization is distinct from an explicit
prohibition: the checked sources do not prove that REG.RU forbids a private
client. They also do not provide the authorization and stability needed to
promise one as a supported `regru` capability.

## What changed

No material contract change was found relative to
[Research: characterize private support operation contracts](https://github.com/adinvadim/reg-ru-cli/issues/20).
The support homepage and help content have been rebuilt or refreshed, but the
support-request and relevant account asset hashes, observed routes, aggregate
queries, and documented human workflow remain the same. No new published API
surface closes any of the earlier gaps.

## Input to the human boundary decision

The refreshed evidence supports treating **safe browser handoff** and
**automation blocked pending a published or explicitly authorized contract**
as the two evidence-aligned promises today. They differ mainly in product
positioning: the first is a deliverable user journey; the second is the trigger
for reconsidering automation.

The evidence does not support presenting an experimental private adapter as a
stable or provider-authorized capability. If the human owner deliberately
accepts that option, the risk acceptance should be explicit: a separately
labelled, default-off adapter; no blind retries after an ambiguous mutation;
no stability promise; and a named owner for provider authorization, drift
maintenance, and incident handling. This report supplies the contract facts
and risk asymmetry; it does not choose among the product options.

[regapi]: https://www.reg.ru/reseller/api2doc
[regapi-intro]: https://help.reg.ru/support/partneram/reg-api/chto-takoye-reg-api-i-kto-mozhet-ispolzovat-reg-api
[contact]: https://help.reg.ru/support/lichnyy-kabinet/registratsiya-i-kontaktnyye-dannyye/kak-svyazatsya-so-sluzhboy-podderzhki
[cabinet]: https://help.reg.ru/support/lichnyy-kabinet/registratsiya-i-kontaktnyye-dannyye/znakomstvo-s-lichnym-kabinetom-reg-ru
[rules]: https://img.reg.ru/faq/pravila_obsluguvania_rules-of-service-support_11082025.pdf
[support-bundle]: https://help.reg.ru/dist/knowledge-base-main.1d65b0a5ec7920d5b7e6.js
[account-main]: https://www.reg.ru/user/account/index.82f5f8db7d99ba5ed418.js
[account-gql]: https://www.reg.ru/user/account/4229.37275d176a2e742ac00c.js
[account-support]: https://www.reg.ru/user/account/1144.0ec38cc04acd1ff94414.js
