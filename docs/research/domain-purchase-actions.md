# Domain purchase: actions, failure semantics, and the 2026-11-07 manual run

Research date: 2026-11-08. Scope: what `regru domain buy` must do end to end
(search → quote → buy → set NS → NIC zone → delegation → verify), how each
step fails, how an ambiguous outcome is reconciled, which steps need the human,
and what confirmation policy keeps an agent from overspending. The REG.API
contract details live in [domain-regapi-surface.md](domain-regapi-surface.md)
and availability in [domain-availability.md](domain-availability.md); this
note links to them instead of repeating them.

## Evidence boundary

Primary evidence is the agent transcripts of the real purchase of
`domkray.ru` on 2026-11-07: the orchestrating Claude Code session and six
delegated Codex Computer Use sessions (research, work-account attempt, NIC
attempt, personal-account attempt, SBP top-up preparation, cart preparation,
payment + identification + NS). Timestamps below are **UTC as recorded in the
logs** (MSK = UTC+3); the brief's "17:13–18:55 MSK" window is the same window
in UTC. Secondary evidence is this repository (README, `docs/cli-contract.md`,
`docs/billing.md`, `internal/cli/runtime.go`, `internal/provider/regapi`,
commit `006bbd7`), the `nic-dns` CLI and skill, and the public REG.API 2
reference. Nothing was bought, changed or logged into for this note.

Redacted throughout: passport data, birth date, address, card masks, account
e-mails, 1Password item IDs, payment/SBP/PayMaster URLs and tokens. Order
numbers are kept.

## What actually happened on 2026-11-07

The short version: the domain was bought in one sitting, but only on the
third account tried, only after a human SBP top-up, with three separate human
"yes" moments (payment click, Gosuslugi data consent, NS-change warning), and
the identification that REG.RU warned "can take up to three working days"
completed in about 90 seconds. Delegation to NIC was visible in public DNS
about 30 minutes after the NS change was saved.

