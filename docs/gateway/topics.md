# Room topic snapshots

`topic.read` reads the native durable header of one authorized room through
HTTP, framed TCP/TLS, WebSocket, Unix stream and stdio intake. Topic writes
use [topic.set](topic-set.md). Live nonmessage updates remain unverified here;
this does not complete an adapter.

HTTP uses `GET /v1/topic?kind=room&target=%23room` with the author headers.
Only `kind` and `target` are accepted, once each. `POST /v1/request` and
correlated stream requests carry the same room intent:

```json
{"version":1,"request_id":"topic-read-1","operation":"topic.read","target":{"kind":"room","name":"#room"}}
```

No parent/poll selector, cursor, paging, message, delivery policy or timing
arguments apply. The listener must allow the operation and the room. A one-use
read grant run as the author owns the bounded native session. Reads create no
delivery job. Topic-only configurations require two global and per-author sessions to
preserve send capacity, but no cursor key or watch quota. Capabilities expose
configured `topic_targets` and `max_topic_bytes: 4096`, with upstream compatibility
unknown until a request executes. TOPIC is a baseline native command and does
not require CHAT negotiation.

The result contains `target` and whole `topic`. A populated header also includes
`set_by` and UTC `set_at`, preserving the native setter nickname and Unix-second
timestamp from numeric 333. There is no setter account identity on this native
response. With no topic, the text is empty and setter/time are omitted. Clearing
the native topic returns that empty fact; the gateway does not invent a timestamp
for absence. Topic state survives history eviction and needs no history proof.

A populated response requires its matching topic event and complete metadata;
missing, duplicate-before-metadata, malformed and foreign responses fail without
partial facts. Numeric 333 must have the exact room/recipient, valid setter and
canonical representable timestamp. Native text is bounded to 400 UTF-8 bytes
without controls. Canonical native response encoding and the whole gateway result
are bounded to 4 KiB, with effective deadline checks before emission.

The pinned SDK collapses raw 331 and 332 into TopicEvent and discards their
recipient/parameter structure. Thus validation of those numerics covers the
exposed channel/text, not their complete raw structure. An empty event is the
native terminal no-topic response; a populated event waits for raw 333 metadata.
There is no separate end marker after these native terminal responses. The
accounting uses canonical encoding of exposed fields rather than discarded raw
parameters. Numeric 333 retains its raw fields and is checked directly.

Collector race checks cover whole Unicode text/metadata, empty headers, exact
byte boundaries, missing/duplicate/foreign metadata, malformed timestamps and
cancellation under continuous unrelated traffic. Final collector race checks
passed in 1.513s, including the valid 400-byte header and Unix epoch timestamp.
The gateway packages and CLI passed race checks. Final topic and shared
TCP/WebSocket/Unix conformance race checks passed in 53.884s on Darwin: whole
headers and metadata survive history eviction, clearing returns the whole empty
fact, typed denials disclose no result, and native history/receipt checks show
Dated records follow. Where they mention principals, bindings, signed or
encrypted envelopes, signing keys or return routes, they describe the
[removed identity model](removed-identity.md), which no longer exists.

no delivery mutation. HTTP/1.1, HTTP/2, signed HTTP/2, framed TCP, WebSocket and
Unix streams are exercised against isolated pinned native fixtures. Native
setters acknowledge changes before independent queries, and an independent
observer verifies the live header. Vet/staticcheck/build and format checks pass.
Linux runtime and complete catalogue acceptance remain pending.
