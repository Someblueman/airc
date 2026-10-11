# ZeroMQ local REQ/REP profile

Plain author requests need no principal, bearer or key on this carrier; see
[author network carriers](author-network-carriers.md). The rest of this page
describes the legacy signed/encrypted profile where it differs.

The `zeromq` listener implements bounded ZMTP 3.1 framing through
`internal/gateway/zmqwire`. Full service integration acceptance passed at the
[recorded checkpoint](testing.md#zeromq-service-integration-checkpoint). Native-client
qualification remains on the [roadmap](ROADMAP.md).

The selected profile is numeric TCP, server REP and client REQ, with ZMTP
NULL. NULL and peer metadata confer no sender authority: a REQ body is a plain
contract request naming its author (AIRC checks it), and `account.identify`
holds for the connection. Like IRC it needs no TLS, on any address; account
tokens are then visible on the network. A body that is not a JSON object is
the legacy encrypted signed envelope. The service caps connections, shares
global session/request capacity, and owns handshake/read/write deadlines and
joined shutdown.

The owner sends a complete 64-byte greeting and requires a complete peer
greeting with NULL and protocol version at least 3.1. Signature padding is
ignored; older protocol versions and other security mechanisms are refused.
The codec accepts client READY with exactly one case-insensitive Socket-Type
property whose value is REQ. Metadata names must be unique, with at most 32
properties and a total command body of at most 4,096 bytes. Other bounded
properties are ignored, including Identity and Resource.

Requests consist of one empty MORE delimiter and one nonempty final data
frame. Replies use the same envelope. Application bodies are at most 65,536
bytes; commands cannot carry MORE. Declared lengths are checked before
allocation. Correlation envelopes, multipart application bodies and older
protocol negotiation are outside this profile. The owner enforces the
REQ/REP alternating exchange; between messages it answers a ZMTP 3.1 `PING`
heartbeat with `PONG` and its context, and refuses other commands.

Wire semantics follow [ZMTP 3.1](https://rfc.zeromq.org/spec/37/) and
[REQ/REP](https://rfc.zeromq.org/spec/28/). These codec tests do not establish
interoperability with a native ZeroMQ client or delivery through native AIRC.

## Listener and application contract

Configure an enabled listener with protocol `zeromq` and a numeric address
(for example `0.0.0.0:5555`; the default is `127.0.0.1:0`). Operations default
to every author operation. TLS and browser origins are outside this profile.
The legacy body (raw compact JWE with a signed envelope) is limited to `send`,
`context.read`, `history.read` and `search` and needs a recipient key.

Each REP body is JSON with `version: 1`, the request's `request_id` when known,
and exactly one `result` (the existing detached result) or `error` (the stable
AIRC failure). Unauthenticated requests and pre-authentication saturation have
no request ID. Read results preserve the entire page, opaque cursor and gap
facts; an oversized encoded response becomes a capacity error with no partial
result. Send replies report durable intake, independently of native acceptance.
Replayed signed send IDs use the existing journal rules.

The complete handshake, each request once its first byte arrives, each
dispatch and each whole reply write have a three-second limit. An idle
connection between messages has no deadline and stays open, bounded by the
listener's connection caps. Each connection executes requests serially;
cancellation closes transport I/O and joins admitted dispatch before the
service closes the journal. A watch answers once (long poll).
