# ICMPv6 message encapsulation

This Darwin ARM64 profile carries plain author requests in the
[datagram fragments](author-network-carriers.md#fragments) of the
[UDP packet protocol](udp.md) inside ICMPv6 Echo Requests. Both application
requests and application responses use Echo Requests. The receiver
discards kernel-generated Echo Replies; a ping response never establishes
durable intake or AIRC delivery.

Native AIRC acceptance and full normal/race/static repository checks pass on
Darwin ARM64. The qualified profile uses loopback paired endpoints; remote
peers and other platforms require their own runtime evidence.

## Configuration and paired endpoints

Use the ordinary `airc-gateway serve --file CONFIG` command with a listener:

```json
{
  "id": "icmp",
  "protocol": "icmp_v6",
  "address": "[::1]:23917",
  "enabled": true,
  "icmp_v6": {"interface": "lo0"},
  "operations": ["send", "receipt.get", "history.read", "watch"]
}
```

The address is a canonical IPv6 literal followed by a channel from 1 to 65535.
The channel occupies an ICMP Echo field, not a UDP port. Zero, names,
noncanonical spellings, mapped IPv4, unspecified, multicast and link-local
addresses, and address zones are refused. The explicit interface must be up
and own the listener's exact local address. This initial profile excludes
link-local addressing and other platforms.

Each request names its author; replies go to the request's source address and
the channel in its Echo Sequence, within the [amplification
bound](author-network-carriers.md). History and watch require the ordinary
cursor key. TLS and browser origins do not apply; the listener access policy
does.

Channels provide cooperative demultiplexing, not exclusive kernel port binds.
The owner must assign distinct receiving channels to independent endpoints
sharing a local address. The configuration refuses duplicate enabled endpoints;
it cannot reserve a channel against a separate process.

## Wire and resource boundaries

| ICMPv6 field | Profile value |
| --- | --- |
| Type and code | Echo Request, 128 and 0 |
| Echo Identifier | Destination channel |
| Echo Sequence | Sender's configured local channel |
| Echo data | One fragment, at most 1000 bytes |

A client sends to the gateway channel with its own channel in Echo Sequence.
The gateway sends application responses to the request's source and channel,
with its own listener channel in Echo Sequence. Request IDs, fragment
identity and whole-page reconstruction remain the existing packet
protocol; Echo Sequence is not an application retry counter.

The receiver requires type 128/code 0, the exact local destination channel,
nonzero source channel, and matching destination/interface ancillary data.
Other ICMP types, automatic Echo Replies, malformed and oversized frames are
discarded before fragment admission. The sender pins its configured local
source and interface. Only IPv6 unicast sources receive replies.

The gateway admits only Echo Requests through a kernel ICMP filter and sets a
256 KiB receive queue, checking the actual socket option before binding. Native
clients need adequate receive capacity for a whole response burst;
the acceptance client also uses a 256 KiB queue. These are per-socket settings,
with no global kernel or permission changes. They reduce avoidable local loss
without making ICMP reliable or guaranteeing capacity under arbitrary traffic.

The kernel may generate an automatic Echo Reply even when the application
refuses the payload. Only an application response carries gateway status.
Loss, reordering and duplicated packets remain possible; retain the original
request and request ID for retries. Bounded reassembly, source admission, complete
cursor pages, durable replay and uncertain native outcomes follow [udp.md](udp.md).

The 1000-byte fragment cap bounds application packet handling. It is not a
kernel memory quota or a guarantee about path fragmentation. Closing the owned
socket interrupts reads; the existing packet service joins request and delivery
workers before releasing journal ownership.

## Socket feasibility evidence

Dated records follow. Where they mention principals, bindings, signed or
encrypted envelopes, signing keys or return routes, they describe the
[removed identity model](removed-identity.md), which no longer exists.

On 2026-10-09, two Darwin ARM64 unprivileged ICMPv6 datagram sockets bound to
`::1` exchanged real Echo Requests in both directions. Each observed request
preserved its identifier, sequence and exact payload, with destination `::1`
and interface `lo0`. Separate type-129 kernel replies were also observed.
An application attempt to send an Echo Reply was refused. Explicit source and
interface control data on writes also worked. The bounded native probe, output
and source/binary hashes are retained in `/tmp/airc-icmpv6-probe/`.

The probe uses the installed [Go ICMP endpoint API](https://pkg.go.dev/golang.org/x/net@v0.58.0/icmp#ListenPacket).
The gateway owns the same kind of native datagram socket directly so it can
set and verify receive capacity before binding.
Apple's [ICMPv6 implementation](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/netinet6/icmp6.c)
copies an Echo Request for reflection while retaining the original for local
delivery, and restricts unprivileged sends to selected request types. IPv4's
different receive behavior does not qualify this mode or vice versa.

The probe used no permission changes or traffic beyond loopback. No Linux,
remote peer or carrier-path qualification follows from these socket results.

A separate queued-burst experiment used sixteen distinct 1000-byte payloads
before draining the receiver. All three trials with the native 8192-byte receive
buffer obtained only seven payloads; all three trials with a 262144-byte buffer
obtained all sixteen within 700 ms. Exact identifiers, sequences, bytes,
destination and interface were checked. Source and results are retained in
`/tmp/icmp6_buffer_ab.go` and `/tmp/icmp6_buffer_ab.log`. This establishes the
bounded queue-capacity difference, not a general network or performance claim.

## Native qualification

On 2026-10-09, two native integration cases passed under the race detector in
12.058 seconds after final client review. They use an independent unprivileged
ICMPv6 client, the actual gateway process, a pinned native AIRC server and a
separate native observer. The gateway binary SHA-256 was
`ddd2b88ee969dc135b5a78905de328cfe5a389d06e7d34c481bbd0dd180ad367`.

The client submits six encrypted signed sends and checks accepted receipts with
one send attempt. Independent native history and live events verify exact author,
room and Unicode/large bodies. A discarded first receipt followed by the same
signed intent and a fresh transport correlation produces one native post.
Protected whole history exceeds 16 KiB and equals the complete native CHECK
result. Limit-one reads advance through every message and the empty caught-up
cursor page. The client observes and ignores real kernel Echo Replies.

Refusals cover wrong realm, tampered encryption, expired and future validity,
signed-only requests, wrong destination channel, an oversized assembly and an
incomplete fragment set. These produce no application response, journal job or
native message. Retrying the exact retained incomplete envelope with a fresh
fragment-delivery ID then produces one accepted native post. Both cases stop
the gateway with SIGINT and require successful joined exit.

Socket tests cover 1000-byte payloads in both directions, every fragment of a
64 KiB response, a complete queued maximum request, destination/interface and
source-channel checks, oversized valid-prefix refusal and close interrupting a
blocked read. Startup failures do not claim readiness or retain the journal.
Configuration tests reject malformed endpoints, missing credentials, unsafe
routes and unsupported enabled platforms. Linux ARM64 compilation verifies the
unsupported-platform path only.

Review found and corrected two framing errors before qualification: the receive
buffer must include the eight-byte ICMP header, and the socket must not apply
the 16 KiB request-only AGD1 parser to responses allowed up to 64 KiB. Native
large-message acceptance also exposed fragment loss at the default 8 KiB ICMP
receive queue; the controlled experiment above supports the explicit 256 KiB
queue at both endpoints. The native client sends each fragment burst without
pacing; receipt polling follows ordinary durable-queue behavior.

This evidence covers Darwin ARM64 loopback paired endpoints. It does not cover
Linux runtime, remote peers, physical network carriers, process-restart replay
for this mode or a separate native streaming-watch exercise. Shared journal
and packet-watch behavior is unchanged. The full checkpoint passed all 49
ICMPv6 test/subtest cases in both normal and race suites, with no failures or
race/sanitizer diagnostics. Exact commands, source hashes and terminal results
are in [testing.md](testing.md#icmpv6-acceptance-checkpoint).
