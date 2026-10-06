# DTLS over UDP

The `dtls` listener binds a dedicated UDP endpoint and requires an owner-provided
certificate and private key, including on loopback. The ready record reports
its actual address. The implementation pins Pion DTLS v3.1.5 and uses DTLS 1.2
with ALPN `airc-gateway-dtls/1`, ephemeral ECDH, AES-128-GCM and required extended
master secret. This tag implements DTLS 1.2; it does not establish DTLS 1.3
interoperability. See the [tagged library](https://github.com/pion/dtls/tree/v3.1.5).

Clients must verify certificate trust and the endpoint name. The current
certificate profile requires RSA or ECDSA server keys. The pinned release
rejected the existing Ed25519 test certificate during certificate signature
validation; that limitation is not bypassed with insecure verification.
Application signatures continue to use the shared Ed25519 JWS profile.
Client certificates and PSKs are not AIRC principal credentials. Each request
must pass the existing detached signed/encrypted envelope authorization.

## Application datagrams

DTLS preserves application records; it does not turn UDP into a reliable byte
stream. Each DTLS record carries one AGD1 fragment with the existing
[WebTransport fragment layout](webtransport.md#datagram-frames). There is no
length-prefixed stream handshake, HTTP prefix, connection bearer credential or
raw AIRC command escape hatch. The carrier advertises `dtls_datagrams` and
`long_poll` alongside the current 32 shared operations.

AGD1 requests are bounded to 16 KiB and responses to 64 KiB. Each fragment is at
most 1000 bytes, leaving room for DTLS record overhead inside the 1200-byte UDP
profile. Four incomplete assemblies expire after five seconds. A bounded
64-entry completed-frame cache suppresses frame IDs for thirty seconds.
Response sequence numbers start at one for each correlation; deliver only
contiguous pages and keep the last contiguous opaque cursor after loss. Legacy
`watch` seals the unchanged native position with a fresh nonce each time;
`events.watch` reuses its cursor token while that position is unchanged. Live
OBSERVE records omit the persisted sequence and receipt fields available from
CHECK; other live message metadata is preserved.

DTLS retransmits handshake flights, but application messages and responses can
still be lost or reordered. Keep the original durable request ID and exact
authenticated intent when retrying an uncertain mutation. Use fresh correlation
and transport frame IDs after reconnect. Receipts report native acceptance;
packet delivery, local writes and transport setup do not prove it. Bounded
retries retain the shared honest possibility of duplicate native posts.

## Admission and lifecycle

The gateway owns the UDP socket and peer demultiplexer because the library's
default packet listener does not expose the required queue byte limit. Packets
larger than 1200 bytes and invalid initial ClientHello records are dropped
before peer allocation. Shared service slots bound associations before
handshake processing. Each peer has a 32-packet queue; overflow drops packets.
Writes are serialized and can target only that peer's fixed endpoint.
Source IP/port supplies no AIRC authority. Connection IDs and endpoint migration
are not enabled; reconnect creates a new association.

Handshakes have a five-second bound and application writes a three-second bound.
Ordinary dispatch has a four-second bound. Native watch leases last fifteen
seconds within the shared twenty-second dispatch bound, with at most 128 pages
and one MiB including framing. Quiet association expiry is thirty seconds, checked once per second. Only
successful handshakes and completed authenticated result/page writes renew
activity. Raw packets, malformed records and authentication-error replies cannot
renew the lease.
Shutdown stops admission, closes owned socket I/O and peers, joins connection
workers and native leases, then releases accepted admission slots.

## Acceptance

The Darwin loopback native race run verifies all current 32 shared operations
against independent native records and mutation facts. Actual UDP proxy controls
verify handshake retransmission, uncertain response loss, reordered/duplicated
records and durable replay on the same association and after reconnect. The
encrypted datagram replay fixture verifies one native send attempt, exact native
IDs, authors, targets and bodies, then reads the two complete CHECK records
through consecutive one-message pages and an empty caught-up cursor resume.
Every page preserves native metadata, a nonempty cursor and no gap. Native
watch tests verify retained/live metadata, authenticated heartbeat positions,
fifteen-second lease expiry and remote close; SIGINT joins an active native
watch and incomplete request before UDP rebind. Certificate trust/name and ALPN
failures, tampered signatures and malformed/oversized records produce no native
intake. See [STATUS.md](STATUS.md) for exact checks and publication state.

The focused DTLS replay and certificate/ALPN/tamper boundary tests pass twice
under `go test -race` on Darwin; `go vet ./integration/gateway` passes.
This is local certificate-verified ECDSA client evidence.

Native acceptance uses ECDSA certificates. RSA interoperability, remote
networks/PMTU, DTLS 1.3 and complete catalogue evidence remain outstanding. Additional
native AIRC operations are optional; see [ROADMAP.md](ROADMAP.md).
