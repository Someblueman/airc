# CAN-FD through Linux SocketCAN ISO-TP

The `canfd_isotp` listener selects Linux SocketCAN ISO-TP over CAN-FD with
normal 11-bit addressing, at most 64 data bytes per transmitted CAN frame,
and no transmit bit-rate switching. This is the concrete CAN-FD implementation.
It carries the same encrypted, signed [AGD1 requests and whole protected
responses](can-isotp.md) as the Classical CAN listener. Device and native AIRC
testing remain deferred.

The [Linux link-layer options](https://docs.kernel.org/networking/iso15765-2.html#link-layer-options)
define the generated and accepted frame type separately from the maximum
transmit data length. The gateway sets and reads back exactly
`CAN_ISOTP_LL_OPTS = {CANFD_MTU=72, tx_dl=64, tx_flags=0}`. The kernel drops
Classical CAN frames before ISO-TP receive dispatch. Received CAN-FD frames
may use other valid data lengths; 64 is the transmit maximum, not a requirement
that every received frame contain 64 bytes. Transmit BRS is disabled; this
does not filter received BRS flags. Provision compatible nominal/data bitrates
and an FD-capable peer for the frames it sends.

## Configuration

Provision an up Linux CAN-FD interface with MTU 72, ISO-TP kernel support,
compatible physical bus wiring and termination. The gateway fixes the
interface index, checks its CAN device type and refuses a Classical-only
interface. It does not configure interface bitrate or controller mode.

```json
{
  "id": "canfd-link",
  "protocol": "canfd_isotp",
  "address": "can0/710/718",
  "enabled": true,
  "operations": ["send", "context.read", "history.read", "search"]
}
```

The endpoint grammar and native identity requirements are the same as
[Classical CAN](can-isotp.md#configuration-and-provisioning). Configure ingress
recipient keys, response signing and a protected reverse route for every
configured signing principal:

```json
{
  "id": "canfd-return",
  "principal": "sensor",
  "protocol": "canfd_isotp",
  "listener": "canfd-link",
  "destination": "can0/718/710",
  "recipient_key_id": "sensor-result",
  "recipient_public_key_file": "/absolute/keys/sensor-result.pem"
}
```

Run `airc-gateway serve --file CONFIG`. The route protocol must match its
listener. RX/TX ID pairs must not overlap on an interface across any enabled
Classical CAN or CAN-FD listener; reserve the pair across other processes and
devices too. The remote peer transmits on `710` and receives on `718` here,
using CAN-FD ISO-TP normal addressing. CAN identifiers grant no AIRC authority.
TLS fields, browser origins, anonymous access and watch operations are refused.

## Delivery and bounds

Each ISO-TP PDU contains exactly one AGD1 datagram of 1–1000 bytes. The shared
limits remain 16 KiB per encrypted request, four incomplete assemblies per
source, five-second assembly expiry, and 64 KiB per whole encrypted response
(32 KiB decrypted frame). Fixed recipient keys protect complete responses
before fragmentation. A CAN acknowledgement or a successful local write does
not establish native AIRC acceptance; verify the complete protected result.

The listener uses the same supervised socket worker, bounded private IPC,
timeouts, flow-control settings and kill/reap shutdown as the Classical CAN
profile. The selected frame type survives worker startup and both directions
of packet addressing; there is no automatic fallback to Classical CAN. Source
and request capacity is shared with other gateway listeners. Large messages,
bus load and peer separation delays can exceed response deadlines; use bounded
pages and retain the ordinary uncertain-outcome retry policy.

## Deferred verification

Production `go build ./...` passed with `CGO_ENABLED=0` for Darwin ARM64,
Linux AMD64 and Linux ARM64. Formatting, whitespace and the advisory source
size check passed.

Source review covers the FD option selection, strict interface MTU, exact
option readback, worker mode propagation, fixed reverse routes and ID conflicts
across both profiles. No CAN-FD device, virtual bus, runtime probe or native
AIRC acceptance was run.

Later acceptance must exercise real FD endpoints with an independent AIRC
observer, signed Send and whole bounded reads, replay, invalid signatures,
classical-frame rejection, variable legal FD frame sizes, oversized and
malformed PDUs, interface changes, bus/flow-control loss, worker failure and
joined shutdown. Classical CAN verification does not qualify this mode.
