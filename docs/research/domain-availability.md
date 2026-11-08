# Domain availability and candidate generation

Research date: 2026-11-08

## Question and evidence boundary

This note answers one question for the planned `regru domain search` and
`regru domain check` commands: how the CLI establishes that a name is free
quickly and correctly, and how it generates candidate names from a few words.

Evidence comes from four places:

- the production availability rules in `~/sandbox/ru-com-domains`
  (`docs/research/availability.md`,
  `packages/server/src/acquisition/registry.ts`,
  `packages/domain/src/registration.ts`,
  `packages/server/src/domains/identity.ts`);
- first-party registry documents: the TCI WHOIS terms for .RU/.РФ
  ([whois_ru_rf.pdf][tci-whois]), the Verisign RDAP service and the IANA RDAP
  bootstrap file ([dns.json][iana-bootstrap]);
- the REG.API 2 reference for `domain/check` ([api2doc][regapi-check]);
- eight live read-only lookups on 2026-11-08 06:28–06:30 UTC (three TCI WHOIS,
  two Verisign RDAP, one IANA bootstrap fetch, plus three empty `nc` attempts
  that produced no output because BSD `nc` closed the socket on stdin EOF;
  use a real client, not `nc`, for manual checks) and local IDNA computations.

No registration, order, reservation, authenticated REG.API or Firecrawl call
was made.

## Decision

**Registry first, registrar second.** The authoritative registry answers
"is there a record for this name" in about 0.5 s with no account. A registry
miss makes a name a *candidate-free*; it becomes *free* only when the
registrar confirms it can register the name right now. Any registry record,
whatever its status, means *not free*. Every failure is *unknown*, never free.

The registrar step is optional because REG.API `domain/check` is published as
a **partner-only** function. The CLI should capability-gate it the same way the
billing contract gates `bill/get_for_period`, and report registry-only results
honestly as `likely-free` with the evidence, not as `free`.

## What ru-com-domains already proves

`ru-com-domains` hit two real false positives where a registrar search said
"available" while the registry still held the name in `pending delete`
(`fullstacktraining.com` on 2026-10-11, `1860-pd.com` on 2026-10-14). The
user's purchase of the first one failed with `This domain is already
registered.` Its fixed rules, which this CLI should copy:

| Rule | ru-com-domains behaviour |
| --- | --- |
| Positive registrar quote | Must be confirmed by an authoritative registry lookup before it counts. |
| .com taken | Verisign RDAP `GET https://rdap.verisign.com/com/v1/domain/<ascii>` returns a `domain` object whose `ldhName` matches. Taken **regardless of status**: expiry, `redemption period` and `pending delete` do not release the name. |
| .com miss | Only HTTP 404 **with** `Content-Type: application/rdap+json` from that fixed endpoint. It corroborates absence; it never establishes availability alone. |
| .ru/.рф taken | `whois.tcinet.ru:43`, ASCII (ACE) query, a `domain:` line matching the queried name and `state:` containing `REGISTERED`. A mismatched `domain:` is an error. |
| .ru/.рф miss | Both `% TCI Whois Service.` header and the exact line `No entries found for the selected source(s).` must be present. |
| Everything else | Empty, interrupted, throttled, oversized, mismatched or unknown responses fail the check. HTTP non-2xx/non-404 is a provider failure that honours `Retry-After`. |
| Transport | 12 s deadline per TCP/HTTP request, 128 KB response cap, RDAP `redirect: 'error'`. |
| Freshness | A "free" verdict is usable for 10 minutes (`registrationAvailable`, `< 600_000` ms). A pending-deletion verdict is usable for 24 hours (`freshPendingDeletion`). |
| Pending deletion | `pendingDelete`/`pending delete` counts as pending deletion only if no restorable status is present: `redemptionPeriod`, `redemption period`, `pendingRestore`, `pending restore`. Pending deletion is still *not registrable*. |
| Name validation | `domainToASCII(lowercase)` then a strict regex for second-level `.ru`, `.com`, `xn--p1ai`, plus a Unicode round-trip (`toASCII(toUnicode(x)) == x`). URLs, whitespace, `/@:#?%\` and subdomains are rejected. |
| Persisted evidence | Source URL, `checkedAt`, record state and statuses. No contact data and no raw WHOIS text. |

## Registry facts verified today

### TCI WHOIS for .RU/.РФ (and .SU)

The TCI terms ([whois_ru_rf.pdf][tci-whois], v1.0, 2022-09-12) state:

- interfaces: `https://tcinet.ru/whois` and `whois.tcinet.ru:43`, protocol
  RFC 3912 (one query per TCP connection, `name\r\n`);
