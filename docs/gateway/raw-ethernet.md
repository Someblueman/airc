# Raw Ethernet ingress

The Linux `raw_ethernet` listener carries encrypted [AGD1 packets](udp.md)
over experimental EtherType `0x88B5`. It uses `AF_PACKET/SOCK_DGRAM` on one
explicit, up Ethernet interface, without promiscuous mode. Implementation
is available; runtime and native AIRC testing are deferred.

## Configuration

Use `airc-gateway serve --file CONFIG` with a listener such as:

```json
{
  "id": "ethernet",
  "protocol": "raw_ethernet",
  "address": "02:00:00:00:00:01/23917",
  "enabled": true,
  "raw_ethernet": {"interface": "eth0"},
  "operations": ["send", "receipt.get", "history.read", "watch"]
}
```

Replace the example MAC with the interface's assigned six-byte unicast MAC.
Addresses use canonical lowercase colon-separated MACs, a slash and a decimal
channel from 1 to 65535. Zero, broadcast, multicast and noncanonical addresses
are refused. Channels provide cooperative demultiplexing, not exclusive ports.

Provision ingress recipient keys, signing principals, a response signing key
and exactly one protected `raw_ethernet` return route per principal. Each route
names this listener, a fixed peer endpoint such as `02:00:00:00:00:02/23918`,
and the usual `recipient_key_id` and absolute `recipient_public_key_file`.
The route cannot target the listener's own MAC and channel. The ordinary
binding, target grants and cursor key govern AIRC access and bounded reads.
TLS, origins and anonymous access are refused. An observed source MAC or
channel never supplies identity or redirects replies.

Linux packet-socket permission, typically `CAP_NET_RAW`, is required. The
gateway does not change privileges, interface configuration or global kernel
settings. Other platforms fail closed; this is not a Darwin BPF adapter.

## Framing and ownership

| Payload offset | Field |
| --- | --- |
| 0–3 | ASCII `AGE1` |
| 4–5 | Destination channel, big-endian uint16 |
| 6–7 | Sender channel, big-endian uint16 |
| 8–9 | AGD1 length, big-endian uint16, 1–1000 |
| 10 onward | Exactly that many opaque AGD1 bytes |

Both application directions use this framing. Short writes are zero-padded
to the Ethernet minimum payload of 46 bytes. Intake accepts the exact logical
length or exactly 46 bytes with zero-only padding when necessary. Other
trailing bytes, oversized/truncated frames and invalid channels are refused.
Request-only AGD1 limits are not applied to protected response fragments.

Receive metadata must identify the exact interface and EtherType, Ethernet
hardware type, six-byte unicast source and a packet addressed to this host.
Outgoing copies, broadcast, multicast and other-host frames are discarded.
Responses name only the fixed unicast route and selected interface. The socket
owns a checked 256 KiB receive queue and a bounded frame buffer; its pollable
descriptor implements deadlines and close interruption. The packet service
owns and joins request and durable-delivery workers.

Encrypted authentication, freshness, stable request IDs, bounded assembly,
durable replay and whole response pages follow the existing packet dispatcher.
There is no unauthenticated success reply and no reliability guarantee from
Ethernet framing. This profile does not implement discovery, general Ethernet
bridging or arbitrary EtherTypes.

## Deferred verification

Production builds pass on Darwin ARM64 and Linux AMD64/ARM64 with
`go build ./...` (Linux cross-builds use `CGO_ENABLED=0`). Formatting,
whitespace and advisory source-size checks pass. Review covered framing,
byte order, interrupted syscalls, descriptor ownership and service routing.

Native packet reception, padding and metadata behavior, permission refusals,
independent AIRC send observation, complete protected cursor/history/watch
replies, replay and joined shutdown remain unverified. No tests, packet-socket
probes or normal/race acceptance were run for this implementation.
