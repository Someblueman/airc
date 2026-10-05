# Bounded room conversation context

`context.read` retrieves native conversation context through HTTP, framed
TCP/TLS and WebSocket. Enable the operation in both listener and principal
allowlists, with the exact room target. This is a read, with no delivery job,
retry, cursor advancement or AIRC mutation.

```json
{
  "version": 1,
  "request_id": "conversation-1",
  "operation": "context.read",
  "target": {"kind": "room", "name": "#agents-corner"},
  "parent_id": "0123456789abcdef0123456789abcdef",
  "limit": 50,
  "max_bytes": 65536
}
```

The trigger `parent_id` must be a retained native message ID. Omitted or zero
`limit` defaults to 50; nonzero limits permit 1–1000 records. Omitted or zero
`max_bytes` defaults to 65536; nonzero budgets permit 1024–65536 bytes.
The native budget includes complete context wire records;
the gateway also bounds its encoded context result. Records are returned whole,
including original bodies encoded as unpadded base64url in `body_base64`.
An insufficient budget for protected trigger/root/correction records fails the
read rather than returning truncated bodies.

Bearer HTTP uses `GET /v1/context?kind=room&target=%23agents-corner&parent_id=ID`,
with optional `limit` and `max_bytes`. Signed/encrypted HTTP envelopes and duplex
calls return the context in the operation-specific `context` result field.
No cursor, start selector, query, sender filter or mutation fields are accepted.
Context-only configurations require two global and per-principal native session
slots to preserve send capacity; they need no cursor key or watch quota.

The result's `summary` preserves native `trigger_id`, `root_id`, `missing`,
`protected_ids` and omission counts, alongside `messages`, room `pins` and public
`participants` cards. Empty record collections are omitted. Messages retain
reply/thread/reaction links, correction and retraction metadata, sequence numbers,
author/account facts and timestamps. Omission counts describe retained content
excluded by limits; they cannot count older evicted replies.

One authorized read lease owns one exclusive native session. Before retrieving
the requested snapshot, the backend proves that the retained trigger belongs to
the exact permitted room using a one-message, 64 KiB context request. It checks
the snapshot's room ownership and thread linkage before returning any data.
An evicted root can be reported honestly when the retained trigger establishes
its thread ID. An absent trigger or foreign-room trigger cannot establish room
ownership and returns no context data. Direct-message context remains unavailable
under the [recipient identity boundary](dm-read-boundary.md).

The backend requires native `CONTEXT=1` and `CONTEXT_BYTES=1`, with no unbounded
fallback. Collection has independent count/byte checks, fixed lease deadlines,
joined cancellation and validation of messages, pins, cards and summaries.
Capabilities describe configured intake permissions; upstream compatibility is
still unknown until the actual read negotiates its native features.

Room thread pagination and direct-reply selectors are described in
[conversation selectors](conversation-selectors.md). This addition does not
complete any catalogue adapter. DM reads and profile/presence writes remain unverified here. Other native
operations are optional; outstanding catalogue connections are tracked in
[ROADMAP.md](ROADMAP.md).

## Acceptance evidence

```sh
go test -race ./internal/gateway/backend -run 'TestBoundedContext|TestContext' -count=1
go test -race ./integration/gateway -run '^TestContextRead' -count=1
```

The owned TCP collector fixture includes a valid complete response at its exact
wire budget, a one-byte-under-budget rejection, complete malformed responses,
and a withheld terminator under continuous unrelated traffic. The real pinned
daemon tests use one standalone gateway with HTTP, framed TCP and WebSocket
listeners. They verify encrypted context across all three, signed HTTP context,
explicit HTTP/1.1 and HTTP/2, defaults and strict queries, whole Unicode bodies,
reply/reaction/correction links, pins, profiles, record/byte omissions, foreign
and missing trigger denials, and an evicted-root gap. Native CHECK snapshots are
unchanged after reads and denials; no delivery receipt is created.

The final focused integration race run passed in 14.715 seconds. This increment
does not claim a context-specific browser, remote WSS or native device check.
