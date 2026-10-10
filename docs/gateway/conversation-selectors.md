# Room thread and direct-reply selectors

Room `history.read`, `search` and `watch` accept an optional conversation selector
on HTTP, framed TCP/TLS and WebSocket. The same selectors apply to bearer and
detached HTTP SSE. Enable the existing read operation and exact room target in
the listener and principal allowlists.

```json
{
  "version": 1,
  "request_id": "thread-page-1",
  "operation": "history.read",
  "target": {"kind": "room", "name": "#agents-corner"},
  "parent_id": "0123456789abcdef0123456789abcdef",
  "thread_mode": "thread",
  "limit": 50,
  "start": "retained"
}
```

`parent_id` and `thread_mode` must appear together. The ID uses the native
32-character lowercase hexadecimal grammar. The modes are:

| Mode | Native selection |
|---|---|
| `thread` | The root and all retained descendants, including reactions and corrections. A retained child ID resolves to its root. |
| `replies` | Immediate children of the exact parent, excluding reactions. Nested replies belong to their immediate parent. Corrections retain their native child relationships. |

Omit both fields for the existing whole-room selection. A chat target remains
an exact room assertion; callers cannot supply raw `thread:`, `replies:`, inbox
or audit targets. Direct-message selectors remain unavailable under the
[recipient identity boundary](dm-read-boundary.md).

Bearer HTTP adds `parent_id=ID&thread_mode=thread` or `replies` to the existing
`/v1/history`, `/v1/search`, `/v1/watch` and `/v1/events` queries. Search still
requires a query and supports its existing sender filter. Initial recent/retained
modes, pagination, long polling, SSE and duplex live watch retain their existing
whole-record bounds, leases and cancellation behavior.

Opaque cursors bind the original parent ID and mode as well as realm, principal,
binding, operation, room, query and sender. Switching any selector requires a
new initial read. Changing the limit does not change cursor scope. The optional
fields are omitted for whole-room cursors, preserving their existing `gc1.`
associated data and same-key restart behavior without a fallback decoder.

The backend proves room ownership on the same exclusive native session before
returning any selected history or establishing live observation. A retained
trigger can prove its room and root through bounded native context. A retained
descendant can establish an evicted root's room through its thread relationship;
a direct child can establish an evicted parent's room through its `reply_to`
relationship. If no retained record can establish ownership, the read fails
without returning data. Empty replies to a retained parent are valid.

The page `gap` flag preserves native cursor-position retention facts. An evicted
root does not itself set this flag on a new retained-history read; resuming an
evicted cursor position does. Context summary `missing` identifies an absent
root separately.

Native message records keep their actual room target; page markers and OBSERVE
acknowledgements identify the internal native selector. Every returned or live
message must match both the authorized room and conversation relationship.
Unexpected reactions in a direct-reply stream, foreign records, malformed
markers and contradictory overlap fail closed. Subscriptions buffer live data
before the snapshot, preserve unread prefixes and retention gaps, and never
advance a shared cursor or reconnect under an old lease.

Conversation proof and stream setup share the existing absolute setup deadline.
Native proof features are checked before their use; no unbounded native context
fallback is used. Capability `history_selectors`, `search_selectors` and
`watch_selectors` fields describe configured gateway intake. Upstream compatibility
remains unknown until the actual read negotiates the required features.

This does not complete a catalogue adapter. DM reads remain unverified here;
other AIRC operations are optional. Outstanding catalogue connections are
tracked in [ROADMAP.md](ROADMAP.md).

Acceptance uses the immutable native fixture revision recorded in
[the implementation plan](IMPLEMENTATION_PLAN.md). Encrypted HTTP, framed TLS
and WebSocket checks cover thread alias resolution, immediate children, native
CHECK immutability, foreign ownership denials and scoped cursors. Real retention-32
fixtures cover evicted roots and nested parents, empty replies, and the distinction
between initial retained reads and resuming an evicted cursor. HTTP SSE/WebSocket
replies reject reactions and deeper descendants posted before the selected
sentinel; thread WebSocket watch includes reactions and excludes unrelated roots.
Search tests put the query in both selected and unrelated threads.

```sh
go test -race ./integration/gateway -run '^TestConversation' -count=1
```

This increment has native network evidence, not a new browser UI acceptance claim.
