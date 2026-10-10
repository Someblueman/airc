# IPv4 ICMP message encapsulation

The Linux `icmp_v4` listener carries encrypted [AGD1 packets](udp.md) in
ICMP Echo Requests. Both application requests and protected responses use
Echo Requests. Kernel Echo Replies are discarded; they never establish
authentication, durable admission or delivery to AIRC.

This is an implemented Linux raw-socket profile. Testing is deferred; no
native delivery or runtime qualification is claimed.

## Configuration and paired endpoints

Use `airc-gateway serve --file CONFIG` with a listener such as:

```json
{
  "id": "icmp4",
  "protocol": "icmp_v4",
  "address": "127.0.0.1:23917",
  "enabled": true,
  "icmp_v4": {"interface": "lo"},
  "operations": ["send", "receipt.get", "history.read", "watch"]
}
```

The canonical IPv4 literal must be assigned to the explicit, up interface.
The channel ranges from 1 to 65535 and occupies an ICMP Echo field, not a
UDP port. Names, IPv6, unspecified, multicast, limited broadcast and
noncanonical endpoints are refused. Known network and directed broadcast
addresses on the interface are refused, except /31 and /32 point-to-point
addresses. Only authorized unicast peers should be provisioned; remote
subnet masks cannot be inferred from an address alone.

Configure ingress recipient keys, signing principals, a response signing key
and exactly one protected return route for every principal, for example:

```json
{
  "id": "icmp4-return",
  "principal": "sender",
  "protocol": "icmp_v4",
  "listener": "icmp4",
  "destination": "127.0.0.1:23918",
  "recipient_key_id": "reader",
  "recipient_public_key_file": "/absolute/private/reader-public.pem"
}
```

The fixed route cannot target the listener's own address and channel.
Observed source addresses and channels grant no authority and never redirect
responses. The usual binding and target grants select AIRC identity and
room access; reads require the ordinary cursor key. TLS, browser origins and
anonymous access are refused. Channels provide cooperative demultiplexing,
not exclusive kernel port reservations.

Linux raw-socket permission, typically `CAP_NET_RAW`, is required. The
gateway makes no capability, interface, firewall or global kernel changes.
Other platforms fail closed. Darwin's unprivileged ping sockets and the
qualified Darwin ICMPv6 adapter have different receive semantics and do not
qualify this profile.

## Wire and resource boundaries

| Field | Value |
| --- | --- |
| IPv4 protocol | 1 |
| ICMP type / code | Echo Request, 8 / 0 |
| ICMP checksum | Complete header and data, Internet checksum |
| Echo identifier | Destination channel |
| Echo sequence | Sender's configured channel |
| Echo data | One opaque AGD1 datagram, 1–1000 bytes |

A client sends to the gateway's channel and receives application replies
on its separately provisioned channel. The sequence field is not a retry
counter. Authentication, signed intent, original freshness, bounded
reassembly, durable replay and complete response pages use the existing
packet dispatcher. The socket does not apply request-only AGD1 parsing to
larger protected responses.

Intake requires an ordinary 20-byte IPv4 header, complete declared length,
protocol 1, usable source, exact destination/interface metadata, correct
ICMP checksum, Type 8/Code 0 and matching destination channel. Echo Replies,
other ICMP types, empty or oversized payloads and invalid channels are
discarded. Linux may reassemble fragments before userspace receives them;
the adapter does not impose a kernel reassembly quota.

Raw IPv4 socket ownership is shared with the protocol-253 adapter. The
receive queue is set and checked at 256 KiB, and reads use a buffer large
enough for any IPv4 datagram. Writes calculate the ICMP checksum and pin
the configured source address and interface. Packets too large for the path
MTU fail with the socket error. Close interrupts reads, and the service joins
packet and durable-delivery workers.

The kernel may automatically answer an Echo Request regardless of whether
the gateway admits its data. Only a verified protected application response
carries gateway status. Retain the original signed intent and stable request
ID when retrying uncertain or lost responses.

## Deferred verification

The production tree builds on Darwin ARM64 and Linux AMD64/ARM64 with
`go build ./...` (Linux cross-builds use `CGO_ENABLED=0`). Formatting,
whitespace and advisory source-size checks pass. Review covered the checksum,
socket ownership, configuration, fixed routing and service integration.

Runtime tests must cover actual incoming requests, automatic Echo Replies,
checksum/channel/length/interface refusals, retained replay, independent
native AIRC send observation, whole cursor/history/watch replies and joined
shutdown. No tests, raw-socket probes or normal/race acceptance were run for
this implementation. Prior UDP or ICMPv6 results are not mode evidence.
