# DCCP connected packet ingress

The Linux `dccp` listener carries the encrypted [AGD1 packet protocol](udp.md)
over IPv4 DCCP (IP protocol 33). This implementation uses connected records,
the fixed four-byte service code `AIRC`, and provisioned peer endpoints.
Runtime and native AIRC qualification are deferred. Upstream Linux has
[retired DCCP sockets](https://github.com/torvalds/linux/commit/2a63dd0edf388802074f1d4d6b588a3b4c380688);
this mode requires a kernel retaining `CONFIG_IP_DCCP`.

## Configuration

Use `airc-gateway serve --file CONFIG` with a listener such as:

```json
{
  "id": "dccp-link",
  "protocol": "dccp",
  "address": "127.0.0.1:23917",
  "enabled": true,
  "operations": ["send", "receipt.get", "history.read", "watch"]
}
```

Configure signing principals, ingress recipient keys, a response signing key,
and exactly one protected return route per principal for this listener:

```json
{
  "id": "dccp-return",
  "principal": "sender",
  "protocol": "dccp",
  "listener": "dccp-link",
  "destination": "127.0.0.1:23918",
  "recipient_key_id": "reader",
  "recipient_public_key_file": "/absolute/private/reader-public.pem"
}
```

Both endpoints require canonical numeric IPv4 addresses and ports 1–65535.
Names, wildcards, multicast, limited broadcast and IPv6 are refused. Provision
unicast peers; remote subnet boundaries cannot be inferred from an IP alone.
Clients must bind their provisioned return IP and port before connecting,
and configure the `AIRC` service code. A NAT path must preserve the configured
peer endpoint as seen by the gateway. A route cannot point to the listener.

The peer endpoint is an admission filter, not an AIRC identity. Each accepted
connection can use only the principals whose configured routes match that
peer. The gateway still authenticates and decrypts every request envelope,
checks original freshness and grants, and encrypts replies to the configured
recipient key. Observed addresses never create a response route. Bounded
reads require the ordinary cursor key. TLS, origins and anonymous access are
unsupported for this packet profile.

## Wire and ownership

Each DCCP data record is exactly one opaque AGD1 fragment of 1–1000 bytes;
there is no additional stream length prefix. The socket refuses truncated
or oversized records and treats zero-length data as connection termination.
Reassembly, signed intent, stable IDs, durable replay, complete history pages
and bounded watches use the existing packet dispatcher. Transport delivery
is unreliable: a successful socket write is not AIRC acceptance. Retry the
same signed intent and request ID after loss or uncertainty.

The Linux service-code socket option receives the exact network-order bytes
`AIRC`. Before each write, the current DCCP maximum packet size must cover
the entire record. Smaller paths or socket errors fail the write; the adapter
does not silently truncate or switch transports. This profile relies on the
kernel's DCCP congestion control and does not change global kernel settings.
Socket-option semantics follow the [Linux DCCP documentation](https://docs.kernel.org/6.14/networking/dccp.html).

The listener has a bounded backlog, a 64-record transmit queue and requested
128 KiB send/receive buffers (subject to Linux clamping and accounting).
Full transport checksum coverage is required in both directions.
Accepted connections and fragment
sources share the service session limit, while dispatch uses the separate
shared request limit. Each connection owns an independent assembler and
write serialization. A peer must complete an authenticated request within
five seconds, then remain active within a 30-second idle limit. Only fully
authenticated requests with an authorized route extend that idle limit.
Malformed or incomplete traffic cannot retain a session slot. Only authenticated
requests can cause application replies.

Nonblocking close-on-exec descriptors use the Go poller and deadlines.
Connection errors remain local to that connection. Service cancellation
closes the listener and connected sockets and joins all request workers.
Unsupported platforms and kernels without DCCP support fail closed.

## Deferred verification

Production `go build ./...` passes on Darwin ARM64 and Linux AMD64/ARM64
(Linux cross-builds use `CGO_ENABLED=0`). Formatting, whitespace and advisory
source-size checks pass. Review covered kernel record semantics, peer and
route restrictions, authenticated idle renewal and shutdown ownership.

The implementation needs real compatible client/server endpoints, native
AIRC send/read/replay, complete protected pages, service-code and peer
refusals, packet loss, size/path-MTU boundaries and joined shutdown checks.
No tests, runtime DCCP probes or normal/race acceptance were run for this
implementation. Prior native probing found no DCCP support in the available
Linux ARM64 container kernel; a compatible fixture is still needed for
qualification. Production compilation does not supply that evidence.
