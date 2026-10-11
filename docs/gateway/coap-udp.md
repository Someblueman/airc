# Plain CoAP over UDP mailbox profile

Plain author requests need no principal, bearer or key on this carrier; see
[author network carriers](author-network-carriers.md). The rest of this page
describes the legacy signed/encrypted profile where it differs.

Enable `coap_udp` with a numeric UDP bind address and an explicit operation
allowlist. No TLS or HTTP Origin fields are accepted. Provision the existing
`response_signing_key` and exactly one return route per principal, with
`protocol: "coap_udp"`, the matching listener ID, a numeric unicast destination
with nonzero port, recipient key ID and P-256 recipient public-key file. The
route is selected from the verified principal and AIRC binding. A packet source
address supplies neither identity nor a response destination.

## Wire contract

Send a CoAP NON POST to `/v1/coap/fragments`, with Content-Format
`application/octet-stream`. Its body is one complete AGD1 request fragment, at
most 1000 bytes, using the layout in [WebTransport](webtransport.md#datagram-frames).
Only URI-Path and Content-Format options are accepted. This profile does not
combine AGD1 with CoAP Blockwise, Observe, CON retransmission or response tokens.
Each complete assembled body is the existing version-1 FrameRequest, with
`profile: "encrypted"` and its signed/encrypted detached operation envelope.
The request ceiling is 16 KiB. Signed-only, malformed and unverifiable requests
produce no mailbox output or native work. No unauthenticated error replies or
CoAP acknowledgements are sent to the request source.

Results are separate CoAP NON POSTs to `/v1/coap/mailbox` at the fixed return
endpoint, also with octet-stream Content-Format. Each body carries one AGD1
fragment of the protected response token. Reassemble the complete token, then
apply the existing [UDP response verification](udp.md): decrypt for the
provisioned recipient, verify the gateway Ed25519 signature, and check freshness
and exact realm/principal/binding/route scope before exposing its FrameResponse.
Its correlation, operation and contiguous response sequence establish which
logical request it answers. A CoAP MID or local write cannot establish AIRC
acceptance. Response tokens are bounded to 64 KiB and plaintext response payloads
to the shared JOSE payload ceiling.

The shared packet engine owns the socket, source slots, four incomplete
assemblies per source, five-second assembly/source inactivity bounds, completed
fragment IDs, global request admission and finite output budget. Four-second
ordinary dispatch and twenty-second streams preserve the shared native leases.
The one-MiB output budget includes the CoAP framing, with a reserved final-error
allowance. Writes have a three-second bound. Shutdown closes owned I/O and
joins request workers and native watch leases before releasing source slots.
Response MIDs are not reused until a 247-second idle interval; exhaustion of the
16-bit space is a response-capacity failure. This conservative interval follows
[RFC 7252 section 4.8.2](https://www.rfc-editor.org/rfc/rfc7252.html#section-4.8.2).

## Reading and uncertainty

Authenticated `history.read` requests use explicit limits and opaque cursors.
Long polling and finite AGD1 watch output use the same protected mailbox; this
is distinct from CoAP Observe. Advertised watch modes are `long_poll` and
`coap_udp_mailbox`. Incomplete or lost output leaves the last complete cursor.
Keep the original durable mutation ID and exact intent, including validity
timestamps, when retrying an uncertain result; use fresh fragment IDs and
correlations after reconnect. Recover acceptance through `receipt.get` and its
native message ID. NON delivery remains lossy, and the shared delivery policy
retains honest possible duplicates at the native acceptance boundary.

## Verified local acceptance

A real go-coap v3.5.4 UDP client submits encrypted requests to the standalone
Darwin gateway. Two multiline Unicode bodies exceeding 3500 bytes are verified
by an independent native observer and separate CHECK records, including exact
IDs, author and target. Durable accepted receipts and replay retain one native
attempt; the first replay uses a new client socket. Consecutive one-message
history pages match complete native metadata, report `more` then `ok`, retain
nonempty cursors without gaps and finish with an empty caught-up resume.

Boundary fixtures verify signed-only and tampered requests have no mailbox
output, journal job or native intake, followed by recovery on the same client.
SIGINT with an active native watch and incomplete request joins the gateway,
releases the native lease, leaves no jobs and permits UDP rebinding. Codec and
configuration tests cover malformed methods/options/fragments, oversize input,
protected route matching, and MID exhaustion/idle reuse. Focused native tests
and existing UDP loss/replay regression pass twice under race. Full affected
adapter/configuration/service and shared gateway race suites pass. CLI race
checks, `go build ./cmd/...`, `go vet ./...`, formatting and module staticcheck
pass. Native checks are in `coap-udp-native-race.out` (35.182 seconds), and
reconnect/observer checks in `coap-udp-reconnect-observer-race.out`, under
`/tmp/airc-roadmap-baseline/`.

This is a native encrypted NON mailbox profile, not OSCORE or remote constrained
device qualification. Plain TCP has a separate [protected connection profile](coap-plain-tcp.md).
OSCORE and device evidence remain on
[ROADMAP.md](ROADMAP.md). Final full-module acceptance remains outstanding there.
