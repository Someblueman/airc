# Virtio-vsock host/guest connections

The Linux `virtio_vsock` listener carries the existing [framed protocol](framed-stream-v1.md)
over TLS 1.3 on `AF_VSOCK/SOCK_STREAM`. Explicit context IDs (CIDs) restrict
the transport peers. Signed gateway principals still select the AIRC identity;
a CID is not a user credential. Implementation is available, with testing deferred.

## Configuration

Use `airc-gateway serve --file CONFIG` with a listener such as:

```json
{
  "id": "guest-link",
  "protocol": "virtio_vsock",
  "address": "2:23917",
  "enabled": true,
  "virtio_vsock": {"allowed_peer_cids": [3]},
  "tls_cert_file": "/absolute/private/server-cert.pem",
  "tls_key_file": "/absolute/private/server-key.pem",
  "operations": ["send", "receipt.get", "history.read", "watch"]
}
```

Select the actual local and peer CIDs for the host/guest pair. CID 2 names
the host; guest CIDs are assigned by the virtualization environment. This
profile accepts explicit CIDs 2 through 4294967294 and ports 1024 through
4294967294. Wildcards, hypervisor/local-loopback CIDs, leading zeros and
reserved ports are refused. The kernel must confirm the exact bound address.
The peer list has 1–64 unique CIDs and cannot include the local CID.

TLS certificate/key files, signing principals and ingress recipient keys
are required. Clients must verify the certificate chain and configured server
name and negotiate ALPN `airc-gateway/1`; the certificate name is independent
of the numeric CID. Signed and encrypted request envelopes use the ordinary
principal, binding, target and freshness checks. Responses return on the
same TLS connection, so separate return-route entries are refused. History
and watch require the ordinary cursor key. Anonymous access and browser
origins are unsupported.

## Ownership and protocol

The socket uses a bounded listen backlog and the service's existing shared
connection and request limits. Rejected peer CIDs are closed before framed
admission. Accepted connections expose actual local and remote CID/port
metadata and use pollable nonblocking descriptors with close-on-exec,
deadlines and close interruption. TLS handshakes and framed requests retain
their existing deadlines and size limits: requests up to 16 KiB and complete
response frames up to 64 KiB.

The framed dispatcher supplies durable sends, stable request IDs, protected
cursor/history reads and bounded watches. Service cancellation closes the
listener and accepted streams and joins request/delivery workers. The gateway
does not create VMs, assign CIDs, configure hypervisors or change privileges.
Unsupported platforms and unavailable kernel transports fail closed.

## Deferred verification

Production `go build ./...` passes on Darwin ARM64 and Linux AMD64/ARM64
(Linux cross-builds use `CGO_ENABLED=0`). Formatting, whitespace and advisory
source-size checks pass. Review covered peer filtering, TLS integration,
connection races, deadline errors and descriptor ownership.

Actual host/guest connectivity, peer filtering, certificate rejection, native
AIRC send/read/replay and joined shutdown remain unverified. No tests, native
vsock probes or normal/race gate were run for this implementation. Local
framed-TCP evidence does not qualify the vsock path; Hyper-V sockets are a
separate roadmap mode.
