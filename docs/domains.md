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
regru domain rereg catalog fetch
regru domain rereg catalog search [pattern] [--date YYYY-MM-DD|today|tomorrow] [--zones ru,рф,su] [--registrar R] [--with-bids] [--limit 1..10000]
regru domain rereg quote <name>
regru domain rereg bids
regru domain rereg bid <name> --price RUB --max-price RUB --contact profile|credential --ns nic|<ns1,...> [--over-limit] [--allow-release-window]
```

The `rereg` commands cover the REG.RU backorder of releasing names; see
[Backorder (rereg)](#backorder-rereg).

| Command | Account | Network | State change |
| --- | --- | --- | --- |
| `search`, `check` | optional | TCI WHOIS, Verisign RDAP; REG.API `domain/check` only when the account enables it | none |
| `quote` | required | REG.API `domain/get_prices show_renew_data=1` | none |
| `buy` | required | registry recheck, `domain/get_prices`, `user/get_balance`, `domain/create` | **paid from balance** |
| `list` | required | REG.API `service/get_list servtype=domain` | none |
| `ns set` | required | REG.API `domain/update_nss` | delegation |
| `rereg catalog fetch` | none | public `rereg_extlist.csv` through a headless browser | local snapshot only |
| `rereg catalog search` | none | none (local snapshot) | none |
| `rereg quote` | optional | public lot page and `blitz_info`; with an account REG.API `domain/get_user_rereg_bids`, `user/get_balance` | none |
| `rereg bids` | required | REG.API `domain/get_user_rereg_bids` | none |
| `rereg bid` | required | REG.API `domain/get_user_rereg_bids`, public lot page, `user/get_balance`, `domain/set_rereg_bids` | **charged from balance at once** |

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
transliterated with the primary table plus spellings that differ from the
primary phrase by exactly one well-known substitution (never combined across
words) (`й` → `y|i|j`, `кс` → `ks|x`, `ы` → `y|i`, …, at
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

## Backorder (rereg)

REG.RU runs an auction for `.ru` and `.рф` names from every registrar (and
`.su` names it services) that are about to be released by the registry. A name
appears at 08:00 MSK the day after it expires. A bid is a fixed ladder price
(observed: 450, 1950, 5950, 11950, … 10,000,000 RUB, VAT included); a higher
bid from someone else replaces ours and refunds it to the balance, and a bid at
the blitz price closes the lot. On the release day the blitz steps down every
15 minutes from 12:10 to 16:45 MSK. The last payment is accepted at 16:45 MSK;
REG.RU does not guarantee registration, and a failed lot is refunded.

### Public data and the anti-bot page

`www.reg.ru` answers plain HTTP clients with a JavaScript interstitial, so
`catalog fetch` and `quote` read public pages through a throwaway headless
Chrome/Chromium: a new temporary profile per command, no account, no portal
session, deleted on exit, only the `https://www.reg.ru` origin. A missing
browser fails with `missing_browser`; an interstitial that does not clear
within 20 seconds is a retryable `network_error` with
`details.reason = anti_bot_challenge`; an unrecognised page is
`private_contract_drift` (exit 8).

- `catalog fetch` downloads `static_files/rereg_extlist.csv` (about 300,000
  rows, 17.5 MB; header `Домен;Ставки;Текущ.блиц,руб.;Перв.регистрация;Дата удаления;Регистратор`),
  validates the header, and atomically replaces the snapshot in
  `<UserCacheDir>/regru/rereg/` (mode 0600, with a SHA-256 checked on read).
  The file reveals only whether a name has bids, never the amounts. Rows that
  are not valid names under the CLI rules (for example Cyrillic `.su`) are
  counted as skipped with the `catalog_rows_skipped` warning. Its deadline is
  four times `--timeout`.
- `catalog search` filters the snapshot locally by substring, release date,
  zone, registrar, and bid presence, sorted by release date then name. It adds
  `catalog_stale` when the snapshot is more than 12 hours old.

### Quote

`quote` returns the shared dropcatch quote object in `data` (money in integer
kopecks, `null` when unknown, never `0` for unknown):

