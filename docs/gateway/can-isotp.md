# Classical CAN through Linux SocketCAN ISO-TP

The `can_isotp` listener uses Linux's `AF_CAN`, `SOCK_DGRAM`, `CAN_ISOTP`
socket API with normal addressing and 11-bit CAN identifiers. Each ISO-TP
payload carries one existing [AGD1 datagram](udp.md); the kernel handles its
segmentation and flow control over Classical CAN frames. The application
reassembles and authenticates complete encrypted, signed native requests and
returns whole encrypted responses. Native CAN and AIRC testing are deferred.

This selects a concrete CAN API without requiring a userspace CAN library.
The kernel documents the [ISO-TP socket contract](https://docs.kernel.org/networking/iso15765-2.html)
and [option layouts](https://github.com/torvalds/linux/blob/master/include/uapi/linux/can/isotp.h).
The separate [CAN-FD profile](canfd-isotp.md) selects FD frames. 29-bit
identifiers, extended/mixed addressing and functional broadcast are outside
these profiles.

## Configuration and provisioning

Provision a Linux SocketCAN interface, compatible ISO-TP kernel support,
bus bitrate and a paired endpoint before starting the gateway. Both endpoints
must agree on the identifiers and framing. The gateway requires an up CAN
interface, fixes its interface index, and explicitly selects Classical CAN
frames with eight-byte link payloads. It does not configure the adapter,
bitrate, bus termination or device permissions.

Within an otherwise provisioned gateway configuration:

```json
{
  "id": "can-link",
  "protocol": "can_isotp",
  "address": "can0/700/708",
  "enabled": true,
  "operations": ["send", "context.read", "history.read", "search"]
}
```

The address is `interface/rx_id/tx_id`: each identifier is exactly three
lowercase hexadecimal digits from `000` through `7ff`, and RX differs from
TX. The interface name is 1–15 ASCII letters, digits, underscores, hyphens
or dots, excluding `.` and `..`. Configure the remote endpoint with reversed
IDs: it transmits on `700` and receives on `708` in this example.

Configure ingress recipient keys, a response signing key and one protected
return route for every configured signing principal:

```json
{
  "id": "can-return",
  "principal": "sensor",
  "protocol": "can_isotp",
  "listener": "can-link",
  "destination": "can0/708/700",
  "recipient_key_id": "sensor-result",
  "recipient_public_key_file": "/absolute/keys/sensor-result.pem"
}
```

Every destination must be the exact reverse of its listener's address.
Its interface component identifies the local gateway adapter used to reach
that peer; the remote host need not use the same interface name. Routes fix
both the signing principal's binding and response recipient key. Native
signed target and operation grants remain authoritative. IDs and flow-control
frames carry no AIRC authority, and physical bus access does not identify
a sender. TLS fields, browser origins and anonymous access are refused.
Only Send, context.read, history.read and search may be configured. Bounded
reads require the usual cursor configuration where applicable.

Run `airc-gateway serve --file CONFIG`. Within one configuration, enabled
Classical CAN and CAN-FD listeners may not reuse either ID on the same interface. Reserve the
pair operationally across processes and devices as well: ISO-TP sockets do
not grant an exclusive bus address. Other nodes using that pair can disrupt
reassembly or flow control; native authentication prevents them from gaining
an AIRC identity. The gateway does not reserve or modify other bus nodes.

## Wire, delivery and resource bounds

An ISO-TP PDU is exactly one AGD1 datagram of at most 1000 bytes, without any
additional CAN-specific application header. A complete encrypted
`FrameRequest` is bounded to 16 KiB. Use fresh AGD1 frame and transport
correlation IDs for retries, preserving the native durable request ID and
original intent. The peer reverses RX/TX IDs, uses normal 11-bit addressing
and Classical CAN with eight-byte data fields, and runs standard ISO-TP flow
control. The gateway advertises receive blocks of eight frames and no forced
separation delay; outbound traffic respects the peer's flow-control timing.

Responses are signed and encrypted as a whole before AGD1 fragmentation.
Reassemble and verify the complete token, signer, recipient, realm,
principal, binding, route, lifetime, correlation and sequence before consuming
a result or advancing a cursor. CAN acknowledgements, flow-control frames
and successful local writes do not establish native AIRC acceptance. A
protected native response establishes the reported outcome; missing or
incomplete responses retain the ordinary uncertain-delivery and retry policy.

The existing packet limits apply: four incomplete assemblies per source,
a five-second assembly lifetime, 16 KiB requests, 64 KiB protected response
tokens and 32 KiB decrypted response frames. Shared source/request limits
bound application work. Writes are serialized with a three-second whole
response deadline including queue time. Slow buses or large peer separation
times may require smaller requests and smaller explicit history/search pages;
no bitrate or throughput qualification is claimed.

The socket has bounded receive/send queues and rejects empty, truncated or
oversized PDUs before application intake. The kernel still owns its ISO-TP
reassembly buffers and timers; the application PDU cap is not a kernel memory
quota. Ordinary transmission queues one PDU at a time without the blocking
`CAN_ISOTP_WAIT_TX_DONE` option. Interface loss, bus errors and flow-control
failure remain visible transport failures.

A supervised child process owns the kernel socket because Linux ISO-TP close
can wait indefinitely while a peer repeats flow-control WAIT frames. The
gateway re-executes its exact binary through `/proc/self/exe` and passes two
private Unix packet sockets. The worker verifies parent credentials and handles
only opaque PDUs; native authentication, durable state and response keys stay
in the gateway. Startup waits at most five seconds for readiness. Shutdown
closes IPC, allows 250 milliseconds for exit, then kills and reaps a remaining
worker before joining application work. Parent death also requests SIGKILL.
A lost write acknowledgement ends that association, avoiding stale replies
being mistaken for a later write. Native worker and IPC queues are each
bounded to 64 KiB per direction. Running this profile requires the normal
`airc-gateway` executable with its internal worker entry point and procfs.

## Deferred verification

Production `go build ./...` passed with `CGO_ENABLED=0` for Darwin ARM64,
Linux AMD64 and Linux ARM64. Formatting, whitespace and the advisory source
size check passed. Source review covered kernel behavior, the socket owner,
private worker IPC, configuration and the shared packet integration.

No CAN adapter, virtual CAN device, ISO-TP runtime probe, native AIRC test,
normal/race suite or full acceptance gate was run for this implementation.
Later acceptance requires compatible paired endpoints and an independent
native AIRC observer: verify author/target/body, whole bounded reads, invalid
signatures, replay, ID/interface/MTU refusals, malformed and oversized PDUs,
flow-control timeout, repeated WAIT frames during shutdown, worker death,
IPC backpressure, bus loss and joined shutdown. Classical CAN evidence
will not qualify the separate CAN-FD mode.