| UTC | Event |
| --- | --- |
| 17:13 | Owner asks where to buy `domkray.ru` and put it on NIC DNS. Registry WHOIS (TCI) shows it free. Research agent finds REG.RU public price 169 ₽ (strikethrough 226 ₽) for .ru, NIC promo 219 ₽ with published standard renewal ~3 075 ₽ for individuals. It also catches a wrong punycode in the brief for the defensive `.рф` name. NIC portal session had expired and the NIC CLI failed on a missing Python dependency. |
| 17:35 | Owner: "buy the domain and set it up". `regru` has no domain commands; `regru --account work billing balance` returns only CloudVPS (`REG.API balance is unavailable`). |
| 17:37–17:41 | Agent 1 (work REG.RU account, Chrome): the cart auto-added a second domain (`domkray.online` "for 1 ₽"); agent removed it, cart = 169 ₽. Login then failed: «Неверная почта/логин или пароль». The 1Password password for the work account was stale. |
| 17:42–17:44 | Orchestrator tries REG.API directly with the work account's `regapi.*` fields: `user/get_balance`, `domain/check`, price → all `ACCESS_DENIED_FROM_IP`, from the Mac, from the Coolify host and from the production server. `regru --account work auth status` → `browser_session_interrupted`, so the egress-IP sync of commit `006bbd7` (which only runs after a successful `auth login`/`auth refresh`) never had a chance to whitelist anything. |
| 18:15–18:17 | To make room on NIC: the DNS-hosting service `DP2510072361` (tariff DNS-master L+) was at `domains-num 12 / domains-limit 12`. Orchestrator backed up the dead `novostroy.ai` zone (its live NS are on Cloudflare) to XML, deleted it, created zone `domkray.ru` via raw NIC API `PUT /services/{svc}/zones/domkray.ru`, then `nic-dns add … A @` and `A www` → Coolify host, `nic-dns commit`. The new zone's NS RRs were exactly `ns3-l2, ns4-cloud, ns4-l2, ns8-cloud, ns8-l2 .nic.ru`. `dig A @ns*.nic.ru` returned empty for at least 60 s after commit (and still empty at 18:52, see Unknowns). |
| 18:18–18:20 | Agent 2 (NIC portal, contract login): «Введен некорректный логин или пароль»; by e-mail: «Некорректный договор». Stale password again. |
| 18:21 | Orchestrator tries the personal account's REG.API fields: again `ACCESS_DENIED_FROM_IP`. |
| 18:21–18:22 | Agent 3 (personal REG.RU, Chrome): login OK, admin profile present, price 169 ₽, **balance 0 ₽** → stopped before checkout. |
| 18:24–18:26 | Agent 4 prepared (did not pay) a 300 ₽ balance refill: Balance → refill → amount → method SBP → **unticked "remember payment method"** → one-off SBP QR + link (refill order 223504751, PayMaster cashier, no expiry shown). Orchestrator relayed QR to the owner. |
| 18:31 | Owner: "Готово". |
| 18:31–18:33 | Agent 5: balance shows 300 ₽. Search `domkray.ru` → cart. Cart cleanup took ~12 clicks: an auto-added extra domain (`domkray.online` 1 ₽), a pre-selected «Рег.решение "Домен плюс"» bundle, «Антивирус для сайта» 1 ₽, «Хостинг Хост-0» 1 ₽ trial, «Конструктор сайтов» 1 ₽ trial, «Пакет "Старт"» 359 ₽. One mis-click flipped the order to «Сайт под ключ. Старт» at **10 940 ₽** before it was reverted. Final cart: `domkray.ru`, 1 year, 169 ₽. Existing admin profile selected; it showed «Не пройдена идентификация данных». Agent stopped before «Оплатить» because its Computer Use policy demands confirmation immediately before a financial action. |
| 18:35 | Owner: «оплачивай 169». The confirmation reached the orchestrator, not the agent, so the agent stopped again; the orchestrator relayed the exact words into the agent's thread. |
| 18:36:37 | «Оплатить» **creates the order immediately** («Создание счёта в процессе»), then shows a payment-method page for order **223505019**, 169.00 ₽: two saved cards, SBP, bank card, YooMoney, SberPay, cash, «Баланс личного кабинета». The card/SBP rows carry a **pre-ticked** «Запомнить карту для оплаты и автопродления услуг». Agent chose balance; no further confirm screen. |
| 18:36:52 | Registry `created` timestamp. Page: «Заказ №223505019 оплачен». Balance 300 → 131 ₽. **Payment and registration succeeded with the administrator unidentified.** |
| 18:37 | Domain card: «Активна. Требуется пройти идентификацию», paid till 07.11.2027, **auto-renew already on** («Следующее автопродление 24.10.2027», needs card or balance). REG.RU's free zone on `ns1/ns2.reg.ru` was pre-filled with parking `A @`/`A www` → a REG.RU IP. Renewal price was not shown anywhere the agent looked. |
| 18:37 | Opening «DNS-серверы» → modal «Необходимо подтверждение данных»: without Gosuslugi identification, «продление домена; управление делегированием; изменение контактных данных администратора; перенос домена к другому регистратору; аннулирование домена» will be blocked; «рассмотрение заявки … может занимать несколько дней». Buttons: «Пройти идентификацию» and **«Принять риск и продолжить»**. The agent treated this as a block (its brief said not to work around identification) and went to identification. |
| 18:37–18:40 | Identification page: account-owner data equals admin data, so identifying the account owner identifies the admin automatically. Redirect to Gosuslugi (`reg.ru/auth/esia-redirects/initialize?actionName=identification…`). Login/password from 1Password accepted; Gosuslugi then asked for a one-time code. Agent stopped. |
| 18:40 | Owner pointed to the TOTP stored in the same 1Password item; agent entered it. |
| 18:41 | Gosuslugi consent screen «Разрешение на действия с персональными данными» for «REG.RU Домены и хостинг»: registration/residence address, birth date, full name, identity document, e-mail, mobile phone; all-or-nothing; revocable in Gosuslugi → Согласия и доверенности → Разрешения. Agent stopped again (Computer Use requires confirmation right before granting personal-data access). |
| 18:48 | Owner: «разрешаю REG.RU доступ». Consent granted → return to REG.RU showed «Возникла ошибка» (`Unauthorized`). Retry from the identification page succeeded: «Идентификация запущена … обычно занимает до трех рабочих дней». |
| 18:49:55 | Contacts tab already said «Данные администратора домена идентифицированы через Госуслуги». ~90 s, not days. |
| 18:50 | NS form: presets (`ns1/ns2.reg.ru`, hosting, VPS), «Свой список DNS-серверов», «Не указывать DNS-серверы — домен будет разделегирован». Minimum two; five entered. «Продолжить» → modal «Изменить DNS-серверы? Обновление DNS может занять до 24 часов. В это время домен может быть не работоспособен.» Agent stopped once more. |
| 18:51 | Orchestrator confirmed on the owner's standing instruction (domain unused, change was the goal). Saved: «Внесены изменения. Обновление DNS может занять до 24 часов.» Panel lists the five NIC servers and says records must be managed at that provider. |
| 18:52 | TCI WHOIS (snapshot «Last updated 18:48:02Z») still `nserver ns1/ns2.reg.ru`, `state REGISTERED, DELEGATED, UNVERIFIED`, `paid-till 2027-11-07T18:36:52Z`. `dig +short NS domkray.ru` and `dig A @ns3-l2.nic.ru` both empty. |
| ≤19:24 | Background poll (60 s interval, `dig A @8.8.8.8`) exited: A = Coolify IP from 8.8.8.8, 1.1.1.1 and 77.88.8.8; NS = the five NIC servers; WHOIS `REGISTERED, DELEGATED, VERIFIED`. Upper bound ~33 min from save to public resolution. HTTPS and the site move followed. |

