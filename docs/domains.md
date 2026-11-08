# Domain commands

`regru domain` finds a free name and registers it at REG.RU with nameservers
set in the same order, so a new domain can point at NIC.RU from the start.
Evidence and the provider contracts behind these rules live in
[docs/research/domain-to-cli-synthesis.md](research/domain-to-cli-synthesis.md).

## Commands

```text
regru domain search <word> [word...] [--zones ru,рф,com] [--limit 1..60]
regru domain check <name> [name...]
regru domain quote <name>
regru domain buy <name> --ns nic|<ns1,ns2,...> [--max-price RUB] [--max-renewal RUB] [--user-servid HEX32]
regru domain list
regru domain ns set <name> --ns nic|<ns1,ns2,...>
```

| Command | Account | Network | State change |
| --- | --- | --- | --- |
| `search`, `check` | optional | TCI WHOIS, Verisign RDAP; REG.API `domain/check` only when the account enables it | none |
| `quote` | required | REG.API `domain/get_prices show_renew_data=1` | none |
| `buy` | required | registry recheck, `domain/get_prices`, `user/get_balance`, `domain/create` | **paid from balance** |
| `list` | required | REG.API `service/get_list servtype=domain` | none |
| `ns set` | required | REG.API `domain/update_nss` | delegation |

`--ns nic` expands to `ns3-l2.nic.ru, ns4-l2.nic.ru, ns8-l2.nic.ru,
ns4-cloud.nic.ru, ns8-cloud.nic.ru`. A custom list takes 2–8 distinct
hostnames, sent as the `nss` hash `ns0`, `ns1`, …. REG.API documents only
`ns0`–`ns3`, so after a successful `buy` or `ns set` the CLI reads
`domain/get_nss` and reports `nameserversReported` and
`nameserversConfirmed`; a different set adds the `nameservers_mismatch`
warning, a failed read adds `nameservers_unverified`. The read never turns a
committed order into a failure.

## Names and IDN

A name is exactly one label (at least two characters in `.ru`, `.рф`, `.su`;
one is allowed in `.com`/`.net`) plus a supported zone: `ru`, `рф` (also `rf` or
`xn--p1ai`), `su`, `com`, `net`. URLs, subdomains, whitespace, and
`/@:?#%\` are usage errors (exit 2). Input is folded with the UTS #46 lookup
mapping, converted with the strict IDNA2008 registration profile, and must
round-trip exactly. `.ru`, `.su`, `.com`, `.net` labels are Latin LDH; `.рф`
labels are Cyrillic, digits, and hyphens.

Every result carries both forms (`домкрай.рф` and `xn--80ahqcjlq.xn--p1ai`).
A name typed as Punycode adds the `punycode_input` warning with its decoded
Unicode form, because a hand-made label such as `xn--80ajmfjh7a` is valid
Punycode for a different word. Only the ASCII form is sent to registries and
REG.API.

## Availability

States, in output order for `search`:

| State | Meaning |
| --- | --- |
| `free` | The registry has no record **and** REG.API `domain/check` answered `Available`. |
| `likely_free` | The registry has no record; the registrar did not confirm. |
| `taken` | The registry holds any matching record (including `pendingDelete`, reported as `record.pendingDelete`), or the registrar refused the name. |
| `unknown` | Timeout, rate limit, mismatch, or unrecognised reply. Never treated as free. |

Registry rules:

- `.ru`, `.рф`, `.su`: `whois.tcinet.ru:43`. A miss needs both the
  `% TCI Whois Service.` header and the exact line
  `No entries found for the selected source(s).`. At most two connections,
  at least 600 ms between connection starts, 5 s per query. Any refused
  connection, timeout, empty, mismatched, or unrecognised reply stops all TCI
  work for the run (the remaining names become `unknown` with
  `tci_stopped_after_unexpected_reply`), since TCI blocks an IP for an hour
  above 120 queries per minute.
- `.com`, `.net`: `https://rdap.verisign.com/{com,net}/v1/domain/<ascii>`.
  A miss is HTTP 404 with `Content-Type: application/rdap+json`; the body may
  be empty. Redirects are not followed. A 429 stops RDAP work for the run.
- Contact data and raw WHOIS text are never kept or printed.

Registrar confirmation is optional because `domain/check` is documented as
partner-only. It runs as one batched call for the `likely_free` names only
when an account is selected and its profile sets `registrar_check = true`.
Otherwise `registrar.reason` explains why it did not run
(`no_account_selected`, `registrar_check_disabled`,
`registrar_check_partner_only`, `registrar_authentication_failed`,
`registrar_unavailable`, `credentials_unavailable`).

`search` generates candidates deterministically: each Cyrillic word is
transliterated with the primary table plus spellings that differ by one
well-known substitution (`й` → `y|i|j`, `кс` → `ks|x`, `ы` → `y|i`, …, at
most four per word form); the caller can add forms with `a|b`. Words are
joined and, for two or more words, hyphenated. `.рф` receives only all-Cyrillic
input as typed. Ranking: the exact primary brand in the first zone, then zone
order, unhyphenated before hyphenated, fewer substitutions, shorter label, ASCII
order. The list is truncated to `--limit` (default 24) before any lookup.

A run where some names are `unknown` succeeds with `complete: false` and the
`availability_incomplete` warning. A run where every name is `unknown` fails
with `availability_unknown` (exit 6, retryable).

## Quote

`quote` returns `firstYear`, `renewal`, `retailFirstYear`, `retailRenewal`,
`minPeriod`, `currency` (REG.API `RUR`), and the spending-policy evaluation
against the account caps. REG.API exposes no promo field, so the quoted
first-year price may differ from the public site's promo price.

## Buy

`buy` is the only paid operation. Its order of work is fixed:

