# Bounded native live events

`events.watch` combines retained messages with native live room events. It requires
its own explicit listener and principal permission; `watch` remains message-only.
Use `GET /v1/live` for a bounded bearer long-poll or `GET /v1/live/events` for SSE.
Signed/encrypted HTTP, framed TCP/TLS, WebSocket/WSS, Unix and stdio requests select
`operation: "events.watch"` and support ordinary pages and bounded streams. The reduced MCP interface does not
expose nonmessage events; `wait` uses message-only `watch`.

The request uses the existing room selector, limit, start and opaque cursor
fields. Thread and reply selectors restrict signals to proven conversation
parents. A parent outside the initial snapshot is checked using native context
on the same private observation session. No room JOIN or persistent identity is
introduced. Direct-message observation remains pending.

Pages keep `messages` and add an optional `events` array. Each event has `kind`
and exact room `target`, plus native fields appropriate to its kind:

| Kind | Native fields |
|---|---|
| `join`, `part`, `quit` | member; departure reason when supplied |
| `kick` | actor, member, reason |
| `topic` | actor, topic (an explicit empty string means cleared) |
| `mode` | actor, member, mode (`+o` or `-o`) |
| `signal` | complete native signal object: typing, thinking, prepare, cancel or results |

Live events have no invented occurrence ID, timestamp or retained sequence.
Signal IDs identify a parent or poll, not an event occurrence. Nonmessage pages
preserve the exact opaque message checkpoint; messages advance it. Cursors bind
the operation, author scope and selector, so a `watch` cursor cannot be
reused for `events.watch`. Live events are not retained or replayed: cancellation,
renewal or disconnection can lose them. Retained messages remain resumable.

The pinned native observer broadcasts visible persistent departures, membership,
operator modes, topics and signals. It does not broadcast nickname changes,
profile/presence updates, room-setting changes or pin mutations to this private
observer. Their existing snapshot reads remain available.

The existing one-use read lease, revocation checks, session reserve, watch quotas,
response byte bounds and cancellation cleanup apply. Mixed live buffers and
streams have a 128-record ceiling; requested page limits and the 64 KiB encoded
page ceiling also apply. Malformed native fields, foreign targets, unprovable
conversation scope and overflow fail without exposing a partial page. Raw native
signal JSON is validated before typed conversion, including duplicate and unknown
fields. Quiet long-polls return after the existing bounded wait.

## SDK raw-signal boundary

The roadmap review retains `Client.ObserveSignalsRaw` only for the existing
optional `events.watch` implementation. It issues native `OBSERVE` and selects
raw decoding only for numeric 780; numeric 777 message events remain typed.
Preserving the original signal envelope and JSON lets the gateway reject foreign
recipients, unexpected tags, duplicate/unknown/null fields and invalid signal
unions before normalization. Ordinary message-only `watch` uses typed `Observe`,
which restores typed signals. The gateway owns one selected room's event stream
on its private read session; the SDK hook supplies no additional permission,
persistence or replay guarantee.
`TestObserveSignalsRawPreserves780AndLeaves777Typed` covers envelope preservation
and copied wire fields; the native restoration test covers switching back to
`Observe`. `TestNormalizeLiveEventSignalsStrictly` and the live-bound tests cover
strict normalization and resource limits. No additional raw-signal API is needed.

Isolated native tests compare whole signal objects with an independent observer,
exercise JOIN/PART/QUIT/KICK, modes, topic changes and clears, prove an omitted
thread parent, reject foreign cursors, and show a message advancing an unchanged
nonmessage checkpoint. Carrier tests cover HTTP/1.1 and HTTP/2, signed/encrypted
SSE, framed encryption/TLS, WSS, Unix, stdio and MCP pages/progress. This increment
does not complete the connection catalogue. Additional native operations are optional.

Earlier MCP live-signal acceptance applies to the superseded operation
interface; the four-tool replacement rejects `events.watch`.
