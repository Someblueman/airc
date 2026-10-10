# Raw IPv4 packet ingress

The Linux `raw_ip` listener carries the existing encrypted [AGD1 packet
protocol](udp.md) over IPv4 protocol 253. It uses a fixed application channel
at each endpoint and owner-provisioned encrypted return routes. Implementation
and compilation do not establish runtime qualification; testing is deferred.

## Configuration

Use `airc-gateway serve --file CONFIG` with a listener such as:

```json
{
  "id": "raw",
  "protocol": "raw_ip",
  "address": "127.0.0.1:23917",
  "enabled": true,
  "raw_ip": {"interface": "lo"},
  "operations": ["send", "receipt.get", "history.read", "watch"]
}
```

The endpoint is a canonical IPv4 literal and a channel from 1 to 65535, not a
UDP port. The explicit interface must be up and own that exact address.
Unspecified, multicast, limited broadcast, IPv6, names and noncanonical
endpoints are rejected. The socket also rejects known network and directed
broadcast addresses on the selected interface, except point-to-point /31 and
/32 addresses. Remote subnet boundaries cannot be inferred from an address
alone; provision only authorized unicast peers.

Configure ingress recipient keys, signing principals, a response signing key,
and exactly one fixed return route for every principal:

```json
{
  "id": "raw-return",
  "principal": "sender",
  "protocol": "raw_ip",
  "listener": "raw",
  "destination": "127.0.0.1:23918",
  "recipient_key_id": "reader",
  "recipient_public_key_file": "/absolute/private/reader-public.pem"
}
```

The usual binding and target grants determine AIRC identity and room access.
Reads require the ordinary cursor key. TLS, browser origins and anonymous
access are not supported. Return routes cannot target the listener's own
address and channel. The observed packet source never grants authority or
selects a response destination.

Creating the socket requires Linux raw-socket permission, typically
`CAP_NET_RAW`. The gateway does not change capabilities, interfaces, routes,
firewalls or global kernel settings. Unsupported platforms fail closed.
Channels provide cooperative demultiplexing; they do not reserve exclusive
kernel ports against other processes using protocol 253.

## Wire and resource boundaries

Each IPv4 protocol-253 payload contains:

| Offset | Field |
| --- | --- |
| 0–3 | ASCII `AGI1` |
| 4–5 | Destination channel, big-endian uint16 |
| 6–7 | Sender channel, big-endian uint16 |
| 8 onward | One opaque AGD1 datagram, 1–1000 bytes |

Requests and encrypted responses use the same outer framing. The socket
does not apply request-only AGD1 limits to response fragments. Signed intent,
original freshness, durable replay, bounded reassembly, complete response
pages and uncertainty follow the existing packet dispatcher.

Intake checks the IPv4 version, protocol, complete declared length, ordinary
20-byte header, source, exact destination, interface metadata and destination
channel before exposing the AGD1 payload. Malformed, oversized and mismatched
frames are discarded. Linux may reassemble IP fragments before delivery to
the raw socket; these checks do not impose a kernel reassembly quota.

The receive queue is set and checked at 256 KiB. A receive buffer large enough
for the maximum IPv4 datagram avoids accepting a truncated valid prefix.
Writes pin the configured source address and interface, use an ordinary IPv4
header and fixed route, and propagate socket errors. The 1000-byte fragment
cap fits a typical 1500-byte MTU, but does not guarantee compatibility with
smaller paths. There is no automatic response to unauthenticated traffic.
Closing the socket interrupts reads; the existing service owns and joins
packet workers and durable delivery.

## Deferred verification

The production tree compiles with `go build ./...` on Darwin ARM64 and with
`GOOS=linux GOARCH=amd64 CGO_ENABLED=0` and
`GOOS=linux GOARCH=arm64 CGO_ENABLED=0`. Formatting, whitespace and the
advisory source-size check pass. Review covered the socket adapter,
configuration, shared packet routing and service ownership.

No raw-IP unit tests, runtime socket probes, native AIRC acceptance, race
suite or full repository gate were run for this implementation. Linux AMD64
and ARM64 runtime behavior, real paired clients, interface and capability
refusals, loss/replay, complete protected history/watch responses and joined
shutdown remain on the roadmap. Existing ICMPv6 or UDP evidence does not
qualify this mode.
