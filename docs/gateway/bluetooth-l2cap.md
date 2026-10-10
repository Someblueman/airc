# Bluetooth L2CAP

The Linux `bluetooth_l2cap` listener selects direct BR/EDR L2CAP stream
sockets with enhanced retransmission mode (ERTM). The existing
[framed protocol](framed-stream-v1.md) runs over verified TLS 1.3 on the
connection, supporting signed Send requests and bounded reads. This mode
does not use RFCOMM, LE credit-based channels, ATT or BLE GATT. Device and
native AIRC verification are deferred.

## Configuration and peer provisioning

Add a listener with actual provisioned controller and peer addresses:

```json
{
  "id": "l2cap-link",
  "protocol": "bluetooth_l2cap",
  "address": "00:11:22:33:44:55/4097",
  "enabled": true,
  "l2cap": {"allowed_peer_addresses": ["aa:bb:cc:dd:ee:ff"]},
  "tls_cert_file": "/absolute/private/server-cert.pem",
  "tls_key_file": "/absolute/private/server-key.pem",
  "operations": ["send", "receipt.get", "history.read", "watch"]
}
```

Run the ordinary `airc-gateway serve --file CONFIG`. The explicit local
endpoint uses a lowercase, six-byte Bluetooth address and a decimal dynamic
PSM (protocol/service multiplexer). PSMs range from 4097 through 65535 and
must satisfy `(psm & 0x0101) == 1`. All-zero/all-FF addresses, noncanonical
spellings, wildcard addresses, static assigned PSMs and dynamic PSM zero
are rejected. The peer list contains 1–64 distinct nonlocal addresses.

Provision authenticated pairing capable of satisfying `BT_SECURITY_HIGH`,
an enabled BR/EDR controller and ERTM support on both ends. The listener
requests high security before accepting connections. Each accepted socket
must report at least high security and negotiated ERTM; lower security or
a downgrade to basic mode is refused. Local and peer address types must
remain BR/EDR, and their PSM and dynamic channel IDs are checked. Only
allowlisted peers reach TLS processing. Link admission does not select an
AIRC identity: the native signed principal and its current grants do that.

Clients bind their provisioned local address, select the server's explicit
PSM and open a Linux L2CAP `SOCK_STREAM` socket in ERTM mode. They verify
the negotiated mode/security and split writes at their negotiated outgoing
MTU, then perform TLS with certificate-chain and server-name verification
and ALPN `airc-gateway/1`. The configured certificate name is independent
of the Bluetooth address. Length-prefixed frames and signed or encrypted
signed request envelopes then follow the ordinary framed profile. The
gateway does not register SDP, discover PSMs, configure controllers, pair
devices or change system privileges.

Enabled profiles require TLS certificate/key files, ingress recipient keys
and signing keys for every configured principal. Responses return on the
same TLS connection. Separate return routes, anonymous requests and browser
origins are refused. Cursor reads and watches require their ordinary keys
and permissions. Stable durable Send IDs, replay handling and native AIRC
identity rules are unchanged.

## Stream handling and ownership

Linux stream reads preserve a partially consumed L2CAP SDU. Stream writes
are nevertheless limited to the negotiated outgoing MTU; the adapter
serializes writes and splits them into bounded SDUs before passing them to
the kernel. ERTM supplies lower-level segmentation and retransmission. The
accepted socket's mode and MTU are checked instead of assuming its requested
settings survived negotiation. These semantics come from the
[Linux L2CAP socket implementation](https://github.com/torvalds/linux/blob/master/net/bluetooth/l2cap_sock.c)
and [channel implementation](https://github.com/torvalds/linux/blob/master/net/bluetooth/l2cap_core.c).

The adapter normalizes the pinned x/sys library's asymmetric address
conversion: bind input is in display order, while returned addresses and
PSM/CID fields reflect kernel byte order. It uses nonblocking close-on-exec
descriptors with deadline and close interruption, a bounded backlog and
the existing service connection/request limits. Framed handshake, idle,
partial-request, dispatch and write deadlines remain active. Frames remain
bounded to 16 KiB requests and 64 KiB responses. Cancellation closes and
joins the existing service workers. There is no journal or wire-envelope
migration.

## Deferred verification

Real peer/controller negotiation, high-security refusal, ERTM downgrade
refusal, MTU chunking, TLS trust, native Send/read/replay, radio interruption
and joined shutdown still require a physical Linux fixture. No tests,
Bluetooth probes or normal/race acceptance are run for this implementation.
Production `go build ./...` passed for Darwin ARM64 and Linux AMD64/ARM64
with `CGO_ENABLED=0`; formatting, whitespace and source-size checks passed.
Compilation alone does not qualify L2CAP or BLE GATT.
