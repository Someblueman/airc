# Attempt grants

The store hands out attempt grants: the only way a delivery worker or the
backend may dial AIRC for a job. Acceptance persistence is in
[acceptance.md](acceptance.md), error completion in
[completion.md](completion.md), workers in [delivery.md](delivery.md) and
pruning in [maintenance.md](maintenance.md).

The store's serialized gate orders intake, lookups and `Reserve` commits; no
network I/O runs under it. `Reserve` checks the stored job, the absence of
another active attempt, expiry, schedule and the relevant send or lookup
budget. It commits a random attempt ID, kind and deadline and increments the
selected counter before returning a `Grant`. Failed transactions return no
grant. Sends enter `dispatching`; lookups do not pretend another send
occurred. Receipt-only permits its initial send, then only bounded recovery.
Deadlines are at most 30 seconds from reservation and no later than stored
expiry.

`Grant.Consume` is the backend's pre-dial boundary. Nil, expired,
closed-store and already consumed handles fail; value copies share
consumption state. A grant cannot authorize a reconnect or another retry.

Reopening clears old active handles and recovers unfinished sends to
`retry_wait` or `uncertain` according to their stored policy, with duplicate
risk. Counters and schedules stay persisted, and recovery needs a new
reservation. A registered author's job waits in `credential_wait` until its
token returns (see [authentication.md](authentication.md#tokens-and-the-journal)).

The journal's historical migrations (including the revocation and
authorization-generation columns of schema 2) are kept unchanged; nothing
writes them any more. See [removed-identity.md](removed-identity.md).
