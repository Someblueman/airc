# Bounded native SSE room messages

`GET /v1/events?kind=room&target=%23room&limit=50` returns UTF-8
`text/event-stream`. Name the author with `AIRC-Nick` and, for a registered
account, `AIRC-Token`. The route uses the same `watch` operation, listener
targets, dedicated cursor key and author-scoped opaque cursors as
[long polling](watch.md). A long-poll checkpoint can resume SSE
and vice versa. Direct-message reads remain unimplemented. Nonmessage room events use the separate
[events.watch profile](live-events.md).
Browser Fetch supports configured exact origins; see [browser.md](browser.md)
and [the one-lease example](../../examples/gateway/events-fetch.js). Native
EventSource cannot supply author headers through its constructor; use Fetch,
or the JSON-RPC stream (`POST /v1/rpc/events`, [jsonrpc.md](jsonrpc.md)),
which carries the author in its body.
The framing follows the [WHATWG SSE protocol](https://html.spec.whatwg.org/multipage/server-sent-events.html).

Parameters are exactly those of `/v1/watch`. Initial `start=retained` reads the
retained prefix; omitted start uses recent history. Resume with `cursor` or a
single `Last-Event-ID` header containing the opaque event ID. If both are supplied
they must agree. Resumed requests cannot include `start`. Raw upstream IDs,
another author's cursors, foreign selectors or operations, malformed cursors and unknown/duplicate
query parameters fail before any upstream session is allocated. The listener's gate
and the author are checked again for every renewed lease; an already authorized
lease may finish within its original 15-second deadline.

The first `event: page` carries one complete JSON HistoryPage, including explicit
`status` and `gap`. Its `id` is the same opaque cursor in that page, omitted only
when no position exists. The first frame also supplies `retry: 1000`. Successful
setup creates one exclusive ephemeral SDK session, subscribes before CHECK and
correlates bounded buffered live overlap as described in [watch.md](watch.md).
A `more` or gap page ends the lease with `event: end`, `{"reason":"resume"}`;
resume the delivered checkpoint to consume the next contiguous retained prefix.
No later live message overtakes an unread prefix. Empty archives never invent a
checkpoint. Retention expiry remains an explicit gap.

After catching up, native messages produce individual whole page events over the
same upstream subscription. Previously delivered IDs are deduplicated and their
immutable fields must agree. Metadata absent from SDK live events remains unknown.
Quiet streams send `: keepalive` comments every two seconds without advancing IDs.
At the original authorization deadline, the stream ends with
`{"reason":"lease_expired"}`. Renew from the last complete page ID; automatic
network reconnection is the client's responsibility and must be bounded. Treat
uncertain network delivery as allowing repeated messages, and deduplicate by ID.

Setup, including dial/login/acknowledgement/snapshot, is bounded by four seconds
from grant issuance. Before the first page, failures use ordinary typed HTTP JSON
errors. After headers, an upstream failure ends with `upstream_unavailable`, and
resource exhaustion ends with `capacity`; terminal events have no ID. A truncated
frame is not a checkpoint. Resume from the last completely decoded page, with
retention gaps checked normally. A terminal reason is not proof of successful
receipt by the client.

Each synchronous write/flush has a three-second deadline, overriding the normal
five-second HTTP response bound only for this stream. No unbounded outbound queue
is added. Each whole frame is at most 64 KiB, and a lease permits at most 128 data
or keepalive frames and 1 MiB on the wire, reserving 1 KiB for the terminal frame.
The backend additionally bounds distinct live records to 128 per lease. Watch
and session quotas, including the send reserve, remain in force. Cancellation,
write failure, setup rejection, capacity, expiry and service shutdown close and
join the SDK session before releasing capacity.

Verified against isolated pinned AIRC fixtures using real TLS clients:

```sh
go test -race ./integration/gateway -run 'HTTPEvents|StreamingEvents' -count=1
```

These tests cover HTTP/1.1 and HTTP/2 multi-message live streams, opaque ID scope,
cancellation join, retained-prefix Last-Event-ID resume,
quiet expiry beyond the normal write timeout, an abandoned HTTP/2 response body,
independent caller progress, and shutdown of the actual `airc-gateway serve`
process while streaming. Browser Fetch also passes the isolated loopback probe described in [browser.md](browser.md).
Abrupt process restart, all malformed
upstream-event boundaries and complete SSE catalogue acceptance are not claimed.