Root causes worth carrying into the design:

- **Stale credentials, not provider outages, killed two of three registrars.**
  Both the work REG.RU and NIC passwords in 1Password were rejected. A CLI
  that depends on a portal login should run a read-only `auth status` as a
  precondition and report `authentication_expired` before any order is built.
- **REG.API never worked from any host** because no egress IP was
  allowlisted. The `006bbd7` sync only runs on a successful `auth login` or
  active `auth refresh`; with the work session broken and the personal profile
  not used by `regru`, nothing ever synchronized. `ACCESS_DENIED_FROM_IP` is
  classified as an authentication failure in `regapi.APIError`, which is
  right for exit codes but hides the fix: the hint should say "run
  `regru auth refresh` to sync the REG.API allowlist".
- **The money step was human only because the balance was 0 ₽.** Paying from
  balance itself was one click with no extra REG.RU confirmation.
- **Identification was not a precondition for buying**, only for managing
  delegation (and renewal, transfer, contact change, cancellation). The gate
  presented as a warning with an "accept risk" button, consistent with REG.RU
  stating current rules apply until it is in the registrar list or
  18 January 2027 (see the sibling note, §5).
- **The cart is adversarial.** Bundles and 1 ₽ trials (that renew at full
  price) are pre-selected, and a single wrong click took the total to
  10 940 ₽. Any portal-driven purchase must verify the final line items, not
  just the domain line.

## End-to-end action list

Legend: **$** financial, **D** destructive or hard to undo, **H** needs the
human. "API" means REG.API 2 from an allowlisted IP; "portal" means the
browser-backed session; "nic" means NIC DNS-hosting API (`nic-dns` + raw calls).

### 1. Preflight (read-only)

Surface: `regru auth status` (portal) + REG.API `user/get_balance` +
`user/get_persons` (ESIA flag) + NIC `GET /services` (zone slots).
Preconditions: selected account, credential process, allowlisted IP.
Failure modes seen: `authentication_expired`/wrong password (17:41, 18:20),
`browser_session_interrupted` (17:42), `ACCESS_DENIED_FROM_IP` (17:44, 18:21),
NIC CLI missing dependency (17:16), NIC service full (`domains-num ==
domains-limit`). Reconcile: none needed. Human: only to fix stale
credentials or complete a 2FA login. The CLI should fold these into one
`domain buy --dry-run` readiness report (balance, ESIA status, IP allowlist,
NIC slot, auth state) so the agent learns all blockers in one call rather
than one per delegated browser agent, which is what cost an hour on 11-07.

### 2. Search / availability

