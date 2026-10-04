# Failed-attempt completion boundary

The journal records failed consumed attempts with `Complete`. This is a trusted
backend API, not an ingress request. The backend must close its session and join
owned work before calling it. Its stable failure classification states whether
this send may have posted, whether retry is useful, an optional upstream numeric
code, and any validated server retry delay. Lookup attempts cannot post.

The update fences the store-owned grant, consumed state, attempt ID and kind.
Failure to commit retains the active marker; another reservation is denied.
A receipt privately retained by `RecordAcceptance` cannot be replaced by failure
completion: save it through `RetryAcceptance`. Already saved acceptance remains
authoritative. The future backend must have one event owner that records observed
acceptance before considering failure completion; these primitives cannot prove
what an independently supplied backend actually observed.

Schema version 3 adds explicit possible prior acceptance, stable failure details,
a durable stop on further sends after permanent rejection, and the send counter
for which a lookup was reserved. Versions 1 and 2 migrate transactionally. Old
unfinished sends are conservatively uncertain; old accepted receipts remain
accepted. An in-progress send reports null acceptance until its result is known.
A known pre-write failure reports `accepted=false`; a possible write reports
null and `acceptance_uncertain=true`. A later rejection or unknown retained
receipt cannot erase an earlier possible acceptance. Duplicate risk is set when
reposting such an uncertain job, rather than after a known pre-write failure.

Send and lookup backoff use their separate stored counters and exponential caps,
with jitter between half the cap (rounded up) and the full cap. A server delay is
a lower bound, including on a lookup that precedes a resend. Scheduling is capped
at stored expiry without overflow or extending lifetime. Restart and changed
configuration defaults preserve the admitted policy, counters and schedules.

Under `retry`, reserve at most one lookup before each possible scheduled resend.
The lookup reservation itself records that decision, so a crash consumes the
probe. Unknown or transient lookup outcomes permit the due stable-ID resend.
When no resend is allowed (permanent rejection, exhausted send budget or
`receipt_only`), remaining lookup budget can still recover an uncertain receipt
with its own delay before expiry. Authentication or permission failures block
further automatic actions. Permanent rejection remains sticky across unknown
lookups and restart. Receipt-only never grants a second send.

The state and nullable receipt facts remain separate. Expired possible writes
stay unknown. Unresolved jobs keep `terminal_at` null, including rejected or
expired jobs with possible prior acceptance; future pruning must not erase them.
Known failures and saved acceptance have a terminal retention start. Expiry of
idle queued jobs, operator resolution, pruning and backup remain pending.

`next_action` and `next_attempt_at` are scheduling hints, not grants. Active
attempts report `await_receipt` or `await_lookup`; available actions report
`send` or `lookup`, and exhausted/terminal actions report `none`. `Reserve` still
rechecks current authorization, expiry, due time, budgets and ownership.

## Evidence and limitations

Real SQLite tests cover no-write and uncertain failures, independent lookup and
send counters, retained acceptance after a later rejection, sticky rejection,
crashed lookup recovery, receipt-only, budget exhaustion, bounded jitter, server
delay/overflow, expiry, SQL save failure, correlation fencing and acceptance
precedence. An abrupt child process exits after committing completion; reopening
preserves its uncertainty, error and schedule. Version 1/2 fixtures migrate while
preserving identity, request content, expiry and old attempt uncertainty.

The immutable daemon test drives the public SDK and these primitives explicitly:
a canceled dial returns no lease, a real `RETRY` returns numeric `488`, and a later
send uses the same stable ID. An independent observer verifies the actual author,
target, body and message ID before acceptance is saved. This is not a running
backend, scheduler or automatic outage-recovery claim.

```sh
go test -race -count=1 ./internal/gateway/store
go test -race -count=1 ./integration/gateway -run 'TestWire.*'
```

Backend phase timeouts, partial writes, receipt withholding, live worker cleanup,
automatic fair scheduling and the complete adapter catalogue remain pending.
