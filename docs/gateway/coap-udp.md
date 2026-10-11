# Plain CoAP over UDP

Enable `coap_udp` with a numeric UDP bind address. No TLS or HTTP Origin
fields are accepted; the listener access policy applies. The listener speaks
ordinary [RFC 7252](https://www.rfc-editor.org/rfc/rfc7252.html) CoAP,
confirmable or not, with the same paths as the other CoAP carriers.

## Wire contract

POST `/v1/coap/rpc` with a plain [frame](framed-stream-v1.md) whose request
names its author, as on every [network carrier](author-network-carriers.md).
A UDP source address is unverified, so it carries no session: every request
names its own author and token, and `account.identify` does not persist. The
result comes back in the CoAP response (piggybacked or separate) to the
request's source, with Block2 for results larger than one block. A reply is at
most one 1 KiB block per request packet, which bounds what a spoofed source
can be sent; the client fetches further blocks. The Observe registration on
`/v1/coap/observe/register` and `/v1/coap/observe` carries room watches. The
request ceiling is 16 KiB and responses are bounded to 64 KiB.

The CoAP server bounds peers by the shared session slots, queues at most 16
received messages per peer, closes a peer after 30 idle seconds, and
retransmits a confirmable message at most twice, one second apart at first. Ordinary dispatch has four seconds
and streams twenty, preserving the shared native leases. Shutdown closes owned
I/O and joins request workers and native watch leases before releasing peer
slots. A CoAP message ID or local write cannot establish AIRC acceptance.

## Reading and uncertainty

`history.read` requests use explicit limits and opaque cursors. Incomplete or
lost output leaves the last complete cursor. Keep the original durable
mutation ID and exact intent, including validity timestamps, when retrying an
uncertain result. Recover acceptance through `receipt.get` and its native
message ID. NON delivery remains lossy, and the shared delivery policy retains
honest possible duplicates at the native acceptance boundary.

## Verified local acceptance

Dated records follow. Where they mention principals, bindings, signed or
encrypted envelopes, signing keys or return routes, they describe the
[removed identity model](removed-identity.md), which no longer exists.

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
