# Bluetooth RFCOMM

The Linux `bluetooth_rfcomm` listener uses native
`AF_BLUETOOTH/SOCK_STREAM/BTPROTO_RFCOMM` sockets. It carries the existing
[framed protocol](framed-stream-v1.md) over TLS 1.3. This selects the Linux
kernel RFCOMM API, with a provisioned BR/EDR controller and peers, rather than
an RFCOMM TTY or a separate Bluetooth proxy. Implementation is available;
radio, pairing and native AIRC acceptance are deferred.

## Configuration and client

Use `airc-gateway serve --file CONFIG` with this listener shape, replacing
the addresses and paths with the provisioned values:

```json
{
  "id": "bluetooth-link",
  "protocol": "bluetooth_rfcomm",
  "address": "00:11:22:33:44:55/7",
  "enabled": true,
  "rfcomm": {"allowed_peer_addresses": ["aa:bb:cc:dd:ee:ff"]},
  "tls_cert_file": "/absolute/private/server-cert.pem",
  "tls_key_file": "/absolute/private/server-key.pem",
  "operations": ["send", "receipt.get", "history.read", "watch"]
}
```

The local address is a canonical lowercase, six-byte Bluetooth device
address followed by `/` and a decimal channel from 1 through 30. Wildcards,
all-zero/all-FF addresses, dynamic channel zero and leading zeros in the
channel are rejected. The allowlist has 1–64 distinct peer addresses and
cannot contain the local address. Bluetooth addresses do not use Ethernet's
multicast-bit validation. The kernel must report the exact configured local
address and channel; accepted peers must match the allowlist.

Provision pairing capable of satisfying `BT_SECURITY_HIGH` and enable the
controller before starting the gateway. The gateway requests this security
level before listening, checks that the option is retained, and checks its
inheritance on each accepted connection. Failure is fatal or refuses the
connection; there is no lower-security fallback. The getter reports the
configured level, not proof of a negotiated key size. Pairing and link
security enforcement remain kernel/controller responsibilities. The
[Linux RFCOMM socket source](https://github.com/torvalds/linux/blob/master/net/bluetooth/rfcomm/sock.c)
defines the option and accepted-socket behavior.

Clients open an RFCOMM stream to the explicit server address/channel and
then perform TLS 1.3 with certificate-chain and server-name verification,
negotiating ALPN `airc-gateway/1`. The certificate name is configured
independently of the Bluetooth address. They then send the ordinary bounded
length-prefixed request frames. A client must provision the same Bluetooth
security policy; a raw serial terminal is not a framed TLS client. The
gateway does not publish an SDP record, select a channel, pair devices,
configure a controller, create TTYs, or change privileges.

TLS certificates, ingress recipient keys and signing keys for every
configured principal are required. Signed or encrypted signed envelopes
retain the existing principal, binding, target, operation and freshness
checks. Bluetooth addresses restrict admission but never select AIRC
identity. Replies use the same verified TLS connection; separate return
routes, anonymous access and browser origins are refused. History and watch
require the ordinary cursor key and grants.

## Bounds and ownership

The socket has a bounded listen backlog and uses the service's shared
connection and request limits. Disallowed peers are closed before framed
admission. Nonblocking, close-on-exec descriptors use the Go poller and
support read/write deadlines and close interruption. The existing framed
TLS handshake, idle, partial-frame, native-request and write deadlines
apply. Request frames are at most 16 KiB and response frames at most 64 KiB.
No new fragmentation, journal schema, durable identity or replay format is
introduced. Cancellation closes the listener and streams and joins the
existing request and delivery workers.

## Deferred verification

Actual controller binding, pairing refusal, peer filtering, TLS trust and
ALPN refusal, authenticated AIRC sends, bounded cursor reads, stable replay,
radio loss and joined shutdown still need a real Linux device/client
fixture. No Bluetooth probes, tests or full normal/race acceptance are run
for this implementation. Production `go build ./...` passed for Darwin
ARM64 and Linux AMD64/ARM64 with `CGO_ENABLED=0`; formatting, whitespace and
source-size checks passed. A successful build does not qualify the radio
path. L2CAP and BLE GATT remain distinct catalogue modes.
