# Parent-authorized backend replies

The SDK backend now submits replies through the same consumed grant, pinned
account, bounded exclusive lease, stable ID and receipt-persistence path as
ordinary sends. It first reads the retained parent with public
`RequestContextWithOptions`, requiring exact `REPLIES=1`, `CHAT=1`, `CONTEXT=1`
and `CONTEXT_BYTES=1`. Missing features are upstream incompatibility, not a
transport limitation or permission to skip parent authorization.

The context request uses limit 1 and a 64 KiB complete-response byte budget.
The pinned server prioritizes the exact triggering parent before the root or
other conversation records. Pins and profiles share the response budget; their
bodies are never exposed through this authorization operation. The backend must
receive exactly that parent, a matching trigger ID and a valid root consistent
with the parent's original thread linkage. A missing or omitted parent is not
authorization. Retained parent/root metadata is immutable under the inspected
upstream correction model; corrections preserve the original records and links.

For a room reply, the retained parent room must exactly equal the normalized,
authorized requested room. Room case remains significant. For a DM reply, the
parent's sender and target must be the pinned author and requested DM peer, in
either direction. The gateway does not infer privacy from broad trusted-local
archive access, participant profiles or a supplied parent ID. It uses the native
nickname participant boundary; it does not claim a stronger private tenant model.
A caller cannot point an allowed target at another room's parent or a third
party's DM.

Parent lookup is part of the already budgeted send or receipt-lookup attempt,
not a second grant or an additional receipt-recovery probe. It has a 10-second
phase bound within the original overall deadline. Its deadline callback closes and joins the SDK session. Failed,
late, incomplete or unauthorized parent reads never submit `REPLY`; they report
known no-write evidence for this attempt while preserving any older uncertainty
in the journal. Expiry/revocation do not extend the original grant exception.

After authorization, `ReplyWithID` uses the stored parent, normalized body and
stable upstream ID. The correlated accepted receipt must match both the parent
and the verified root, in addition to the shared receipt contract. A wrong root
after a possible write remains uncertain. Receipt lookup on a fresh lease recovers
the original reply metadata without submitting another reply. Each lookup first
repeats the same bounded parent proof and pins the recovered root to it. If the
parent has been evicted or cannot be proved, the lookup cannot establish acceptance;
the journal preserves prior uncertainty. The journal must
save acceptance before reporting success.

## Evidence

Against the immutable isolated daemon, real backend room and nested replies have
matching author, target, body, parent and root at the independent observer.
Both outgoing and incoming DM parents produce a reply to the explicitly allowed
peer. Lost reply results recover through a fresh public `RETRY` lease and save the
same message ID and linkage.

An owned relay counts actual `REPLY` commands. Foreign-room parents, third-party
DM parents, missing parents and parent-response timeout with continuing unrelated
traffic each produce zero reply commands and known no-write completion. A
corrupted receipt root is rejected while the observer confirms the actual post;
completion retains uncertainty. The same corrupted-root rejection is verified
on retained receipt recovery after journal reopening. A grant committed before
durable revocation still performs exactly one parent-authorized reply and saves its receipt; reuse
of the consumed grant fails.

```sh
go test -race -count=1 ./integration/gateway -run 'TestBackend.*(Replies|Parent|ReplyRoot)'
go test -race ./internal/gateway/backend
```

This completes the backend reply submission boundary, not an ingress reply API.
Automatic retry workers, runtime lifecycle, richer reads/live delivery, native
TLS acceptance and the full transport catalogue remain pending. Separate
phase-only timeout and actual backend blocked-write injection checks also remain.
