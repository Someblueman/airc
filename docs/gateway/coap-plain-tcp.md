# Plain CoAP over TCP

Plain author requests need no principal, bearer or key on this carrier; see
[author network carriers](author-network-carriers.md). The rest of this page
describes the legacy signed/encrypted profile where it differs.

Enable `coap_tcp` with a numeric TCP bind address and an explicit operation
allowlist. TLS and Origin fields are rejected. Each operation requires the
existing encrypted, signed detached envelope; neither the connection address
nor a CoAP token supplies AIRC authority. Provision the gateway response signer
and exactly one `coap_tcp` return route per principal, with the matching
listener ID, `destination: "connection"`, recipient key ID and P-256 public-key
file. The verified principal and binding select the recipient. Results return
on that connection; this route never dials a caller-supplied address.

## Request and response contract

The standard TCP CoAP CSM exchange precedes application messages. The gateway
advertises a 1152-byte maximum receive frame and standard blockwise support.
Peer limits, Block1/Block2, output capacity, deadlines and joined lifecycle use
the existing [CoAP TCP engine](coap-tcp.md). Plain TCP uses the same RFC 8323
framing, without UDP message IDs or confirmable/non-confirmable types.

`POST /v1/coap/rpc`, with Content-Format `application/json`, carries the
version-1 FrameRequest, `profile: "encrypted"`, an encrypted signed operation
envelope, and a bounded correlation ID. The assembled request ceiling is
16 KiB. The logical mutation ID, target, author authority and validity fields
remain inside the authenticated envelope. Reconnect retries retain the original
intent and durable ID, with fresh transport tokens and correlations.

An authenticated result is returned as CoAP Content with a JSON string holding
the protected response token. Decrypt it for the provisioned recipient and
verify the gateway Ed25519 signature, freshness and exact
realm/principal/binding/route scope using [UDP response verification](udp.md).
Only then decode the FrameResponse and check its correlation, operation and
sequence. CoAP Content establishes that an application response was carried;
AIRC acceptance requires the durable receipt and native message ID. Application
errors are also protected and use the same Content carrier code. Invalid input
may receive an empty generic CoAP error; it exposes no native metadata and does
not renew authenticated activity.

Whole protected bodies must fit the 64-KiB response ceiling, including JSON
framing. Plaintext FrameResponse payloads use the shared JOSE payload ceiling,
which is also advertised in capabilities. Peer receive limits still apply to
each physical frame; Block2 returns complete protected bodies without truncation.

## Bounded reads and lifecycle

`history.read` accepts explicit limits and opaque cursors. Ordinary watch
requests provide long polling; the only advertised watch mode is `long_poll`.
CoAP Observe and registration resources are unavailable on this profile. A lost
or interrupted response leaves the last completely verified cursor. Durable
receipts recover uncertain writes; possible native duplicates remain governed
by the shared delivery policy.

The listener owns connection admission. The engine bounds initial CSM to five
seconds, active operations to four, its receive queue to sixteen, token state
to 256 and blockwise reception to three seconds. Ordinary dispatch retains its
four-second bound. Writes and sending block continuations are bounded, and
connections retain the thirty-second authenticated idle and two-minute absolute
lifetimes. Shutdown closes the connection and joins handlers, expiry work and
native read leases.

## Verified local acceptance

A real go-coap v3.5.4 TCP client uses a plain socket and CSM negotiation against
a standalone Darwin gateway. Two multiline Unicode bodies exceeding 3500 bytes
reach durable accepted receipts. An independent native observer and CHECK
records verify exact native IDs, author, target and bodies. Replaying the first
intent through a new TCP connection retains the native ID and one send attempt.
One-message history pages preserve complete native metadata, `more`/`ok`
status, nonempty cursors and no gaps, followed by an empty caught-up resume.

Signed-only and tampered requests, and an attempted Observe registration,
produce no native intake or journal jobs; the same connection subsequently
accepts a valid encrypted request. SIGINT during a confirmed active native
long poll joins both gateway and caller, releases the lease, leaves no jobs
and permits TCP rebinding. The new native group passes twice under race
(14.398 seconds). Existing TLS/DTLS acceptance regressions also pass twice
(20.216 seconds). Verified capability checks pass twice (10.577 seconds);
existing TLS Observe, certificate, pre-CSM and peer-limit checks pass under race
(22.765 seconds). Shared gateway and CLI race suites, build, vet, formatting and
staticcheck pass. Logs are under `/tmp/airc-roadmap-baseline/` with prefix
`coap-plain-tcp-`.

This is native local evidence. OSCORE and remote constrained-device acceptance
remain outstanding in [ROADMAP.md](ROADMAP.md), together with final full-module
acceptance on the eventual unchanged source.