Surface: TCI WHOIS (anonymous) as primary; REG.API `domain/check` is
partner-only (`RESELLER_AUTH_FAILED` for clients); portal search as fallback.
Not $/D. Failure modes: WHOIS rate limits, IDN/punycode mistakes (the brief
on 11-07 had a wrong punycode for the `.рф` name), "free in registry but
reserved/premium at the registrar". Reconcile: n/a. Human: no. Details in
[domain-availability.md](domain-availability.md).

### 3. Quote

Surface: REG.API `domain/get_prices` (registration and `renew_price`) or
portal price. Not $/D. Failure: promo price differs from list price
(REG.RU showed 226 → 169 ₽; NIC 219 ₽ first year vs ~3 075 ₽ renewal), and
the renewal price was **never confirmed** on 11-07 — neither the cart nor the
domain card showed it. Requirement: the quote must carry both first-year and
renewal price, and buy must refuse if renewal is unknown unless an explicit
`--allow-unknown-renewal` is given. Human: no.

### 4. Ensure funds (top-up)  **$ H**

Surface: portal only (Balance → refill → SBP). REG.API has no refill method;
the existing `billing invoice payment-link` deliberately opens a visible
browser and never returns a URL (see `docs/billing.md`). On 11-07 the
SBP link/QR was produced by a browser agent and relayed to the owner.
Preconditions: portal session; amount ≥ quote (11-07 used 300 ₽ for a
169 ₽ domain). Failure modes: no expiry shown on the SBP link; the refill
form defaults to "remember payment method" (agent unticked it); a pending
refill order exists until paid. Reconcile: re-read `user/get_balance` and
look for the refill order in invoice history; never re-create a refill
because the first one "seems lost". Human: the payment itself, always. The
agent may prepare the QR; it must not pay, store a card or enable
auto-payment.

### 5. Buy  **$** (and effectively **D**: no refund path)

Surface, preferred: REG.API `domain/create` with `pay_type=prepay`, a saved
`profile_type`/`profile_name` (profiles can only be created in the web UI),
NS at order time, and a pre-generated `user_servid` (32 hex) persisted
locally **before** dispatch. Fallback: portal cart (what 11-07 used).
Preconditions: quote within caps, balance ≥ price, existing admin profile,
allowlisted IP.

Failure modes:

- API: `NOT_ENOUGH_MONEY` (no order created unless `ok_if_no_money`; never
  set that flag), `DOUBLE_ORDER` / `DOMAIN_ALREADY_ORDERED` (an unpaid order
  exists), `USER_SERVID_IS_NOT_UNIQUE` (our earlier attempt landed),
  `BILLING_LOCK` (retryable), `Unavailable domain name`, transport error or
  5xx after dispatch (the existing client already maps these to
  `AmbiguousMutationError` → `outcome_unknown`, exit 10).
- Portal: «Оплатить» **creates an order/bill before payment**, so an
  interruption between 18:36:37 and 18:36:52 leaves an unpaid order; the
  method page offers saved cards first and a pre-ticked "remember card for
  auto-renew"; there is no second confirmation after choosing «Баланс».
  Upsells change the total silently.

Reconcile an ambiguous buy: query `service/get_info` by `user_servid` (or
`domain/nop`/`service/get_list` by name), then `bill/nop` on the returned
`bill_id`, then `user/get_balance` delta, then TCI WHOIS `registrar` and
`created`. Only if all say "nothing happened" may the CLI retry, and the
retry reuses the same `user_servid` so a landed first attempt fails with
`USER_SERVID_IS_NOT_UNIQUE` instead of buying twice. For the portal path the
equivalent is the account's unpaid-bill list (`billing invoice list`) plus
the domain list; an unpaid order for the name must be paid or deleted, not
duplicated.

Post-conditions to verify and report: order/bill id, amount charged, balance
after, service state `N → A` (registration is an application), paid-till,
**auto-renew flag** (on by default on 11-07), renewal price.

Human: the first-year spend is fine for an agent under the caps below. On
11-07 the human was needed only because the Computer Use tool insists on
in-thread confirmation; a CLI with `--max-price` makes that unnecessary.