| Field | Source |
| --- | --- |
| `service`, `domain` | `regru`, the Unicode name (`name` adds both forms) |
| `releaseDate` | own bid `delete_date`, else the catalogue snapshot, else `null` |
| `state` | `ordered_by_me` (own bid), `releasing` (lot page), `not_listed` (REG.RU serves the general list instead), `unknown` |
| `steps[]` | the ladder; `available` between the next bid and the blitz (the zone blitz on the release day) |
| `nextBidKopecks` | own bid `next_price`, else the lot page `minPrice` |
| `currentTopKopecks` | own bid `max_bid`, else inferred as the ladder step below `minPrice` (with a note); `null` without bids |
| `competingBidVisible` | a top bid that is not ours |
| `myOrder` | `{id: ascii name, amountKopecks, status}`; status `leading`, `outbid`, `closed_leading`, `closed_outbid` |
| `bidDeadline` | 16:45 MSK on the release date (RFC 3339, `+03:00`); `auctionUntil` is always `null` |
| `balance` | `user/get_balance` prepay; `sufficient` compares it with the full next bid |
| `notes[]`, `sources` | caveats and which source answered |

Without an account `quote` uses public data only (`myOrder` and `balance` are
`null`). When one source fails the other still answers with a warning
(`public_lot_unavailable`, `own_bids_unavailable`, `balance_unavailable`);
when none answers the command fails. Its deadline is three times `--timeout`.

### Bids

`bids` returns `data` as an array of `{service, domain, name, id,
amountKopecks, status, active, maxBidKopecks, nextBidKopecks, blitzKopecks,
releaseDate, bidDeadline, registrar}`, including outbid lots, straight from
REG.API. REG.API drops a row after registration or renewal, so the final
outcome must be checked with `regru domain list` or WHOIS.

### Bid

`bid` sends one `domain/set_rereg_bids` and REG.RU charges the balance at
once. A paid bid cannot be cancelled. It requires TTY confirmation or
`--force` and supports `--dry-run` (plan: domain, price and max price in
kopecks, contact source, nameservers, override flags; zero credential,
browser, or network activity). The order of work is fixed:

1. Validate locally: name in `.ru`/`.рф`/`.su`, `--price` ≤ `--max-price`,
   `--contact`, `--ns`.
2. Guard window: between 16:55 and 17:15 MSK the bid is refused with
   `policy_refused` / `guard_window` (exit 9), also for `--dry-run`, unless
   `--allow-release-window`. The REG.API account may be shared with an
   automated buyer that calls `domain/create` at 17:00: a concurrent bid can
   cause its `BILLING_LOCK`, spends the shared 1,200 requests/hour, and 10
   identical invalid requests ban the account for an hour.
3. Caps: `--price` above `rereg_max_bid` (default 3000 RUB) or today's
   journaled spending plus `--price` above `rereg_max_daily` (default 10000
   RUB, Moscow calendar day) is `policy_refused` / `spend_limit_exceeded`
   unless `--over-limit`.
4. A per-account lock (`bid_in_progress`, retryable) serialises bids.
5. REG.API `domain/get_user_rereg_bids`: an own bid at or above `--price` is
   `existing_bid`, a closed lot is `lot_closed`; `--price` must be a step of
   the zone ladder (exit 2 lists the nearby steps).
6. The public lot page: a name REG.RU does not list is `not_listed`;
   `--price` below `minPrice` or above the blitz is exit 2. If the page cannot
   be read the bid continues with the `public_lot_unverified` warning.
7. The release date (own bid or catalogue snapshot): at or after 16:45 MSK on
   that day the bid is `deadline_passed` unless `--allow-release-window`. An
   unknown date adds `release_date_unknown`.
8. `user/get_balance`: a short balance is `insufficient_funds` (exit 4) with
   nothing sent.
9. Journal an intent, send once, journal the outcome.

