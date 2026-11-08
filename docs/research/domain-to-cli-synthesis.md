# Domains: to-cli synthesis

Sources: `domain-regapi-surface.md` (REG.API), `domain-availability.md` (registries, IDN, candidates), `domain-purchase-actions.md` (2026-11-07 manual purchase, safety). Goal: `regru domain` finds a free name and buys it in one step, NS on NIC.RU from the start.

## Capability matrix

| Capability | Source | Status | Paid | Auth | Read/action | Direct/crawl | Risk | Confidence |
|---|---|---|---|---|---|---|---|---|
| IDN normalise + round trip | `golang.org/x/net/idna` (already in go.mod) | official lib | free | none | local | direct | wrong punycode (2026-11-07) | high |
| Registry availability .ru/.рф/.su | TCI WHOIS `whois.tcinet.ru:43` | official, 120 req/min/IP, ban 1 h | free | none | read | direct | false free on unknown output | high |
| Registry availability .com/.net | Verisign RDAP, 404 = miss | official | free | none | read | direct | empty 404 body | high |
| Registrar availability | REG.API `domain/check` | official, **partners only** | free | regapi | read | direct | `RESELLER_AUTH_FAILED` for client accounts | medium |
| Prices reg + renew | REG.API `domain/get_prices show_renew_data=1` | official, clients | free | regapi | read | direct | promo vs tariff price unknown | medium |
| Balance | REG.API `user/get_balance` | official | free | regapi | read | direct | — | high |
| Identification state | REG.API `user/get_persons` `esia_identified` | official | free | regapi | read | direct | field semantics unverified | medium |
| Buy | REG.API `domain/create` with `profile_type`+`profile_name`, `nss`, `user_servid` | official, clients | **paid from balance** | regapi | action | direct | no refund; `NOT_ENOUGH_MONEY` creates no order | high |
| Reconcile buy | `service/get_info` by `user_servid`, balance delta, WHOIS | official | free | regapi | read | direct | `USER_SERVID_IS_NOT_UNIQUE` proves dispatch | high |
| Set NS later | REG.API `domain/update_nss` | official | free | regapi | action | direct | `ESIA_IDENTIFICATION_REQUIRED` | high |
| Top-up | existing `regru billing checkout` (visible browser, SBP) | private portal | owner pays | portal | human | direct | owner step | high |
| NIC zone + A records | `nic-dns` + raw NIC API for zone create | official NIC API | tariff slots | NIC OAuth | action | direct | 12/12 slot limit | medium |
| Owned domains list | REG.API `service/get_list servtype=domain` | official | free | regapi | read | direct (small collection, no archive) | — | high |

## Decisions the evidence settles

- Search never needs an account: registry first; `likely-free` until the registrar confirms; unknown is never free.
- Buy uses REG.API, not the portal cart: no upsells, no saved-card page, payment strictly from balance.
- NS go into `domain/create` itself, so the separate NS change (gated by Gosuslugi) is not needed for a new domain.
- `user_servid` generated before dispatch makes the buy reconcilable; no blind retry.
- Domains are a small current-state list: no crawlkit archive (deferred, named: `domain list` history).

## Open, needs the owner

1. The personal account has no `regapi` credentials configured; REG.API password and IP allowlist must be set (credentials exist in 1Password per 2026-11-07 logs; IP sync runs on `auth login/refresh`).
2. Spending policy for an agent buying without a prompt.
3. Contact profile name on the personal account (web-UI created; `RU.PP`).
4. Whether NIC zone creation joins `regru` or stays in the skill via `nic-dns`.
