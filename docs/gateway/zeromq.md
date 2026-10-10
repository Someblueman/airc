# ZeroMQ local REQ/REP profile

The `zeromq` listener implements bounded ZMTP 3.1 framing through
`internal/gateway/zmqwire`. Full service integration acceptance passed at the
[recorded checkpoint](testing.md#zeromq-service-integration-checkpoint). Native-client
qualification remains on the [roadmap](ROADMAP.md).

The selected profile is numeric loopback TCP, server REP and client REQ,
with ZMTP NULL and mandatory encrypted, signed AIRC application envelopes.
NULL and peer metadata confer no sender authority. The service enforces
loopback, authenticates each envelope through the existing dispatcher, shares
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
protocol negotiation are outside this profile. The owner must enforce the
REQ/REP alternating exchange and refuse unsupported commands.

Wire semantics follow [ZMTP 3.1](https://rfc.zeromq.org/spec/37/) and
[REQ/REP](https://rfc.zeromq.org/spec/28/). These codec tests do not establish
interoperability with a native ZeroMQ client or delivery through native AIRC.

## Listener and application contract

Configure an enabled listener with protocol `zeromq`, numeric loopback address
(for example `127.0.0.1:5555` or `[::1]:5555`) and explicit operations selected
from `send`, `context.read`, `history.read` and `search`. Configure at least one
gateway recipient private key and the existing sender signing keys, principals,
bindings and target permissions. TLS and browser origins are outside this local
profile. The default address is `127.0.0.1:0`.

The REQ body is the raw compact JWE containing the signed AIRC envelope. Each
REP body is JSON with `version: 1`, the authenticated `request_id` when known,
and exactly one `result` (the existing detached result) or `error` (the stable
AIRC failure). Unauthenticated requests and pre-authentication saturation have
no request ID. Read results preserve the entire page, opaque cursor and gap
facts; an oversized encoded response becomes a capacity error with no partial
result. Send replies report durable intake, independently of native acceptance.
Replayed signed send IDs use the existing journal rules.

The complete handshake, each whole request read, each dispatch and each whole
reply write have a three-second limit. Reads also retain the original signed
replay deadline. Idle connections close after three seconds. Each connection
executes requests serially; cancellation closes transport I/O and joins admitted
dispatch before the service closes the journal. There is no watch or heartbeat
command support in this profile.
