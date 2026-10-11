# Streams and local IPC

These carriers move [contract](contract-v1.md) requests inside a *frame*
over a byte stream, a local socket, a pipe, a serial line or a kernel queue.
The local ones are gated by the boundary they already have (owner-only
paths, peer credentials, the parent process, the caller UID); requests and
replies, tokens included, are plaintext to anyone who can read that channel.
AIRC sees `listener:<id>` as their source.

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
new correlation ID. With `"stream": true` the request must be a watch and the
answers are `event: "page"` frames (an empty page is a keepalive) and a final
`event: "end"` with a `reason`. Requests are at most 16 KiB and responses
64 KiB; whole objects or `capacity_exceeded`, never truncation.

On byte streams each frame is a four-byte big-endian length followed by
the JSON. Packet carriers send one frame per record, or split it into
[datagram fragments](network.md#datagram-fragments).

## Framed TCP and TLS

`protocol: "framed"`. TLS is optional; with it the gateway requires TLS 1.3
and ALPN `airc-gateway/1`, and clients verify the certificate.
`account.identify` makes its author the connection's default until it
closes. Four requests may be active per connection. The first byte must
arrive within five seconds, later frames within thirty, and the rest of a
started frame within five; writes and TLS handshakes have three seconds,
calls four and streams twenty (at most 128 frames, 1 MiB). A disconnect
cancels reads; admitted mutations continue.

```sh
go run ./examples/gateway/framed-client --address 127.0.0.1:6683 \
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

## FIFO pair

`protocol: "fifo"`, `address` an owner-only (0700) directory holding
`input.fifo` and `output.fifo` (both 0600, owned by the gateway user), and
an explicit `operations` list. Run `airc-gateway serve-fifo --file CONFIG`.
The producer opens `output.fifo` for reading before starting the child,
then writes length-prefixed frames to `input.fifo`; one producer per pair.
EOF and failures behave as for the parent-owned streams.

## UART

`protocol: "uart"`, `address` a canonical absolute tty path, an explicit
`operations` list; run `airc-gateway serve-uart --file CONFIG`. The line is
fixed at 115200 baud 8N1, raw, without flow control. The gateway checks the
character device, takes an advisory `flock`, requests tty exclusivity and
restores the original settings on exit. TLS is optional: without
certificates the line carries length-prefixed plaintext frames; with them
the peer negotiates TLS 1.3 (ALPN `airc-gateway/1`) first. Writes have ten
seconds, enough for 64 KiB at this rate. It cannot run beside an enabled
broker or bridge. Exercised over a PTY, never a physical line.

## Queues, shared memory and mapped files

Each kernel message or slot holds one
[fragment](network.md#datagram-fragments) (1–1000 bytes); the gateway
reassembles requests and writes response fragments to the listener's
`reply_to`, one owner-provisioned destination of the same kind that a
request cannot change. A listener may set a
[`bound_author`](authentication.md#bound-authors) for requests that name
none. A dequeued or copied fragment is not yet durable: producers keep the
request until they read a result, and retry it with the same `request_id`.

| `protocol` | Platforms | `address` and `reply_to` | Notes |
|---|---|---|---|
| `posix_mqueue` | Linux AMD64, ARM64 | `/name` (2–255 bytes) | `posix_queue.depth` 1–10. The gateway creates its ingress queue exclusively (mode 0600) and removes only that queue; the reply queue is provisioned by the owner. |
| `system_v_queue` | Darwin ARM64, Linux AMD64, ARM64 | decimal key 1–2147483647 | Messages of type 1. Ingress is created exclusively and recorded in the journal; an unknown queue under the same key is refused, never adopted. |
| `shared_memory` | Darwin ARM64, Linux ARM64 | `/name` (≤ 31 bytes) | `shared_memory.lock_directory` holds `<name>/transaction.lock` per object. Objects are pre-provisioned, 16384 bytes, mode 0600; the gateway never creates or removes them. |
| `mapped_file` | Darwin ARM64, Linux ARM64 | absolute 0700 directories holding `queue.dat` (16384 bytes) and `transaction.lock` | Pre-provisioned, mode 0600; never created, truncated or removed by the gateway. |

Shared-memory objects and `queue.dat` share one slot layout, little-endian,
under the transaction lock:

| Offset | Bytes | Content |
|---|---|---|
| 0 | 4 | atomic state: 0 empty, 1 incomplete, 2 ready |
| 4 | 4 | magic `AGQ1`, written by the provisioner |
| 8 | 4 | fragment length, 1–1000 when ready |
| 12 | 4 | zero |
| 16 | 1000 | fragment |

A writer waits for state 0, sets 1, writes length and fragment, then sets 2;
the reader copies a ready record, sets 1, clears the length and sets 0. A
later owner discards an incomplete slot left by a dead process; a ready
slot copied but not emptied can be read twice, which journal replay absorbs.

## Mach IPC, XPC and D-Bus

`protocol: "mach_ipc"` (Darwin ARM64) registers `address` as a name in the
process's bootstrap namespace. The caller's effective UID comes from the
kernel audit trailer; only the gateway's own UID may call unless
`mach_ipc.callers` lists others (`[{"uid": 501}]`, at most 1024). A request
is a simple Mach message: the 24-byte header, a little-endian `uint32`
length, the frame JSON and zero padding to four bytes. Request message ID
`0x41495201` with a send-once reply right; reply ID `0x41495202`. Requests are
at most 16 KiB, replies 64 KiB; `stream: true` is refused because a
send-once right carries one reply. Unknown callers and malformed messages
get no reply.

Two Darwin bridges forward frames unchanged to a `mach_ipc` listener, which
still checks the caller UID:

- `scripts/build-xpc-bridge.sh --output App.app --mach-service NAME` builds
  an application bundle with an embedded XPC service and a `client` that
  reads one frame on stdin and prints the response.
- `scripts/build-dbus-bridge.sh --output DIR` builds `dbus-server` and
  `dbus-client` (GLib/GIO) for D-Bus peer-to-peer connections over an
  owner-only Unix socket; no bus daemon is involved.

Native sources are in [`native/mach`](../../native/mach),
[`native/xpc`](../../native/xpc) and [`native/dbus`](../../native/dbus).
None of these has been run on Darwin with author frames yet.
