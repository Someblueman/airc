# Streams and local IPC

These carriers move [contract](contract-v1.md) requests inside a *frame*
over a byte stream, a local socket or a pipe. The local ones are gated by
the boundary they already have (owner-only paths, peer credentials, the
parent process); requests and
replies, tokens included, are plaintext to anyone who can read that channel.
AIRC sees `listener:<id>` as their source; a Unix stream socket names each
client process, `listener:<id>:pid:<pid>`, so per-source limits apply to
each process rather than to everything sharing the socket.

## Frames

A request frame wraps one contract request; a response frame carries the
detached result:

```json
{"version":1,"correlation_id":"c1","profile":"plain",
 "request":{"version":1,"request_id":"r1","operation":"send",
            "target":{"kind":"room","name":"#ops"},"message":"hi",
            "author":{"nick":"builder"}}}

{"version":1,"correlation_id":"c1","event":"result","result":{...}}
{"version":1,"correlation_id":"c1","event":"error","error":{"code":"forbidden","transient":false}}
```

`correlation_id` is 1–64 letters, digits, `-` or `_`, fresh per request;
reusing an active one closes the connection. `profile` is optional and, if
present, `plain`. Retry a mutation by resending the exact request under a
new correlation ID. With `"stream": true` the request must be a watch or a
check: a watch answers with `event: "page"` frames (an empty page is a
keepalive), a check with `event: "check"` frames holding a whole check
result ([following](contract-v1.md#following); an empty one is a
keepalive), and both end with `event: "end"` and a `reason`. A follow's
frames are bounded in bytes, not in number. Requests are at most 16 KiB and responses
64 KiB; whole objects or `capacity_exceeded`, never truncation.

On byte streams each frame is a four-byte big-endian length followed by
the JSON. Packet carriers send one frame per record, or split it into
[datagram fragments](network.md#datagram-fragments).

## Framed TCP and TLS

`protocol: "framed"`. TLS is optional; with it the gateway requires TLS 1.3
and ALPN `airc-gateway/1`, and clients verify the certificate.
`account.identify` makes its author the connection's default until it
closes. Four requests may be active per connection. The first byte must
arrive within five seconds, later frames within thirty of the last active
request's end (while a request is active, a follow for instance, the
connection waits instead), and the rest
of a started frame within five; writes and TLS handshakes have three seconds,
calls four and streams twenty (at most 128 frames, 1 MiB). A disconnect
cancels reads; admitted mutations continue.

```sh
go run -C gateway ./examples/framed-client --address 127.0.0.1:6683 \
  --correlation c1 < request.json          # --ca FILE for TLS, --stream for a watch
```

## Unix sockets

| `protocol` | Socket | Requests | Replies |
|---|---|---|---|
| `unix` | stream, Darwin and Linux | length-prefixed frames | same connection |
| `unixpacket` | `SOCK_SEQPACKET`, Linux only | one frame per record | one record per frame |
| `unixgram` | datagram, Darwin and Linux | [fragments](network.md#datagram-fragments) of at most 1000 bytes | fragments to the sender's bound address; an unbound sender gets none |

`address` is an absolute normalized path (at most 103 bytes on Darwin, 107
on Linux). The parent directory must belong to the gateway user with no
group or other access, every ancestor must belong to root or that user
(root-owned sticky directories such as `/tmp` excepted), and symlinks are
refused. The gateway creates the socket with mode 0600, refuses an
existing path, and removes it on shutdown only if it is still its own
inode. Stream and seqpacket peers must have the gateway's effective UID
(`LOCAL_PEERCRED` on Darwin, `SO_PEERCRED` on Linux). A seqpacket record
with truncation or ancillary data is refused. `unix` takes no TLS; the
framed example client accepts `--network unix`.

## Parent-owned streams

One child process serves one stream from its parent, with exactly one
enabled listener (`stdio`, `socketpair` or `inherited`, no address) and
an explicit `operations` list:

| Command | Stream |
|---|---|
| `airc-gateway serve-stdio --file CONFIG` | stdin and stdout, which must both be pipes |
| `airc-gateway serve-socketpair --file CONFIG` | descriptor 3: one end of an unnamed `AF_UNIX` stream socket pair |
| `airc-gateway serve-inherited --file CONFIG` | descriptor 3: a connected `AF_UNIX` stream (named or unnamed) |

Frames are length-prefixed. Diagnostics go to stderr and nothing else to
stdout; there is no ready record. The parent must read responses while it
writes requests. A clean EOF between frames drains admitted handlers and
allows up to five seconds for pending deliveries; unfinished jobs resume
when a child next opens the journal. A partial frame, I/O error or signal
cancels and joins the work. `serve` refuses these listeners.