1. Validate the name and nameservers locally. `--dry-run` stops here with a
   plan (`domain`, `nameservers`, `period`, `payment`, caps given as flags) and
   makes no credential, registry, or REG.API call.
2. Require `contact_profile_type` and `contact_profile_name` in the account
   profile (`configuration_error`, exit 3, otherwise).
   With `--user-servid`, first look that ID up with `service/get_info`: a
   service for this name is reported as committed (`reconciled: true`) without
   any further check or order; a service for another name refuses with
   `spending_policy_refused` / `user_servid_conflict`; an unused ID continues.
3. Re-check the registry. `taken` fails with `domain_unavailable`; `unknown`
   fails with `availability_unknown`. Nothing is sent to REG.API.
4. Read the live zone price. A minimum period above one year refuses.
5. Apply the spending policy (below).
6. Read `user/get_balance`. A short balance fails with `insufficient_funds`
   before dispatch.
7. Send `domain/create` once with `period=1`, `profile_type`, `profile_name`,
   `nss` (`ns0`…), a 32-hex `user_servid`, and `pay_type=prepay`.
   `ok_if_no_money` and `reg_premium` are never sent, so a premium or unpaid
   order cannot be created.

### Spending policy

| Live quote | Result |
| --- | --- |
| first year ≤ `max_price` and renewal ≤ `max_renewal` | proceeds without a prompt, also with `--no-input` |
| either price above its cap | `price_cap_exceeded` (exit 4) unless `--force`; with `--force` it proceeds and warns `spending_cap_overridden` |
| renewal or first-year price missing | `spending_policy_refused` (exit 7); `--force` does not override |

Caps come from `--max-price`/`--max-renewal`, then the account profile, then
the defaults 500 and 1500 RUB. Amounts are decimal roubles with at most two
decimals.

### Failures and reconciliation

| Situation | Code | Exit |
| --- | --- | ---: |
| `NOT_ENOUGH_MONEY`, or balance below price | `insufficient_funds` with `missing_amount`; no order exists. Top up the balance in the REG.RU cabinet (SBP) and rerun. | 4 |
| `ESIA_IDENTIFICATION_REQUIRED`, `NOT_ESIA_IDENTIFIED` | `identification_required` with `url` = `https://www.reg.ru/user/account/domain_identification`; only the owner can complete Gosuslugi identification | 4 |
| per-item refusal (`Unavailable domain name`, stop list, …) | `domain_unavailable` with `provider_code` | 6 |
| `ACCESS_DENIED_FROM_IP` | `authentication_expired`; run `regru auth refresh` to sync the REG.API allowlist | 5 |
| transport error, 5xx, undecodable success, timeout, or Ctrl-C after dispatch | reconciliation, see below | 0 or 10 |

After an ambiguous dispatch the CLI never resends `domain/create`. It looks
the order up with `service/get_info` by `user_servid` (bounded to 20 s, even
after Ctrl-C). A found service for the same name is reported as committed with
`reconciled: true`; a service for another name is `user_servid_conflict`.
When `service/get_info` omits the name, `domain/nop` resolves the requested
name to its service ID for the comparison; if that fails the result stays
`outcome_unknown`. Anything else returns `outcome_unknown` (exit 10) with
`details.evidence`: `userServid`, `serviceLookup` (`not_found` or `failed`),
`balanceBefore`, `balanceAfter`, `balanceChanged`, and `registryState`. Check
`regru domain list` before retrying, and retry only with
`--user-servid <same value>`: the ID is looked up before anything else, so a
landed first order is reported instead of bought twice.

`buy` targets ordinary REG.RU client accounts: it does not send
`enduser_ip`, which REG.API requires only from partners, so partner purchases
are not supported. Registration is an application: the service starts in state `N` and becomes
`A` later. NIC.RU zone creation and records stay outside this CLI (`nic-dns`).

## List and nameservers

`list` returns the current `service/get_list` domain services: name, state,
expiration date, service ID. There is no local archive.

`ns set` replaces the delegation with `domain/update_nss`. It requires TTY
confirmation or `--force`, supports `--dry-run`, and maps the ESIA gate to
`identification_required`. An ambiguous result is `outcome_unknown`; compare
with the registry `nserver` lines before repeating it.

## Account profile settings

```toml
[accounts.personal.domain]
contact_profile_type = "RU.PP"     # GTLD, EU, TJ, RU.PP, RU.ORG, NEWTCI.ORG, NEWTCI.PP
contact_profile_name = "Owner"     # saved contact profile name from the REG.RU web cabinet
max_price = "500"                  # first-year cap, RUB
max_renewal = "1500"               # renewal cap, RUB
registrar_check = false            # true only for partner accounts where domain/check works
```

All fields are non-secret routing metadata, user-profile only, and never
rendered by `account show` or `capability list`. Type and name are set
together.

## Capabilities

`capability list` reports `domain.search` as always configured,
`domain.quote`, `domain.list`, and `domain.ns` when REG.API credentials are
routed, `domain.buy` when REG.API credentials and a contact profile are both
configured, and `domain.registrar_check` when the profile also sets
`registrar_check = true`.

## Examples

```sh
regru domain search кубань "квартал|кварталы"
regru domain search domkray --zones ru,su --limit 6 --plain
regru domain check domkray.ru домкрай.рф --json
regru --account personal domain quote domkray.ru
regru --account personal --dry-run domain buy domkray.ru --ns nic
regru --account personal --no-input --json domain buy domkray.ru --ns nic --max-price 300
regru --account personal --no-input --json domain buy domkray.ru --ns nic --user-servid 0123456789abcdef0123456789abcdef
regru --account personal domain list --plain
regru --account personal domain ns set domkray.ru --ns nic
```
