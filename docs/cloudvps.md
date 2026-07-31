# CloudVPS commands

`regru vps` manages the documented REG.RU CloudVPS v1 resource surface and the
v2 plan/image catalog. It resolves `cloudvps.token` lazily from the selected
account's `credential_process`; the token is never accepted as a flag,
environment value, URL, or normal output.

## Command surface

```text
regru vps list
regru vps get <server-id>                         # alias: show
regru vps ips <server-id>
regru vps create --size S --image I [flags]       # alias: deploy
regru vps rename <server-id> --name NAME
regru vps start|stop|reboot|password-reset <server-id>
regru vps rebuild <server-id> --image IMAGE
regru vps resize <server-id> --size PLAN
regru vps clone <server-id> [--name NAME] [--offline]
regru vps delete <server-id>                      # alias: destroy

regru vps action show|wait <action-id>
regru vps ip list|show|add|ptr|remove
regru vps ssh-key list|add|rename|remove
regru vps snapshot list|create|remove
regru vps backup enable|disable|restore
regru vps plan list|show
regru vps image list|show
```

Use command help for the operation-specific flags. `ip show` deliberately
lists documented addresses and matches the requested address locally because
the current OpenAPI does not publish a single-address endpoint.

## Waiting and mutation safety

Every mutation still follows the global dry-run and confirmation contract.
`--dry-run` performs local validation but does not resolve the credential or
send a request. A real mutation requires terminal confirmation or `--force`.

CloudVPS actions wait by default. `--timeout` bounds each credential or HTTP
request; `--wait-timeout` bounds the complete action sequence and defaults to
10 minutes. `--no-wait` returns the accepted provider action ID without
polling. A timed-out wait does not cancel or mark the provider action failed;
resume it with `vps action wait`.

Reads may retry transient transport, throttling, and server failures with
bounded jitter. Mutations are sent exactly once. A transport failure after a
mutation is handed to the HTTP client returns `outcome_unknown`; reconcile the
resource or action before retrying.

## Disposable verification flow

The live-verification ticket owns real provider execution and spending. Its
low-cost disposable flow is:

1. Select an isolated verification profile and list plans and images for the
   intended region in `--json` mode.
2. Have a human approve the currently returned hourly price and the unique
   disposable server name.
3. Run the create command first with `--dry-run`, then repeat it with `--force`
   and the approved smallest suitable plan.
4. Record the returned server and action IDs; wait for the action explicitly if
   creation used `--no-wait`.
5. Exercise only the read/lifecycle checks authorized by the verification
   ticket.
6. Delete the exact disposable server with `--dry-run` first, then `--force`;
   verify it no longer appears in `vps list`.

Example shape (placeholders are intentional):

```sh
regru --account verify --json vps plan list --region REGION
regru --account verify --json vps image list --region REGION --type distribution
regru --account verify --dry-run vps create --name UNIQUE_NAME --size PLAN --image IMAGE --region REGION
regru --account verify --force --json vps create --name UNIQUE_NAME --size PLAN --image IMAGE --region REGION
regru --account verify --dry-run vps delete SERVER_ID
regru --account verify --force --json vps delete SERVER_ID
```