### 6. Identification (Gosuslugi/ESIA)  **H**

Surface: portal + Gosuslugi only; no API to perform it. API exposes status:
`user/get_persons[].esia_identified`, `service/get_details` `id_state`.
Preconditions: account-owner data equals admin data (then one identification
covers all the owner's domains). Failure modes: Gosuslugi 2FA (one-time
code), all-or-nothing personal-data consent, REG.RU «Возникла ошибка» /
`Unauthorized` on the way back (retry from the identification page worked),
REG.RU's "up to three working days" text (actual: ~90 s). Reconcile: read
`esia_identified` / `id_state` / the contacts tab; never re-grant consent
blindly. Human: always — logging into Gosuslugi and granting personal-data
access are the owner's acts even when credentials are in 1Password. The CLI
should detect "not identified", print the identification URL
(`https://www.reg.ru/user/account/domain_identification`), and stop with a
distinct code (e.g. `identification_required`, exit 4).

### 7. Set NS at REG.RU  **D**

Surface: REG.API `domain/update_nss` (`ESIA_IDENTIFICATION_REQUIRED`
documented) or at order time in `domain/create`; portal «DNS-серверы →
Свой список». Preconditions: target zone already exists and answers at the
target NS (step 8 first). Failure modes: identification gate (soft modal
with «Принять риск и продолжить» on 11-07; hard API error documented),
"up to 24 h, domain may be unavailable" warning, `INVALID_NSS`, the
**«Не указывать DNS-серверы» option undelegates** the domain, and REG.API
individual params only go to `ns0..ns3` (four) while 11-07 used five NIC
servers (the `nss` hash may allow more — unverified). Reconcile: `domain/get_nss`
read-after-write; compare as a set ignoring trailing dots and order. Human:
no for a freshly bought, unused domain; yes if the domain already serves
traffic (the 24 h warning is real there).

### 8. Zone at NIC  (slot management is **D**)

Surface: NIC DNS-hosting API. `nic-dns` covers `records/add/delete/commit`
only; its `zones` command is stubbed and there is **no create-zone or
delete-zone command** — on 11-07 both were raw `api_request("PUT"|"DELETE",
"/services/{svc}/zones/{zone}")` calls. Records: `nic-dns add <zone> A @
<ip> 3600`, `nic-dns add <zone> A www <ip> 3600`, `nic-dns commit <zone>`
(uncommitted changes are not served). Which NS to give REG.RU: read them from
the new zone's own NS RRs rather than hard-coding; on 11-07 they were
`ns3-l2.nic.ru, ns4-l2.nic.ru, ns8-l2.nic.ru, ns4-cloud.nic.ru,
ns8-cloud.nic.ru`. Preconditions: free slot in a service
(`domains-num < domains-limit`; `DP2510072361` was 12/12). Failure modes:
service full; deleting the wrong zone (11-07 verified `dig NS novostroy.ai`
pointed at Cloudflare and saved an XML backup to
`~/sandbox/data/nic-zone-backups/` first); token expiry; forgetting commit.
Reconcile: `GET /services/{svc}/zones` and `nic-dns records <zone>`. Human:
choosing which zone to delete to free a slot, or buying a bigger tariff
($). Creating a zone in a free slot is safe for an agent. Order matters: do
this before step 7 so the domain never delegates to servers that don't know
the zone.

### 9. Wait for delegation

Surface: TCI WHOIS + public resolvers. Not $/D. Observed: REG.RU accepted the
change at 18:51; WHOIS snapshot lagged (its own "Last updated" stamp); public
A answered by 19:24 at the latest. REG.RU's "24 h" is a ceiling, not the
norm. Failure modes: negative caching — `dig +short NS` returned empty at
18:52 even though the registry already had the domain delegated to REG.RU,
because earlier "is it free?" queries cached NXDOMAIN; stale WHOIS
snapshot. The CLI should not block on this; return `delegation: pending` and
offer `regru domain status <name> --wait` with a bounded timeout (exit 124 on
deadline) that polls WHOIS `nserver` and an authoritative query at each
target NS, not a recursive resolver.

### 10. Verify

