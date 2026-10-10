# Room settings and waiting snapshots

`room.read` and `waiting.read` extend the shared HTTP, framed TCP/TLS and
WebSocket intake. They are increments toward the complete catalogue.

Both requests require an exact authorized room target. `waiting.read` also
requires a canonical lowercase 32-character `parent_id`. Neither accepts
mutation fields, selectors, cursors, limits or query filters. Bearer routes are
`GET /v1/room?kind=room&target=%23room` and
`GET /v1/waiting?kind=room&target=%23room&parent_id=...`. Other query fields and
duplicate fields are rejected. Detached results use `room` and `waiting`.

Room results contain `target`, `slow_seconds` and `history_limit`. These are
stored native room settings; a zero history override does not reveal the
daemon's effective global history limit. The native getter explicitly sends
seconds and limit as -1, preventing an accidental room configuration write.
One matching room entry and the native terminator are required, within 4 KiB.

Waiting results contain `target`, `parent_id` and an explicit `signals` array.
Before requesting signals, the same exclusive native session proves that the
question is retained in the authorized room. This proof is required even for
an empty snapshot. Only matching native prepare entries are accepted, with
distinct account or guest identities, at most 1024 entries and 64 KiB.
`expires_at` preserves the native timestamp; it can pass during transfer and is
not a gateway promise that a signal remains active after receipt.

Both operations use existing one-use read leases, immutable bindings, current
authorization and bounded native sessions. Enable them in listener and
principal operation lists, configure exact room targets and reserve at least
two global and per-principal sessions. No cursor key is needed. Reads create
no delivery job, receipt, retry, account or mutation. Malformed, foreign,
duplicate or incomplete responses fail without partial results. Capability
fields describe configured intake; runtime upstream compatibility remains
unknown until native features are negotiated.

## Acceptance checks

The SDK TCP collector fixtures exercise zero/empty results, required
terminators, exact wire boundaries, malformed and mixed records, foreign
identities, duplicate actors, count overflow and cancellation under unrelated
traffic. The pinned daemon and standalone gateway tests exercise nondefault
room settings (17-second slow mode and history override 12), matching snapshots
across encrypted HTTP/framed/WebSocket, explicit bearer HTTP/1.1 and HTTP/2,
signed HTTP/2, multiple actors, native cancellation and expiry, strict query
and credential denials, foreign-parent rejection even with no signals, and
absence of delivery receipts or native history mutation.

```sh
go test -race ./internal/gateway/backend -run 'TestBounded(Room|Waiting)Collector' -count=1
go test -race ./integration/gateway -run '^Test(Room|Waiting)' -count=1
```

These checks do not claim operation-specific browser or hardware acceptance.

Full module normal/race runs passed (gateway integration 411.213s/417.621s).
The final collector test-only additions subsequently passed their focused race
group in 1.621s. Vet, staticcheck and the gateway build passed; independent
review's integration race group passed in 12.624s. No native daemon contract or
dependency changed.
