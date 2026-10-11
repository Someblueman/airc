# Bounded native room watches

`GET /v1/watch?kind=room&target=%23room&limit=50` returns one JSON page using
native `OBSERVE` and `CHECK`. This is a resumable long-poll mode. It is not an SSE
stream. A separate [SSE route](events.md) streams the same events.
WebSocket and other catalogue modes remain required work.
Direct-message watch mapping remains pending. Native nonmessage room events use
the separate [events.watch operation](live-events.md).

The typed selector, limit (1–1000, default 50), `cursor` and initial `start`
parameters use the same strict query parsing as history. Paired
`parent_id`/`thread_mode` parameters select a room thread or direct replies; see
[conversation selectors](conversation-selectors.md). Without a cursor, watch
starts with recent retained messages; `start=retained` explicitly starts the
retained prefix. Initial `start=recent` is also accepted. A resumed cursor cannot
be combined with a start mode. Query/sender filters, audit selectors, unknown or
duplicate parameters, raw IDs and invalid UTF-8 fail.

Every poll checks the listener's access policy and obtains one one-use journal
read lease as its author; AIRC decides whether that author may read the room.
It creates no delivery job or attempt budget. The opaque cursor binds the
realm, author scope, `watch` operation and exact room. History and
search cursors cannot be reused. Use the same provisioned cursor key across
process restarts; rotation invalidates cursors as described in [cursors-v1.md](cursors-v1.md).

The exclusive ephemeral SDK session first receives the exact native subscription
acknowledgement, then requests its history snapshot. Live messages are bounded
and buffered from subscription setup, including before a delayed acknowledgement
and during the snapshot. Missing or rejected acknowledgements discard the complete
setup without exposing any buffered data or beginning CHECK. Snapshot/live overlap is deduplicated by
ID with immutable message-field correlation, preserving richer snapshot metadata.
Buffered overlap is correlated even on gap or `more` pages. If history has more
pages, return the contiguous retained prefix before new live records; callers resume that cursor until caught up. A retention gap remains
explicit, including on an empty page. No reconnect, silent gap reset or shared
cursor advancement occurs.

When caught up with no messages, the poll waits up to two seconds for a native live
message. Unrelated traffic never resets that timer. An idle poll returns an empty
`messages` list, `status: "ok"`, and the snapshot position when one exists. An
entirely empty archive has no position to invent. A live event wakes the poll,
returns its complete body/reply/root/chat metadata, and supplies a new opaque
cursor. Live SDK events contain no sequence/retained receipt facts; omitted facts
remain unknown. Stored snapshot receipts, when provided, retain their own facts.

The shared HTTP bound is four seconds, within the five-second write deadline;
registration or snapshot stalls fail within this enclosing bound. Caller/service
cancellation closes and joins the SDK session. Disconnects, malformed records or
markers, foreign targets, count/byte overflow and exhausted capacity return errors
without a partial page. Total encoded response is capped at 64 KiB, including
cursor framing. Internal snapshot and live buffers each have that byte cap and
the requested record-count cap.

Each watch consumes one bounded lookup/session slot plus the configured global
and per-author watch slot. Lookup accounting preserves a global and per-caller
send slot; watch slots are released after SDK cleanup. Enabled HTTP watch requires
a dedicated cursor key, at least two global and per-author sessions, and
nonzero global/per-author watch limits. Capability discovery returns scoped
`watch_targets`, `max_watch_bytes`, and `watch_mode: "long_poll"`; it does not claim
upstream compatibility before negotiation. Runtime requires ephemeral mode,
`CHECK=1` and the exact native OBSERVE acknowledgement.

Real isolated evidence covers HTTPS HTTP/1.1 and HTTP/2 live wakeup after an empty
snapshot, independent observer/receipt agreement, scoped cursor/parameter denials,
revocation, quiet polls, retained prefix/resume and eviction. A transparent owned
proxy forces a live reply between snapshot data and terminator and duplicates a
live/snapshot record while preserving its stored sequence. Delayed-ack tests buffer
a real live message before the acknowledgement; missing/rejected acknowledgements
return no data and never begin CHECK. The proxy tests idle polls
under continuous noise and verifies disabled CHECK never subscribes. Cancellation,
oversized snapshots and active-watch CLI SIGINT join; real CLI cursor pagination
survives restart with the same key. Focused unit checks cover watch limits/send
reserve, overlap correlation, foreign records and count overflow.
