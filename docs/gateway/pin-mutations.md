# Pin mutations

`pins.add` and `pins.remove` mutate the native pin list for one exact authorized
room and an existing message ID. They use the same durable request/status path
as the other mutations:

```json
{"version":1,"request_id":"pin-1","operation":"pins.add","target":{"kind":"room","name":"#room"},"parent_id":"0123456789abcdef0123456789abcdef"}
```

Both listener and principal must allow the operation and exact room. Message,
reaction, poll, profile, presence, topic, room-setting and read selectors are
invalid. No account or operator provisioning occurs on behalf of a request.
Native room posting restrictions remain authoritative; pin writes do not
require native channel-operator authority.

Native `CHAT pin/unpin` ignores a supplied room and resolves the ID itself.
Before any write, the gateway proves the exact ID/room relation on the same
exclusive session. Add uses the bounded retained context proof. Remove first
reads the authorized room's pins, allowing removal after history eviction;
when absent, a retained context proof also permits native no-op removal of an
already unpinned message. A missing or foreign proof never reaches the writer.
Message IDs do not move between rooms. If a pinned message disappears between
proof and mutation, a native rejection remains a rejection.

The backend requires negotiated `CHAT`, `CONTEXT` and `CONTEXT_BYTES`. Proof,
write and acknowledgement phases have absolute bounds of 5, 5 and 10 seconds,
all within the attempt deadline. A whole exact pin/unpin record followed by
the native terminator confirms acceptance; malformed, duplicate, foreign,
mixed or incomplete evidence cannot do so. The canonical response is bounded
to 4 KiB. Validation covers decoded SDK fields; recipient fields and unknown
JSON extensions discarded by the SDK cannot be recovered or validated here.

The durable `pin` acknowledgement contains version, operation, target and
`parent_id`. This identifies the existing pinned message, not a newly created
chat post. The top-level message ID remains empty, and persistence/recipient
facts remain null because the native ACK has no such facts. A later pins read
is a snapshot, not evidence that a particular uncertain request succeeded.

Stable IDs coalesce identical intent and conflict on changed intent. Saved
acceptance returns the original ACK after a gateway restart, without another
native write even when pins subsequently change. Native pin writes have no
request-ID lookup. Default retries repeat the normalized tuple within the
stored send budget, preserving uncertainty and duplicate risk; native repeated
add/remove is idempotent while the required message/pin remains available.
`receipt_only` does not resend a possibly accepted write, and lookup attempts
remain zero. Explicit native quota/validation/mute failures remain typed;
native mute code 485 is a permanent forbidden write, not acceptance uncertainty.

Schema 7 adds `pin_outcome`, mutually exclusive with receipt, card, topic and
room outcomes. Schemas 1–6 migrate transactionally without changing saved
evidence. A failed save preserves immutable private ACK bytes for persistence
retry, fencing completion and further network work while acceptance is known.
Result retention and pruning include pin bytes.

## Verification

All pin native transport race tests passed in 62.909s. They exercise bearer
HTTP/1.1/HTTP/2, signed HTTP/2 with actual protocol assertions, encrypted HTTP,
TCP, WebSocket, framed TLS, Unix streams and real stdio processes. Independent
native pin snapshots verify both writes. Original encrypted tokens survive
stdio restarts: saved add replay after native unpin leaves the message unpinned,
and saved remove replay after native pin leaves it pinned, with one send and
zero lookups. Signed HTTP replay and changed-ID-intent conflict are also covered.

Authorization controls reject forged cross-room IDs without changing foreign
pins. A positive history-eviction control proves a stored pin outlives the
message before removing it; an evicted add lacks proof and is blocked. Retained
already-absent remove succeeds. Five pins succeed; a sixth is rejected with
native 461. Native mute blocks removal with 485 without changing pins, and
unmute permits a new request. Invalid intent and unauthorized targets create
no job. At the journal crash boundary, acknowledged-but-unsaved add and remove
recover with a second bounded send under retry, or retain unresolved uncertainty
without another write under receipt-only. Native state is verified in both cases.

Final focused backend/core/auth/store race checks passed in
2.029s/1.978s/2.455s/7.607s. Collector controls exercise foreign proof without a
write, malformed/mixed/duplicate ACKs, stored-pin removal and continuous noise
during proof and ACK phases. Signed and encrypted validity controls cover both
operations, missing inner times and mismatches with authenticated outer times.
Journal controls cover immutable failed-save evidence, completion fencing,
whole accepted restart, no resend, retention, pairwise INSERT/UPDATE exclusivity
with single-outcome positive controls, and schema 1–6 migration preservation.
The initial INSERT positive control reused an upstream ID and failed its unique
constraint; it was corrected before the final passing store check.

Independent source review found no must-fix defect. Initial full normal and
race runs failed because the filesystem filled during repeated per-fixture
native exports/builds (integration 385.239s/409.101s); they are not passing
evidence. A scan identified 116,921 compiled cache files containing gateway
fixture source paths, totaling 23,112,227,546 bytes. Those inactive generated
files were removed while unmatched cache was preserved. Native fixture code
now builds one checksum-verified immutable executable per test process with
`-trimpath`, keeping every daemon's accounts/state/socket/lifetime private.
The pin and real two-daemon isolation race group passed in 58.697s after this
change. The full normal suite subsequently passed, including gateway integration
in 474.686s and store tests in 5.296s. The full race suite also passed, including
gateway integration in 484.524s and store tests in 37.250s. Vet and staticcheck
passed. Module tidy had no diff; the actual disabled-example CLI checks passed
without starting a listener. No dependencies or native source were changed.
Fixtures are pinned and disposable; the live daemon and primary
checkout remain outside the test boundary. HTTP, TCP/TLS, WebSocket, Unix stream
and stdio share this implementation.
The complete catalogue remains outstanding. Additional native operations are
optional; nonmessage live updates and DM reads remain unverified here; no adapter is complete.
