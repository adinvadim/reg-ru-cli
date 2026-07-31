# PROTOTYPE — support mutation outcomes

This throwaway prototype asks whether the support adapter can truthfully model
one mutation attempt once request dispatch may have started. In particular, it
tests whether `not-sent`, `committed`, `rejected`, and `outcome-unknown` leave
only safe follow-up actions. An identical intent must remain blocked after an
unknown outcome: acknowledging duplicate risk is not evidence that the first
attempt did or did not commit.

Run it from the repository root:

```sh
go run ./internal/provider/support/prototype/cmd
```

Drive at least these paths:

1. pre-dispatch failure, then retry the same intent;
2. dispatch, then recognized success;
3. dispatch, then ambiguous failure; confirm that retrying the same intent stays
   blocked while a genuinely distinct intent can still be prepared.

The terminal shell and this directory are throwaway. The state machine is kept
separate from terminal I/O so a validated decision can later be implemented in
the production adapter without carrying the prototype shell into `main`.

## Validated decision

The owner accepted the strict model on 2026-08-01. Once dispatch may have
started, an ambiguous response is `outcome-unknown`. The same normalized intent
stays blocked unless an independent read can prove a safe postcondition; a
confirmation flag or acknowledgement of duplicate risk is not evidence and
must not unlock it. A genuinely different intent remains a separate operation.
