# Plain CoAP over TCP

Enable `coap_tcp` with a numeric TCP bind address. TLS and Origin fields are
rejected; the listener access policy applies. Each request names its author,
as on every [network carrier](author-network-carriers.md); the connection is
one author session, and an `account.identify` request sets its default
author. Neither the connection address nor a CoAP token selects an author.
Results return on the same connection. Account tokens cross the connection in
the clear.

## Request and response contract

The standard TCP CoAP CSM exchange precedes application messages. The gateway
advertises a 1152-byte maximum receive frame and standard blockwise support.
Peer limits, Block1/Block2, output capacity, deadlines and joined lifecycle use
the existing [CoAP TCP engine](coap-tcp.md). Plain TCP uses the same RFC 8323
framing, without UDP message IDs or confirmable/non-confirmable types.

`POST /v1/coap/rpc`, with Content-Format `application/json`, carries a
version-1 plain [frame](framed-stream-v1.md) with a bounded correlation ID.
The assembled request ceiling is 16 KiB. Reconnect retries retain the original
intent and durable ID, with fresh transport tokens and correlations.

The result is CoAP Content (or Bad Request for an error frame) whose JSON body
is the `FrameResponse`; check its correlation, operation and sequence. CoAP
Content establishes that an application response was carried; AIRC acceptance
requires the durable receipt and native message ID. Invalid input may receive
an empty generic CoAP error; it exposes no native metadata and does not renew
activity. Whole bodies must fit the 64-KiB response ceiling. Peer receive
limits still apply to each physical frame; Block2 returns complete bodies
without truncation.

## Bounded reads and lifecycle

`history.read` accepts explicit limits and opaque cursors. Ordinary watch
requests provide long polling, and the Observe registration of
[coap-tcp.md](coap-tcp.md) carries live watches on this profile too. A lost
or interrupted response leaves the last complete cursor. Durable
receipts recover uncertain writes; possible native duplicates remain governed
by the shared delivery policy.

The listener owns connection admission. The engine bounds initial CSM to five
seconds, active operations to four, its receive queue to sixteen, token state
to 256 and blockwise reception to three seconds. Ordinary dispatch retains its
four-second bound. Writes and sending block continuations are bounded, and
connections retain the thirty-second idle and two-minute absolute
lifetimes. Shutdown closes the connection and joins handlers, expiry work and
native read leases.

## Verified local acceptance

Dated records follow. Where they mention principals, bindings, signed or
encrypted envelopes, signing keys or return routes, they describe the
[removed identity model](removed-identity.md), which no longer exists.

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
