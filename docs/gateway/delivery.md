# Durable delivery workers

`delivery.Runner` now connects the journal's due work to the bounded SDK backend.
It runs on a service context independently of an admitted caller's lifetime.
There is no ingress listener or CLI service lifecycle yet.

The journal returns at most one due request per principal, rotates after the
last selected principal, excludes active/revoked/expired jobs, and respects
persisted send/probe schedules. An uncertain resend first selects its independently
budgeted receipt probe. Selection is only a hint: `Reserve` rechecks authorization,
expiry, budget, timing and exclusive ownership before committing the grant.

The runner has a bounded number of goroutines and no additional queued jobs.
Each principal occupies at most one worker. This intentionally favors fairness
and a simple session owner over maximum throughput. Each completed attempt saves
its correlated receipt or failed-attempt evidence before further work is selected.
A persistence failure stops the runner and closes other sessions; it does not
release that failed durable marker for another send. Process reopening performs
the existing uncertain-marker recovery without resetting budgets.

Cancellation stops new selection, cancels/joins network attempts and allows each
worker up to five seconds to save its observed outcome with an independent
context. The journal must remain open until `Run` returns. A runner has one
lifetime and cannot be started twice. Cancellation of a caller after admission
has no effect on this service context.

## Verified evidence

The isolated pinned daemon and independent observer verify:

- A durable job admitted during a socket-path outage retries automatically after
  restoration even when its caller has cancelled. No-write dial failure produces
  no duplicate-risk claim; later acceptance has the correct author/target/body.
- A real accepted result lost before journal saving is recovered after reopening
  by one retained-receipt probe, retaining the original message ID and one send.
- Cancellation while a real accepted post's receipt is withheld and unrelated
  traffic continues closes the lease, joins the worker and persists uncertainty.

Journal tests verify principal rotation against a queued flood, active-marker
exclusion, persisted backoff, lookup precedence and durable revocation exclusion.

```sh
go test -race ./internal/gateway/store -run TestReady
go test -race -count=1 ./integration/gateway -run TestDelivery
```

Service/ingress wiring, idle expiry, operator resolution, pruning/backup, metrics,
clock-change handling and the remaining complete Step 6 acceptance cases are
pending. No transport adapter is claimed. The complete catalogue remains required.