`--contact profile` sends the account's `contact_profile_type` and
`contact_profile_name` (as `domain/create` does; REG.API does not document
whether `set_rereg_bids` accepts a profile, so a refusal such as
`CONTACTS_NOT_FOUND` is reported as a provider rejection with nothing charged).
`--contact credential` sends the `contacts` hash from the credential-process
field `rereg.contacts`: a JSON object of `domain/create` contact keys with
string values that must match a contact already verified through ESIA. It is
never written to output, the journal, or errors.

Outcomes:

| Situation | Code | Exit |
| --- | --- | ---: |
| Success | committed, with `billId`, `payment`, `serviceId`, `journalId`, `caps` | 0 |
| `NOT_ENOUGH_MONEY` | `insufficient_funds` | 4 |
| `NOT_ESIA_IDENTIFIED`, `ESIA_IDENTIFICATION_REQUIRED` | `identification_required` | 4 |
| `YOUR_SAME_OR_MORE_BID_FOUND`, `SAME_OR_MORE_BID_FOUND`, `INVALID_BID`, `REREG_NOT_FOUND`, `BILLING_LOCK`, per-item refusal | `network_error` with `provider_code`, `bid_created: false` | 6 |
| Transport error, 5xx, undecodable or incomplete success, timeout, Ctrl-C after dispatch | reconciliation | 0 or 10 |

`set_rereg_bids` has no idempotency key, so the CLI never resends. After an
ambiguous dispatch it reads `domain/get_user_rereg_bids` (bounded to 20 s,
even after Ctrl-C): an own bid at or above the price is reported as committed
with `reconciled: true`. Otherwise it reads the balance and returns
`outcome_unknown` (exit 10) with `journalId` and `evidence`
(`bidLookup` = `not_found`, `found_lower`, or `failed`, `ownBidKopecks`,
`balanceBeforeKopecks`, `balanceAfterKopecks`, `balanceChanged`). Run
`regru domain rereg bids` before any retry; a resend of an equal bid would be
refused by REG.API with `YOUR_SAME_OR_MORE_BID_FOUND`. The CLI never polls on
a timer.

The bid journal lives in `<UserConfigDir>/regru/rereg-journal/` (one
append-only JSON-lines file per account profile ID, mode 0600). It stores the
journal ID, ASCII name, amount, state (`intent`, `confirmed`, `rejected`,
`outcome_unknown`), Moscow day, timestamp, and bill ID only. Intent,
confirmed, and unknown bids count toward the daily cap; rejected ones do not.
An unreadable journal blocks bidding instead of being rewritten.

## Account profile settings

```toml
[accounts.personal.domain]
contact_profile_type = "RU.PP"     # GTLD, EU, TJ, RU.PP, RU.ORG, NEWTCI.ORG, NEWTCI.PP
contact_profile_name = "Owner"     # saved contact profile name from the REG.RU web cabinet
max_price = "500"                  # first-year cap, RUB
max_renewal = "1500"               # renewal cap, RUB
registrar_check = false            # true only for partner accounts where domain/check works
rereg_max_bid = "3000"             # backorder per-bid cap, RUB
rereg_max_daily = "10000"          # backorder spending cap per Moscow day, RUB
```

All fields are non-secret routing metadata, user-profile only, and never
rendered by `account show` or `capability list`. Type and name are set
together.

## Capabilities

`capability list` reports `domain.search` as always configured,
`domain.quote`, `domain.list`, and `domain.ns` when REG.API credentials are
routed, `domain.buy` when REG.API credentials and a contact profile are both
configured, and `domain.registrar_check` when the profile also sets
`registrar_check = true`. `domain.rereg.catalog` and `domain.rereg.quote` are
always configured (public data); `domain.rereg.bids` and `domain.rereg.bid`
need REG.API credentials.

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
regru domain rereg catalog fetch
regru domain rereg catalog search --date tomorrow --with-bids --plain
regru --account personal --no-input --json domain rereg quote moonbot.ru
regru --account personal --no-input --json domain rereg bids
regru --account personal --dry-run domain rereg bid moonbot.ru --price 1950 --max-price 2000 --contact profile --ns nic
regru --account personal --force --no-input --json domain rereg bid moonbot.ru --price 1950 --max-price 2000 --contact credential --ns nic
```