`whois -h whois.tcinet.ru <name>` → `registrar REGRU-RU`, `nserver` = target
set, `state REGISTERED, DELEGATED, VERIFIED` (the `VERIFIED` flag flipped only
after identification), `paid-till`; `dig NS <name> @a.dns.ripn.net`-style
parent check; `dig A <name> @<each target NS>`; then public resolvers.
Report auto-renew state and renewal price.

## Which steps truly need the human

Only three acts are inherently the owner's: paying money into the account
(SBP/card top-up), authenticating to Gosuslugi with its second factor, and
granting REG.RU access to passport-level personal data. Everything else on
11-07 that waited for a human did so because of tool policy (Computer Use
asks before every financial click and before accepting a warning), stale
secrets, or a missing allowlist entry — all fixable in the CLI or in setup.
Choosing which NIC zone to sacrifice when the tariff is full is a judgement
call that should also go to the human.

## Recommended confirmation policy for `regru domain buy`

Keep the repository's existing contract — `--dry-run` makes zero provider
calls, mutations need a TTY `y` or `--force`, `--force` never bypasses
auth/capability/drift/timeout checks, ambiguous dispatch returns
`outcome_unknown` (exit 10) and is never auto-retried — and add explicit
money bounds that are checked against the live quote **after** the quote and
**before** dispatch:

- `--max-price <rub>`: first-year cap, required with `--force`. 11-07 would
  have used 300.
- `--max-renewal <rub>`: renewal cap, required with `--force`; buy fails
  closed if `renew_price` is unknown or above the cap. This is the trap the
  NIC option would have hit (219 ₽ → ~3 075 ₽).
- Exactly one domain line and zero extra services; any other line item, a
  premium flag (`reg_premium`), or a period other than 1 is a hard stop, not
  a prompt.
- Payment source is balance only (`pay_type=prepay`); never
  `ok_if_no_money`, never a card, never "remember card".
- Auto-renew: report it; do not change it silently. A separate, confirmed
  `domain autorenew` command can turn it off if wanted.
- Identification missing → stop with `identification_required` and the URL;
  do not click "accept risk" on the owner's behalf.
- NS change on a domain that already resolves to something other than the
  registrar's parking IP requires a fresh confirmation even with `--force`
  (opt-in flag such as `--replace-live-ns`).

A standing agent authorization can then be expressed as one command line,
for example `regru --account personal --no-input --force domain buy
domkray.ru --max-price 300 --max-renewal 500 --ns-from-nic DP2510072361`,
and the owner's "yes" is the cap values rather than a click relayed between
threads, which is what went wrong at 18:35.

## Unknowns

- Whether REG.RU would actually have changed NS for an unidentified admin
  after «Принять риск и продолжить», and whether REG.API `domain/update_nss`
  returns `ESIA_IDENTIFICATION_REQUIRED` today or only after 18 Jan 2027.
- Whether `domain/create` (API) behaves like the portal and accepts an
  unidentified admin, and whether NS can be set at order time for five
  servers (`ns0..ns3` caps individual params at four; `nss` hash limit
  unverified).
- The real renewal price of `domkray.ru` at REG.RU for this account; it was
  never displayed. Also whether auto-renew on 24.10.2027 would charge a
  saved card (two exist on the account) when the balance is short.
- Why the NIC authoritative servers returned empty answers for
  `domkray.ru` from 18:17 to at least 18:52 even though the zone was
  committed: propagation lag inside NIC, or NIC serving a zone only once it
  is delegated to it. This decides whether "verify zone before switching NS"
  is a usable precondition.
- Exact delegation latency: only the ~33 min upper bound is known (60 s
  poll against 8.8.8.8, possibly delayed by negative caching).
- Whether the SBP refill link/QR expires, and whether a pending unpaid refill
  order blocks anything.
- Why the Gosuslugi → REG.RU return first failed with `Unauthorized`; one
  retry fixed it, but the cause is unknown.
- Whether identification is always this fast; 11-07's ~90 s may be because
  the account owner was the admin and already had matching data.
- Whether REG.API keys for either account can be used at all until an
  allowlisted IP exists; the first successful `regru auth login`/`refresh`
  for the personal profile is the cheapest test.
