# Durable acceptance receipt boundary

The store now validates and saves a complete public SDK `SendReceiptEvent` before
returning a durable accepted result. This is an acceptance primitive and an
isolated wire-to-journal test, not a running backend, worker or transport adapter.
Error completion, automatic recovery/scheduling, pruning, backup, parent/read
authorization and the complete catalogue remain unfinished.

`RecordAcceptance` requires a consumed grant belonging to this open store. The
receipt must carry accepted facts, a valid message ID, the stored stable upstream
request ID, pinned account nickname, normalized typed target and exact normalized
body. Plain sends cannot acquire reply/reaction linkage; replies must match the
stored parent and have a valid thread ID. This does not replace pre-send retained
parent authorization. The SDK event type and supported metadata are bounded;
ordinary send/reply receipts cannot include poll fields. The serialized receipt
ceiling is 64 KiB, allowing JSON escaping of a full 4096-byte body. This is not a
transport payload ceiling.

The local observation time must follow grant consumption and precede its original
deadline. Server message time is not used to prove timely local observation. A
receipt already observed in time may finish saving after operation expiry or
revocation. The future backend must still enforce its dial/write/read deadline;
this API cannot prove when an independently supplied receipt was actually seen.

In one transaction, the matching attempt becomes `accepted`, receipt facts and
the full parsed SDK event are saved, the active marker is cleared, and terminal
retention begins. The attempt ID and kind fence stale completions. Both send
receipts and retained lookup receipts resolve acceptance; a lookup requires a
prior send reservation. Identical save retries return the existing result; conflicting receipts cannot overwrite it. The public
receipt's message timestamp retains the SDK's RFC3339 nanosecond JSON encoding;
journal scheduling/retention timestamps remain integer Unix seconds.

`accepted`, `persisted` and `recipient_connected` remain independent facts, with
unknown connectivity retained as null. Accepted-but-not-persisted is saved as
accepted and never authorizes another send. Saved acceptance survives reopen and
repeated intake. It cannot be downgraded by a stale completion. Lookup returns a
freshly decoded copy; returned objects and consumed request snapshots cannot
mutate the store's private matching intent.

Before SQLite work, a valid receipt is copied privately onto the grant. On save
failure, no durable acceptance is returned, the active marker stays, and another
attempt cannot be reserved in this process. `RetryAcceptance` saves the original
private bytes without sending and without relying on the caller's receipt object.
The future worker must retain this grant and retry saving rather than calling
failure completion or sending again. If the process exits before persistence,
that memory is lost: restart has only the committed attempt marker and must report
uncertainty/duplicate risk under the stored policy. This is not exactly-once
delivery and does not make a storage failure survivable without durable evidence.

## Evidence

SQLite tests cover receipt correlation, unknown versus false facts, nonpersisted
acceptance, full metadata/timestamp preservation, late saving after revocation
and expiry, caller mutation, stale attempt ID/kind fencing, lookup-before-send
denial, retained lookup recovery after reopen, full-size escaped bodies, failed save with private-copy retry, and repeated save/reopen without resend.
Subprocesses exit without cleanup after successful or failed saving; committed
acceptance remains accepted, while failed-save recovery remains uncertain with
the original send budget consumed.

The real pinned daemon fixture receives a journal-admitted operation through the
public SDK with its stable ID. An independent observer confirms author, target,
body and message ID; its retained receipt is recovered through public `RETRY`
after journal reopen, then becomes durable acceptance without a second send.
Reopening/repeating
the request replays that saved receipt and rejects another send reservation. A
reply uses the same path, preserves observed parent/root linkage, and saves a
receipt from a pre-revocation grant after revocation. The fixture owns every
account, process and path and never contacts live AIRC.

```sh
go test -race -count=1 ./internal/gateway/store
go test -race -count=1 ./integration/gateway -run TestWireReceiptBecomesDurableAcceptance
```

Actual backend timeout/write uncertainty, receipt-loss retries, retention eviction,
error completion races and adapter acceptance remain for the next stages.
