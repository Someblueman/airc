# Idle expiry and retention maintenance

`Store.Maintain` atomically expires inactive work and reclaims resolved results
and expired replay markers. Delivery workers invoke it at startup and once per
wall-clock second, with a five-second transaction context. A failure stops
new dispatch and cancels/joins the existing workers, leaving durable state
recoverable. The journal's owner gate and checkpoint capacity checks also apply.

Expiry never clears a reserved attempt or its private observed acceptance.
An already observed valid receipt can therefore still save after expiry. Idle
jobs with no possible prior acceptance become `expired`, `accepted=false`, with
a terminal retention start at maintenance completion. They release pending
quota. Jobs with possible acceptance remain `accepted=null`, visibly uncertain
and unresolved; they continue consuming unresolved quota and are never pruned.
Expiry does not replenish counters, extend lifetime or grant a network attempt.

Resolved results are deleted only at or after their stored terminal start plus
stored retention duration. Config changes do not alter that duration. Accepted
results require their saved receipt, including accepted-but-not-persisted results;
known no-write failures/expiry can also be reclaimed. No active grant is deleted.
Pruning does not promise a particular physical database-file shrink: SQLite
can reuse freed pages, and checkpointing manages the WAL.

Replay markers have an independent lifetime. Their inclusive validity-plus-skew
boundary must pass before deletion. A result can be pruned while its replay
marker remains; admission then rejects recreation rather than creating a new
job. After both windows end, a request past its `expires_at` still fails
validity checks. Session callers must not reuse request IDs after the published retention
window; an unknown old ID does not establish that no earlier post occurred.

Kafka ingestion has been removed. Historical positions, intents, outcomes and
publication records remain untouched by maintenance. Jobs awaiting a historical
Kafka publication are also retained; no consumer advances or publishes them.

Expiry, result deletion and replay deletion share one transaction. Counts are
returned only after commit. A deferred foreign-key commit-failure test confirms
that both expiry and result deletion roll back, with zero claimed changes.

## Evidence

Journal tests cover exact expiry and retention/replay boundaries, changed config
on reopening, quota release, unresolved preservation, active late acceptance,
accepted receipt pruning and failed-commit rollback. Against the pinned daemon,
a real post with a withheld receipt occupies its author's worker while a
second idle job expires without any send attempt; quota becomes available again
and cancellation joins the worker and relay.

```sh
go test -race -count=1 ./internal/gateway/store -run TestMaintenance
go test -race -count=1 ./integration/gateway -run TestDelivery
```

Operator resolution, separate operator receipt probes, backup, clock-change
handling, full storage/load acceptance, runtime ingress and the complete transport
catalogue remain pending. This maintenance primitive does not complete the gateway.
