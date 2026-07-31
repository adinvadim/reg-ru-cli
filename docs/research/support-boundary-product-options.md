# Product options for the REG.RU support boundary

Research date: 2026-07-30

## Decision frame

This note supports
[“Grilling: choose the REG.RU support integration boundary”][boundary-ticket].
It compares the three named boundaries without selecting one for the owner.

The current product map aims for agent-safe support ticket creation, reading,
replies, attachments, and status operations. It also promises stable
machine-readable output, browser-mediated authentication, no raw session
secrets in flags or logs, confirmation for support mutations, and private
adapters that are isolated, version-probed, and fail closed on drift.
([Current product contract][product-map])

The evidence limits what any option can honestly promise:

- REG.RU documents a human support workflow in its website and account panel,
  while REG.API 2 publishes no support-ticket methods.
  ([REG.RU support workflow][regru-support],
  [REG.API 2 function inventory][regru-api])
- Public first-party frontend code reveals unversioned, browser-session-bound
  routes for ticket creation and temporary uploads, but no published
  authentication, compatibility, pagination, error, rate-limit, idempotency,
  or complete ticket-lifecycle contract. The observed identifiers cannot
  safely be treated as durable typed IDs.
  ([Local operation-contract research][operation-research])
- A lost response after a private mutation has an unknown outcome, and the
  current support flow exposes neither a client idempotency key nor a reliable
  independent read surface for reconciliation.
  ([Local resilience research][resilience-research])

Established providers reinforce the distinction between a web capability and
an automation contract. DigitalOcean documents ticket creation as a signed-in
support-portal flow. AWS separately documents a Support API and its AWS CLI
commands where programmatic case management is intended, while marking other
Support Center console operations as unavailable to the SDK and CLI.
([DigitalOcean support portal][digitalocean-support],
[AWS Support API][aws-support-api],
[AWS console-only support operations][aws-console-only])

## Options

| Boundary | User value | Expectation setting | Agent safety | Maintenance | Reversibility |
| --- | --- | --- | --- | --- | --- |
| **1. Authenticated browser handoff only** | Immediate, useful entry point: open the canonical support area in the dedicated authenticated browser profile; the human can use the full provider UI. No listing, scripting, or structured ticket output. | Name it as a website handoff, not `ticket create/list`. State that the CLI neither reads nor submits ticket data. This is materially different from claiming support automation. | Strong. REG.RU retains login, CSRF, CAPTCHA, validation, review, and submission UX. The CLI should open only the generic support area, never synthesize a ticket URL from an opaque token. | Low: browser/session launch, canonical URL, reauthentication, and actionable errors. No private schemas or redacted ticket fixtures. | High. The handoff remains useful if an authorized adapter is added later, and removing automation is unnecessary because none was promised. |
| **2. Block automation pending provider authorization** | Lowest immediate value: deterministic capability error plus documentation of the provider prerequisite. It is the clearest answer for scripts and agents, but contributes no support workflow beyond guidance. | Clearest boundary: support automation is unavailable until REG.RU publishes or explicitly authorizes a contract. If the error automatically opens a browser, this option has effectively moved toward option 1; keep that distinction deliberate. | Strongest. No support session, message, attachment, locator, or mutation passes through the CLI. | Lowest: capability gating, documentation, and periodic review of official REG.RU material. No private endpoint upkeep. | Highest. A later documented API can be introduced behind the existing capability boundary without preserving private behavior. The external dependency—provider authorization—can delay the feature indefinitely. |
| **3. Explicitly experimental private adapter** | Highest potential value: agent/script access to ticket lifecycle operations. Actual availability will be unpredictable because the provider has not promised the interface or semantics. | Must be opt-in, visibly experimental, off by default, and outside the ordinary stable-support promise. The current global promise of stable `--json`/`--plain` needs an explicit answer: either experimental commands are carved out, or their outward schema stays stable while capability availability may fail closed. | Weakest. Structural probes cannot prove authorization or detect every semantic change. Session secrets, opaque locators, attachments, sensitive ticket text, duplicate creates/replies, and ambiguous mutation outcomes all require additional controls. `--dry-run` and confirmation improve user intent but cannot provide provider-side idempotency or rollback. | Highest and ongoing: authenticated contract capture, redacted fixtures, drift probes, session/CSRF behavior, schema adapters, failure reconciliation, and recurring end-to-end verification. Mutations need special handling when delivery is ambiguous. | Mixed. A separate namespace/package and feature flag make code removable, but users may script it despite warnings. Withdrawal can break those scripts, and already-sent messages or attachments cannot be undone. |

## What “experimental” would have to mean

The third option is credible only if its boundary is product behavior rather
than a warning in documentation. Terraform's development overrides bypass
normal version/checksum selection, are explicitly temporary, are not intended
for general use, and may change incompatibly. Kubernetes alpha features are
disabled by default, may be buggy or removed without notice, and are
recommended only for short-lived testing. These are useful expectation-setting
precedents, not evidence that REG.RU authorizes private-route use.
([Terraform development overrides][terraform-overrides],
[Kubernetes alpha features][kubernetes-alpha])

For `regru`, the owner would therefore need to decide at least:

- whether the experiment is a separate command namespace or separately
  installable adapter, and what explicit opt-in enables it;
- whether only reads are initially allowed, or whether non-idempotent
  create/reply/upload/status mutations are accepted despite unknown outcomes;
- whether stable JSON applies to the normalized output even when the backend
  capability can disappear on drift;
- what event disables the adapter globally, and what user-visible recovery
  path follows;
- whether explicit written provider authorization is still a graduation
  criterion, or merely desirable.

## Decision consequences for the map

Options 1 and 2 both keep the private adapter out of the initial stable core,
but they make different product promises. Option 1 preserves support as a
human workflow owned by the CLI; option 2 treats support automation as an
unavailable provider capability. Option 3 retains the adapter ticket, but the
current prototype objective would need to be narrowed from a normal
agent-usable feature to the exact experimental scope and stability carve-outs
the owner accepts. ([Current support-adapter prototype][adapter-ticket])

The decisive owner question is not whether private calls can be observed—they
can. It is whether immediate scripted support value justifies shipping a
surface whose authorization, availability, mutation certainty, and upkeep are
not controlled by this project.

[boundary-ticket]: https://github.com/adinvadim/reg-ru-cli/issues/22
[product-map]: https://github.com/adinvadim/reg-ru-cli/issues/1
[adapter-ticket]: https://github.com/adinvadim/reg-ru-cli/issues/8
[regru-support]: https://help.reg.ru/support/lichnyy-kabinet/registratsiya-i-kontaktnyye-dannyye/kak-svyazatsya-so-sluzhboy-podderzhki
[regru-api]: https://www.reg.ru/reseller/api2doc#common_functions_list
[operation-research]: support-private-operation-contract.md
[resilience-research]: private-portal-resilience-contract.md
[digitalocean-support]: https://docs.digitalocean.com/support/create-support-ticket/
[aws-support-api]: https://docs.aws.amazon.com/awssupport/latest/user/about-support-api.html
[aws-console-only]: https://docs.aws.amazon.com/awssupport/latest/user/support-console-access-control.html
[terraform-overrides]: https://developer.hashicorp.com/terraform/cli/config/config-file#development-overrides-for-provider-developers
[kubernetes-alpha]: https://kubernetes.io/docs/reference/command-line-tools-reference/feature-gates/#feature-stages