- **limit: 120 requests per minute per source IP**; exceeding it blocks the IP
  automatically, and access returns automatically **after one hour**.
  Systematic violation can make the block permanent. "Mass sampling" is
  forbidden by the terms;
- `state:` values: `REGISTERED`, `DELEGATED`, `NOT DELEGATED`,
  `pendingDelete` ("registration and preferential renewal period expired,
  awaiting automatic deletion"), `VERIFIED`, `UNVERIFIED`;
- `free-date:` is the planned deletion date after the preferential renewal
  period. Deletion does not run on weekends, holidays, or the first working day
  after them; such names drop on the second working day;
- names on the registry stop-list are reported (in the web UI) as
  `blocking: Domain can not be registered`. The port-43 rendering of that case
  is not documented.

Live port-43 output, 2026-11-08 (contact lines omitted):

```text
% TCI Whois Service. Terms of use:
% https://tcinet.ru/documents/whois_ru_rf.pdf (in Russian)
% https://tcinet.ru/documents/whois_su.pdf (in Russian)

domain:        DOMKRAY.RU
state:         REGISTERED, DELEGATED, VERIFIED
registrar:     REGRU-RU
created:       2026-11-07T18:36:52Z
paid-till:     2027-11-07T18:36:52Z
free-date:     2027-12-08
source:        TCI

Last updated on 2026-11-08T06:28:01Z
```

```text
% TCI Whois Service. Terms of use:
...
No entries found for the selected source(s).

Last updated on 2026-11-08T06:28:01Z
```

The second block was returned for both `qzxv-nonsense-8k3j2p7w.ru` and
`xn--80ahqcjlq.xn--p1ai` (домкрай.рф, which is therefore currently unregistered
in the registry). Each answer took about 0.5 s. Observations: the `domain:`
value is upper-case, so compare case-insensitively after ACE normalisation;
the `Last updated on` timestamp trailed wall clock by about one minute, so the
port-43 data is near-real-time, not a daily dump; the header also references
`whois_su.pdf`, i.e. the same server answers .SU.

.ru/.рф have **no RDAP** entry in the IANA bootstrap; WHOIS is the only
public registry channel.

### Verisign RDAP for .com/.net

Live responses, 2026-11-08:

| Query | HTTP | Content-Type | Time | Body |
| --- | --- | --- | --- | --- |
| `google.com` | 200 | `application/rdap+json` | 0.85 s | `objectClassName: domain`, `ldhName: GOOGLE.COM`, `status: [...]` |
| `qzxv-nonsense-8k3j2p7w.com` | 404 | `application/rdap+json` | 0.74 s | **empty** |

The 404 body is empty, so the miss signal is status plus content type only; do
not require an RDAP error object. RDAP status strings are space-separated
lower-case (`client transfer prohibited`, `pending delete`,
`redemption period`); EPP camelCase forms also appear in other sources, so
match both. Verisign publishes no numeric rate limit that we found; handle
429 and `Retry-After`.

### IANA RDAP bootstrap for other TLDs

`https://data.iana.org/rdap/dns.json` (publication `2026-10-28T22:00:04Z`)
maps `com` and `net` to `https://rdap.verisign.com/{com,net}/v1/`, `org` to
PIR, and `online`/`store`/`tech` to Radix. `ru`, `su`, `xn--p1ai` and `io` have
no entry. For any other zone the CLI may resolve the base URL from this file
(cache it for a day), but must apply the same rule: match on a returned
`domain` object, accept a miss only as 404 with the RDAP content type, and
report `unknown` when the zone has no RDAP service. Do not fall back to
scraping arbitrary WHOIS servers in the first version.

### REG.API 2 `domain/check`

The [REG.API 2 reference][regapi-check] lists `domain/check` as available to
**partners** (Партнёры). It takes `domains: [{dname}]` in one call,
`premium_as_taken` and `currency`. Per name, `result: "Available"` means
registrable; otherwise an `error_code` such as `DOMAIN_ALREADY_EXISTS`,
`INVALID_DOMAIN_NAME_PUNYCODE`, `TLD_DISABLED` or `DOMAIN_BAD_NAME`. Premium
names carry `is_premium`, `price`, `renew_price`. The API limit is **1200
requests per hour per client and per IP**, enforced together, with
`IP_EXCEEDED_ALLOWED_CONNECTION_RATE` / `ACCOUNT_EXCEEDED_ALLOWED_CONNECTION_RATE`.
REG.RU itself recommends calling the API for orders and changes, not for
information lookups.

## IDN handling in Go

`go.mod` already lists `golang.org/x/net v0.62.0 // indirect`; importing
`golang.org/x/net/idna` only promotes it to a direct dependency. No new module
is needed.

Yesterday's mistake: домкрай.рф was hand-written as `xn--80ajmfjh7a`. Computed
values agree on the correct form:

| Tool | Result for `домкрай.рф` |
| --- | --- |
| `python3 -c "print('домкрай.рф'.encode('idna'))"` | `xn--80ahqcjlq.xn--p1ai` |
| Node `new URL('http://домкрай.рф').hostname` | `xn--80ahqcjlq.xn--p1ai` |
| Go `idna.Registration.ToASCII` / `idna.Lookup.ToASCII` | `xn--80ahqcjlq.xn--p1ai` |

The wrong label is valid Punycode: `idna.Registration.ToUnicode("xn--80ajmfjh7a")`
returns `ейамткн` without error. No validator would have caught it. Only
showing the Unicode form back to the user exposes the error.

Profile behaviour observed with `x/net/idna` (Go 1.26):

| Input | `Registration.ToASCII` | `Lookup.ToASCII` |
| --- | --- | --- |
| `Домкрай.РФ` | error: disallowed rune U+0414 | `xn--80ahqcjlq.xn--p1ai` |
| `дом-край.рф` | `xn----8sbkwdknt.xn--p1ai` | same |
| `ab--c.ru`, `-bad.ru` | error: invalid label | error |
| `дом_край.рф` | error: disallowed rune U+005F | error |
| `faß.de` | `xn--fa-hia.de` (IDNA2008) | same; Python's IDNA2003 codec would give `fass.de` |

`Registration` is strict and applies no mapping, so upper-case input fails.
`Lookup` applies the UTS #46 mapping (case folding, NFC) and is lenient. The
CLI should therefore:

1. Reject whitespace, `/`, `@`, `:`, `?`, `#`, `%`, `\` and anything beyond one
   label plus a supported zone (no URLs, no subdomains).
2. Map with `idna.Lookup.ToUnicode` (folding case and normalising), then
   convert the mapped Unicode with `idna.Registration.ToASCII`, which enforces
   hyphen, BiDi and allowed-rune rules. Verified: `Домкрай.РФ` →
   `домкрай.рф` → `xn--80ahqcjlq.xn--p1ai`.
3. Require `Registration.ToASCII(Registration.ToUnicode(ascii)) == ascii`.
4. Apply zone script rules: `.ru`, `.com`, `.su` labels are ASCII LDH only;
   `.рф` labels are Cyrillic letters, digits and hyphens. Latin in an `.рф`
   label is rejected locally instead of being sent to the registry.
5. Always print both forms (`домкрай.рф (xn--80ahqcjlq.xn--p1ai)`). When the
   user types an `xn--` label, echo the decoded Unicode so a hand-made
   Punycode mistake is visible before anyone registers the wrong name.
6. Send only the ACE form to WHOIS, RDAP and REG.API. Use Go `x/net/idna` as
   the single implementation; do not mix in Python's IDNA2003 codec.

## Recommended availability algorithm

For each normalised ACE name:

1. **Validate locally** (above). Invalid input is a usage error, not a lookup.
2. **Registry lookup**, routed by zone:
   - `.ru`, `.xn--p1ai`, `.su`: TCI WHOIS over a fresh TCP connection, 5 s
     deadline (observed 0.5 s; ru-com-domains uses 12 s for a long-running
     service; an interactive CLI can be tighter and report `unknown` on
     timeout), 128 KB cap.
   - `.com`, `.net`: fixed Verisign RDAP URL, `Accept: application/rdap+json`,
     no redirects, 5 s deadline, 128 KB cap.
   - other zones: IANA bootstrap RDAP if present, otherwise `unknown`
     ("no registry source for this zone").
3. **Classify** the registry answer:
   - matching record with `pendingDelete`/`pending delete` and no
     restorable status → `pending-delete` (not registrable; carry
     `free-date` for .ru/.рф);
   - any other matching record, including `redemption period` →
     `taken`;
   - the exact documented miss signal → `likely-free`;
   - anything else → `unknown` with a reason (`timeout`, `rate-limited`,
     `mismatch`, `unrecognised-response`).
4. **Registrar confirmation** only for `likely-free` names, only when the
   profile has a verified REG.API `domain/check` capability: one batched call
   for all `likely-free` names. `Available` → `free` (with `is_premium` and
   price when present; pass `premium_as_taken=0` and show premium explicitly).
   `DOMAIN_ALREADY_EXISTS` → `taken` (the registrar wins on a conflict with a
   registry miss, matching the ru-com-domains direction of caution). Other error
   codes → `unavailable` with the code. Capability absent → leave
   `likely-free` and state why.
5. **Emit evidence** per name: verdict, source (`whois.tcinet.ru:43`,
   `rdap.verisign.com`, `REG.API domain/check`), `checkedAt`, registry state
   and statuses, `free-date`/`paid-till` when present. Never print contact
   fields or the raw WHOIS body by default.
6. **Freshness**: a `free`/`likely-free` verdict is valid for 10 minutes. Any
   future `domain register` path must recheck rather than trust an older
   answer. Pending-deletion evidence may be cached for 24 hours.

Exit status: 0 when every requested name has a definite verdict, a distinct
non-zero code when any name is `unknown`, so an agent never mistakes a partial
answer for a complete one.

### Parallelism and budgets

- Dispatch zones concurrently; TCI and Verisign are independent.
- TCI: at most 2 concurrent connections and a process-wide spacing of
  ≥ 600 ms (about 100/min, under the documented 120/min/IP). Any refused
  connection, empty reply, or other non-matching text is treated as a
  possible block: stop all TCI work for this run and report `rate-limited`
  with a retry after one hour, the documented recovery time. Do not retry
  in a loop; a repeat offence can be permanent.
- Verisign RDAP: up to 4 concurrent requests; on 429 honour `Retry-After`
  and mark the remaining .com names `unknown`.
- REG.API: one batched `domain/check` per run, so the 1200/hour budget is
  never the constraint.
- `domain search` caps candidates (default 24, `--limit` up to about 60) so a
  single run stays well under one minute of TCI budget and finishes in a few
  seconds. With 2 workers × 0.5 s, 24 .ru/.рф names take about 6–8 s.
- Optional fast filter for large lists: a DNS NS query for the name at the
  TLD servers. An existing delegation proves `taken` with no WHOIS cost. No
  delegation proves nothing (`NOT DELEGATED` names exist), so the name still
  goes to the registry. Defer this until candidate lists outgrow the TCI
  budget.

### Zone table

| Zone | Authoritative source | Free signal (`likely-free`) | Taken signal | Evidence kept |
| --- | --- | --- | --- | --- |
| `.ru` | TCI WHOIS `whois.tcinet.ru:43`, ACE query | `% TCI Whois Service.` header **and** exact line `No entries found for the selected source(s).` | `domain:` equals query (case-insensitive) with any `state:`; `pendingDelete` gives `pending-delete` | `state`, `registrar`, `paid-till`, `free-date`, `Last updated on`, `checkedAt` |
| `.рф` (`xn--p1ai`) | Same TCI WHOIS, ACE query (`xn--…​.xn--p1ai`) | Same as `.ru` | Same as `.ru` | Same, plus the Unicode form |
| `.su` | Same TCI WHOIS (header cites `whois_su.pdf`) | Assumed same format; **not live-verified** | Assumed same | Same |
| `.com` | Verisign RDAP `https://rdap.verisign.com/com/v1/domain/<ace>` | HTTP 404 with `Content-Type: application/rdap+json` (body may be empty) | HTTP 200 RDAP `domain` object whose `ldhName` matches, any `status`; `pending delete` without `redemption period`/`pending restore` gives `pending-delete` | `status[]`, expiration/last-changed events, `checkedAt` |
| `.net` | Verisign RDAP `.../net/v1/domain/<ace>` | Same as `.com` | Same as `.com` | Same |
| other gTLDs | IANA bootstrap `dns.json` → registry RDAP base | 404 + RDAP content type | Matching `domain` object | Source base URL, `status[]`, `checkedAt` |
| any zone, confirmation | REG.API 2 `domain/check` (partner-only) | `result: "Available"` upgrades `likely-free` → `free` | `error_code: DOMAIN_ALREADY_EXISTS` | `is_premium`, `price`, `renew_price`, `currency` |

## Candidate generation for `domain search <words…>`

Keep it deterministic: the same input always produces the same ordered list,
with no network-dependent suggestion source.

**Input.** One or more words, Cyrillic or Latin. A word may carry explicit
alternatives with `|`, for example `kuban "kvartal|kvartaly"` or
`кубань "квартал|кварталы"`. Morphology (singular/plural, case endings) is
never guessed; the user or agent supplies the forms they want.

**Zones.** Default `--zones ru,рф,com`, in that order. `.su` and `.net` are
opt-in. `.рф` gets candidates only from Cyrillic input (the Cyrillic words
joined as typed); it never gets transliterated Latin.

**Transliteration of Cyrillic for `.ru`/`.com`.** Use one primary table and a
short list of well-known alternatives, applied per word, and cap the variants
at 4 per word, chosen in the order listed.

| Cyrillic | Primary | Alternatives |
| --- | --- | --- |
| кс | ks | x |
| х | h | kh |
| ж | zh | j |
| ц | ts | c |
| ч | ch | — |
| ш | sh | — |
| щ | sch | shch |
| й | y | i, j |
| ы | y | i |
| ю | yu | ju, u |
| я | ya | ja, a |
| е, ё | e | yo for ё |
| э | e | — |
| ь, ъ | (dropped) | — |

The primary column is the practical spelling used in Russian brand domains,
deliberately not GOST 7.79 or the ICAO passport table (ICAO gives `kh`, `i`,
`iu`, `ia`, which read badly in a domain); the alternatives cover the common short forms (`domkraj`, `kvartaly`
→ `kvartali`, `ks` → `x`). Enumerate alternatives one substitution at a time
from the primary spelling; do not take the Cartesian product.

**Joining.** For each combination of word variants, emit the concatenation
(`kubankvartal`) and, if there are two or more words, the hyphenated join
(`kuban-kvartal`). Skip anything that fails local validation (label over 63
ACE characters, leading/trailing hyphen, `--` in positions 3–4, too short).
Deduplicate on the ACE name.

**Ranking** (stable sort, earlier key wins):

1. Exact brand: the primary transliteration, concatenated, in the first zone.
2. Zone order as given by `--zones`.
3. No hyphen before hyphen.
4. Primary spelling before alternatives, fewer substitutions first.
5. Shorter ACE label (for `.рф`, the Unicode length).
6. Lexicographic ACE name as the final tie-breaker.

Truncate to `--limit` *after* ranking, then check. Report verdicts in rank
order, grouping `free`, then `likely-free`, then `pending-delete` (with
`free-date`), `taken`, and `unknown`, so an agent can take the first
`free` row. Example for `кубань "квартал|кварталы"` with default zones:
`kubankvartal.ru`, `kubankvartaly.ru`, `kuban-kvartal.ru`, …,
`кубанькварталы.рф`, …, `kubankvartal.com`, ….

## Unknowns

- **Partner gating of `domain/check`.** The reference marks it partner-only.
  Whether an ordinary client account receives `ACCESS_DENIED`, a different
  code, or works anyway is unverified; it needs one authenticated probe with
  the real profile, recorded as a capability state.
- **Stop-listed and reserved .ru/.рф names.** The web UI shows
  `blocking: Domain can not be registered`; whether port 43 says the same or
  just `No entries found` is unknown. This is exactly why a registry miss is
  only `likely-free`.
- **.su response format** was not live-checked.
- **Exact .ru `state:` line for a name in `pendingDelete`** (whether
  `REGISTERED` still appears alongside it) was not observed. The CLI should
  treat any matching `domain:` line as not free, rather than requiring
  `REGISTERED` as ru-com-domains does, so either form is safe.
- **Verisign RDAP rate limit** is unpublished; only 429 handling is designed.
- **TCI block detection.** How a blocked IP looks on port 43 (refused
  connection, silent close, text message) is undocumented; the design treats
  any unexpected reply as a possible block and stops.
- **Registry-to-registrar lag.** A name freed at TCI on its `free-date` is
  often caught by drop-catching registrars within seconds; `likely-free` for a
  freshly dropped name is weaker than for a never-registered one.
- **Minimum label length** for .ru/.рф (believed to be 2 characters) was not
  re-verified against the current registration rules.

[tci-whois]: https://tcinet.ru/documents/whois_ru_rf.pdf
[iana-bootstrap]: https://data.iana.org/rdap/dns.json
[regapi-check]: https://www.reg.ru/reseller/api2doc#domain_check
