# Durable revocation and attempt grants

The store supplies durable local revocation and attempt reservations. These are
primitives for the future dispatcher/backend, not a running delivery system.
Acceptance persistence is in [acceptance.md](acceptance.md). Error completion,
scheduling, pruning, backup and the full
catalogue remain required. No transport or network write is enabled here.

Schema version 2 adds principal/binding revocation and authorization generations,
plus attempt kind. Version 1 databases migrate in a transaction without changing
stored job identity, content, defaults or budgets. New databases use the same
migration. Unsupported newer schemas still fail.

The store's serialized gate orders intake, lookup authorization, local `Revoke`
and `Reserve` commits. This is broader than a per-principal gate because one
SQLite connection already serializes these transactions. No network I/O runs
under it. `Revoke` is a trusted local-operator API, never an untrusted body or
adapter operation. Revocation takes effect at commit and increments generation
once. New intake, status reads and reservations then fail authorization.
Stored results stay intact, and an already granted attempt is not retroactively
cancelled. Reopening cannot undo local revocation even when the old configuration
still says `revoked: false`. Configured startup revocation also persists.
Explicit unrevocation is not implemented.

`Reserve` checks the stored job, current exact operation/target permission,
durable generation, absence of another active attempt, expiry, schedule and the
relevant send/lookup budget. It commits a random attempt ID, kind, deadline and
generation and increments the selected counter before returning a `Grant`.
Failed transactions return no grant. Sends enter `dispatching`; lookup markers
do not pretend another send occurred. Receipt-only permits its initial send,
then only bounded recovery. Deadlines are at most 30 seconds from reservation
and no later than stored expiry.

`Grant.Consume` is the backend's required pre-dial boundary. Nil/zero, expired,
closed-store and already consumed handles fail; value copies share consumption
state. A grant committed before revocation may be consumed once within its
original deadline; its generation records that exception. It cannot authorize
reconnect or another retry. The future backend must enforce the deadline during
dial/write/read, close its client and stop owned work before releasing its slot.
No network timing evidence is claimed yet.

Reopening clears old active handles and recovers unfinished sends to `retry_wait`
or `uncertain` according to their stored policy, with duplicate risk. Counters
and schedules stay persisted. Old handles belong to the closed store and cannot
authorize work after reopening. Recovery needs a new reservation; exhaustion and
revocation remain effective. Completion and the full scheduler remain pending.

Actual SQLite tests cover ordering before/after/concurrent with revocation,
copied-handle single use, deadlines, exclusive attempts, marker/revocation
failures, independent budgets, receipt-only recovery and exhaustion across
reopen. Migration preserves a version 1 queued job. Subprocesses exit without
cleanup after committing a grant or revocation; reopening preserves budgets,
uncertainty and denial. Existing intake/failure/full/ownership tests still pass.

These tests prove the journal gate and handle contract. Revocation during real
network writes, acceptance after revocation, stale completion fencing, and actual
transport no-post cases require the backend and dispatcher acceptance tests.
