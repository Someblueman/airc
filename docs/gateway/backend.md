# Bounded SDK backend: send, reply and retained lookup

`internal/gateway/backend` now performs one public `pkg/irc` send/reply or retained
receipt lookup from a consumed journal grant. It returns receipt or failure
evidence only after closing and joining its exclusive SDK session. Delivery
workers must save that evidence through `RecordAcceptance` or `Complete`; the
backend does not admit requests, maintain another retry journal or run a worker
scheduler. No runtime ingress operation is advertised yet.

Startup reads immutable owner-provisioned account/access credentials, explicit
network/address and TLS policy. Account creation and SDK reconnect are disabled.
The consumed attempt carries its store's binding references, never secret values.
The backend compares them to its immutable configuration and re-derives the
scoped stable ID under its realm before dialing. Another realm, account or
endpoint cannot silently consume a grant under a different binding. Credential
rotation takes effect with a new backend snapshot; it does not change stored
request identity. Custom CA files use native no-follow, owner and permission
checks and strict bounded PEM certificate decoding. System trust is used when no
custom roots are configured. TLS requires verified hostname and TLS 1.3.

A lease must negotiate ephemeral mode and exact `RECEIPTS=1`, `IDEMPOTENCY=1` and
`SAFE_RETRY=1`. Missing/disabled values are permanent incompatibility, not an
excuse to remove stable IDs. Each fresh session has one event-stream owner and
one submission or `RETRY`. Replies authorize the retained room or DM parent
before writing, and verify the receipt root against that read. See
[replies.md](replies.md) for the bounded parent contract and actual wire evidence.

The original grant deadline caps the whole operation. Dial/registration is at
most 10 seconds, writing at most 5 seconds and receipt wait at most 10 seconds,
each within the remaining overall deadline and worker context. Deadline callbacks
close the SDK client to interrupt I/O. Every callback is stopped or joined, and
`Close` joins the SDK reader before the session slot is released. Unrelated events
do not refresh the absolute receipt timer. A possible send write remains uncertain
after disconnect, timeout, unknown error or invalid correlated receipt. Accepted
receipts use the same complete correlation validator as journal persistence and
preserve local observation time for saving after expiry.

Known numeric failures retain their upstream code and stable classification;
`488` after `RETRY` is unknown, not permanent rejection. Slow-mode numeric `486`
uses the exact pinned-server retry-delay grammar; unfamiliar grammar preserves
uncertainty. Registration capacity failure is transient; credential and TLS
verification failures are permanent. The benign registration `422` no-MOTD notice
is ignored even when still queued in the SDK stream.

Session allocation is nonblocking and bounded globally and per principal. Lookup
leases leave one slot for sends whenever the applicable limit is larger than one;
a limit of one serializes both operations. Saturation returns transient capacity
evidence, without starting another goroutine or connection. Read/watch pools and
fair worker scheduling are still pending.

## Evidence and remaining work

The immutable daemon test drives the actual backend send, verifies the independent
observer's author, target, body and message ID, then reopens the journal without
saving and recovers the same receipt using a new backend lease and public `RETRY`.
The result saves acceptance with one send and one lookup. Reusing the grant or
posting after saved acceptance is rejected.

Owned socket proxies test stalled registration, disabled feature values, and
withheld receipts while continuously forwarding unrelated traffic. Each attempt
ends at its stored expiry and its peer observes connection cleanup before the
backend returns. A real saturated lease rejects a second attempt with capacity
backpressure; its deadline closes the peer and releases the slot for a later
attempt. The observer confirms a real post behind the withheld receipt,
and completion preserves that acceptance uncertainty. A TLS relay authenticates
an independent access credential and forwards SASL/chat to the fixture's Unix
listener: trusted TLS succeeds, wrong hostname and wrong access credential fail.
This verifies the backend TLS/access path through that relay; native daemon TLS
listener/access provisioning acceptance remains to be added separately.

```sh
go test -race -count=1 ./integration/gateway -run 'TestBackend'
go test -race ./internal/gateway/backend ./internal/gateway/auth ./internal/gateway/store
```

Blocked-write injection through the actual backend, separate phase-limit checks
without a shorter operation expiry, history,
watch, search/directory, automatic delivery workers, lifecycle wiring and all
catalogue adapters remain pending. No hardware/platform blocker is inferred from
these missing implementations. Nothing is installed, deployed or connected to
live AIRC.
