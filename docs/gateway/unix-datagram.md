# Unix datagram gateway profile

A `unixgram` listener carries plain contract requests in datagram fragments
(the [shared fragment format](author-network-carriers.md)) on a Unix datagram
socket, distinct from the `unix` stream listener. Each request names its
author, or none with the listener's `bound_author`; the response frame is sent
in fragments to the sender's own bound address, and an unbound sender gets no
reply. The owner-only socket is the access gate. See
[local authors](local-authors.md).

```json
{
  "id": "local-packets",
  "protocol": "unixgram",
  "enabled": true,
  "address": "/private/tmp/gateway-private/ingress"
}
```

Use a private owner-controlled parent directory; the gateway binds the ingress
socket with mode `0600`. Unsafe parents, symlinks, existing ingress paths,
mismatched owners or modes and overlong paths are rejected. Ancestors must be
owned by the gateway user or root and deny untrusted writes, except root-owned
sticky directories. Darwin's `/tmp` alias is handled explicitly. Paths are
limited to 103 bytes on Darwin and 107 on Linux. Cleanup removes only the
gateway's own ingress inode. These checks do not isolate mutually hostile
processes sharing one OS account.

Each packet is at most 1000 bytes; larger or truncated input is discarded.
Requests keep the 16 KiB frame limit and results 64 KiB. Source leases, four
partial assemblies per source, five-second assembly and idle expiry, global
admission slots and workers stay bounded; unnamed sources share one bucket.
All fragments of a response share one three-second write deadline and a
serialized writer. Unix sockets can report zero-byte `ENOBUFS` instead of
waiting; such definite failures retry with a one-millisecond pause inside the
deadline, while partial writes are not retried. Watches keep the bounded
stream lease and output budget. For uncertain delivery, retry the same request
ID with a fresh correlation and fragment ID, then query its receipt.

## Acceptance

```sh
go test -race -count=1 ./integration/gateway -run '^(TestUnixgram|TestLocalAuthorsOverUnixDatagram)'
```

Against a pinned AIRC fixture, the tests call the shared operations, compare
whole history and live records independently, verify quiet heartbeats and
lease expiry, join active watches and partial input on SIGINT, replay a lost
response from a new source socket with one native record and one send
attempt, and drop oversized, malformed and incomplete input. A socket pressure
test covers termination at the write deadline; service tests cover the private
ingress lifecycle. [Linux native acceptance](linux-unixgram-testing.md) is
recorded separately. Unixgram is unavailable on unsupported systems.
