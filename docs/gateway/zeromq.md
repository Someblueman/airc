# ZeroMQ local REQ/REP profile

The private `internal/gateway/zmqwire` codec implements bounded ZMTP 3.1
framing. It is not yet registered as a gateway listener. Service integration
and native-client qualification remain on the [roadmap](ROADMAP.md).

The selected profile is numeric loopback TCP, server REP and client REQ,
with ZMTP NULL and mandatory encrypted, signed AIRC application envelopes.
NULL and peer metadata confer no sender authority. The service must enforce
loopback, authenticate each envelope through the existing dispatcher, bound
session/request capacity, and own handshake/read/write deadlines and shutdown.

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
