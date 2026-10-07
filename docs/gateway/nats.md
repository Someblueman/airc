# NATS TLS request/reply profile

This is the selected implementation contract, not a supported-mode claim.
Implementation and native qualification remain in [ROADMAP.md](ROADMAP.md).
The endpoint implements a bounded subset of the
[NATS client protocol](https://docs.nats.io/reference/protocols/client).
It accepts clients directly; it does not subscribe to an external broker or
implement a general broker, routing cluster, JetStream or persistent subscription.

## Authority and requests

The endpoint announces TLS and authentication requirements with INFO, then
requires TLS 1.3 before CONNECT or credentials. CONNECT `auth_token` resolves
an existing active AIRC bearer principal. Client names and subjects do not
establish publisher identity. User/password, NKey and JWT authentication are
outside this selected profile.

Each configured route grants one principal, one literal request subject, one
distinct literal response subject, one exact existing AIRC target and explicit
Send, ContextRead, HistoryRead or Search operations. Subjects cannot contain
wildcards, whitespace or controls. Request and response subjects cannot collide
across routes. Each grant must fit current listener and principal permissions.
History/Search require the existing cursor key; responses require the existing
response signer. The principal must have bearer and envelope-signing credentials.

A client subscribes to its granted response subject before publishing to the
paired request subject, with that response subject as the PUB reply subject.
The payload is the existing compact signed JWS message envelope. Its verified
principal and binding must match CONNECT, its target must match the route,
and its operation must be granted. Signed request IDs and validity times retain
their existing durable replay meaning. No new identity, request or cursor format
is introduced.

Signed MSG results use the granted subscription identifier. Reads retain the
existing explicit page/cursor boundary. One pending request/result per connection
and fixed write deadlines bound response pressure; a slow client is closed
rather than building an unbounded delivery queue. Reconnection and resubmission
of identical signed intent recover durable mutation state. Transport subscription
identifiers are connection-local and never become journal request IDs.

## Wire and lifecycle bounds

The framing prerequisite handles INFO/CONNECT, PUB, SUB, UNSUB, MSG, PING/PONG,
+OK and -ERR with CRLF command lines. Operation names are case-insensitive as
specified by NATS. INFO and CONNECT JSON, subjects, subscription identifiers,
decimal payload lengths and trailing CRLF are bounded before allocation.
The selected profile advertises no header support and refuses HPUB, HMSG,
queue-group subscriptions, wildcard subscriptions and ungranted subjects.

Initial bounds are 32,768 payload bytes, 4,096 command-line bytes, 255 subject
bytes, 64 subscription-identifier bytes and one response subscription per
connection. Configuration has 1-128 routes. These are gateway profile limits,
not claims about general NATS limits. Encoders and decoders must enforce the
same boundaries and preserve buffered bytes beyond the declared payload and its
trailing CRLF. Reads consume at most the bounded declared payload length;
truncation, invalid lengths or invalid trailing CRLF terminate the connection.
An overstated length can absorb subsequent command bytes because NATS has no
independent payload delimiter; the decoder cannot reliably identify that case
when those bytes also satisfy the declared framing.

The listener uses existing global socket/request capacity and principal-session
limits. TLS/CONNECT and each started frame have absolute deadlines; heartbeat
traffic cannot renew a started request deadline. Cancellation closes sockets,
joins owners and releases their capacity. Credentials never appear in errors
or discovery output.

## Delivery meaning and qualification

[Core NATS](https://docs.nats.io/learn/core-nats/) has at-most-once delivery;
publication alone does not establish subscriber receipt. This direct gateway
profile has no pre-intake persistence or broker replay. A connection failure,
capacity refusal or process exit before journal admission may lose a publication.
PING/PONG and optional verbose +OK acknowledge protocol processing only.
Neither establishes durable intake or native AIRC acceptance. A signed result
describes the actual journal/read outcome; native delivery is established through
the existing delivery state and independent AIRC observation.

Qualification must use a real NATS client, exact author/target/body observation,
complete bounded reads, credential/signature/route refusals, replay after lost
responses and restart, oversize/malformed framing, slow peers, capacity release
and joined shutdown. Codec controls and simulated peers alone cannot complete
that item. No NATS executable or cached client/server image was found locally
on 2026-10-07; acquiring or installing one requires separate authorization.
