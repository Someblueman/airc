# Bounded room pin snapshots

`pins.read` returns the native pin snapshot for one authorized room. The shared
operation is implemented for HTTP, framed TCP/TLS and WebSocket. This is an implementation
increment, not completion of a catalogue adapter.

The request contains `version`, `request_id`, `operation: "pins.read"` and an
exact room `target`. It accepts no parent, thread mode, cursor, start, query,
limit, byte budget or mutation fields. Bearer HTTP uses
`GET /v1/pins?kind=room&target=%23room`; only `kind` and `target` are accepted.
Detached results use the `pins` field containing `target` and `pins`.

Enable `pins.read` in both listener and principal operations and provision the
exact room target. The existing one-use fifteen-second read lease, immutable
binding, durable revocation and bounded native session pool apply. A lease
admitted before revocation may complete until its fixed deadline; subsequent
reads fail current authorization. Pin reads create no delivery job, retry,
account, shared cursor or pin mutation. They need no cursor key; configure at
least two global and per-principal sessions to preserve send capacity.

The backend negotiates `CHAT=1` and requests native `CHAT` action `pins`. A valid
response contains zero through five whole pin records and the native chat
terminator. Each entry and its embedded message must match the authorized room;
entry ID and sender must agree with the embedded message. Metadata, UTF-8 text,
duplicate IDs, framing and encoded response bounds are validated before any
snapshot is returned. Missing terminators, malformed or unrelated chat entries,
foreign-room data and cancellation fail without partial data.

Pins are native durable room records and can survive message-history eviction.
The returned annotation preserves correction, retraction, reply and reaction
facts supplied by AIRC. A pin does not require a retained history parent or root.
An empty pin set is a real empty array, not an invented history snapshot.

Capability target and limit fields describe configured gateway intake. Runtime
upstream support remains unknown until the read negotiates the native feature.
DM pins are unavailable because the native operation selects room pins only.

## Acceptance evidence

```sh
go test -race ./internal/gateway/backend -run '^TestBoundedPinsCollector' -count=1
go test -race ./integration/gateway -run '^TestPinsRead|^TestPinnedRecord' -count=1
```

The actual SDK TCP collector fixture exercises complete empty and populated
responses, exact wire-budget acceptance, one-byte overflow, six distinct pins,
duplicates, malformed/mixed entries, identity/room mismatches, and a withheld
terminator under continuous NOTICE traffic. Cancellation must reach the context
deadline and return no data.

The pinned daemon and standalone three-listener gateway verify encrypted reads
with identical whole multiline/Unicode snapshots, explicit HTTP/1.1 and HTTP/2,
signed HTTP/2 envelopes, bearer routing, explicit empty arrays after
native unpin, strict/duplicate query and room permission denials, and no delivery
receipt or native history mutation. A separate retention-32 check confirms that
the pin and its correction both leave CHECK history while the pin snapshot still
returns the original body and correction annotation. The final focused race run
passed in 7.717 seconds after the final effective-deadline check. This does not claim pin-specific browser or native
device acceptance.

Full module normal/race runs passed (gateway integration 339.913s/344.944s).
Those runs preceded the final two-line effective-deadline correction; the backend
full race suite and pin integration race group passed afterward. Vet, staticcheck,
build, formatting and dependency checks also passed.
