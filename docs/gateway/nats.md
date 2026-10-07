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

The configuration prerequisite implements listener `protocol: "nats"` and
`nats.routes`, whose entries contain `principal`, `request_subject`,
`response_subject`, `target` and `operations`. Disabled configurations validate
these grants; enabled NATS remains refused until service integration. Options on
another protocol, empty or duplicate operations, ungranted listener operations,
cross-route subject collisions, missing TLS/credentials/signers, revoked or unknown
principals and target/permission mismatches are refused. Send-only routes do not
require an unused cursor key. The existing strict JSON and private-file boundaries
apply; no separate NATS credential store is introduced.

On 2026-10-07, the affected configuration and codec race suites passed in 3.540s
and 1.232s; existing authentication and CLI configuration consumers passed with
race detection in 6.669s and 2.673s. Vet, staticcheck, formatting and diff checks
passed for the affected configuration/codec packages. Controls cover exact
255-byte subjects, 128/129 route boundaries, disjoint operation grants, every
authority requirement above, strict published JSON names/duplicates/nulls/case
aliases and refusal of unfinished enabled service. This establishes configuration
validation only, not network authentication or native qualification.

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

## Authentication prerequisite

The authentication helper now writes bounded plaintext INFO advertising token
authentication, mandatory TLS, protocol 1, no headers and the codec payload limit.
It then upgrades the same raw socket to TLS 1.3 before reading CONNECT. The caller
supplies public server identity/name/address metadata and a TLS configuration;
the helper clones that configuration before enforcing TLS 1.3. Already-wrapped
TLS sockets are refused because this profile requires INFO before TLS.

One absolute three-second deadline covers INFO writes, TLS negotiation and the
entire CONNECT frame. Parent cancellation closes the raw socket and joins the
callback. On success, the helper returns the TLS connection, owned buffered
reader, verified identity and verbose preference. The caller must close on error
and retain that reader, including pipelined PING/SUB bytes. Principal-session
admission and CONNECT +OK remain the connection owner's responsibility.

CONNECT uses the existing strict JSON boundary and 64-byte bearer credential.
Only protocol 0/1 is accepted; user/password, JWT, NKey/signature fields and
header/no-responder support are refused. Client name/language/version metadata
is UTF-8, at most 255 bytes and contains no controls. Echo and pedantic preferences
do not change identity or route authority; subjects remain literal under the
selected profile. Verbose defaults to true when omitted, matching the protocol.
Malformed JSON, invalid credentials or a principal without a configured route
receive the same authentication error without including supplied credentials.

On 2026-10-07, affected race suites passed: adapter 102.015s, auth 7.575s,
config 5.706s and codec 2.105s. Vet, staticcheck, formatting and diff checks
passed. Strengthened prompt-cancellation and joined test-writer controls then
passed in the focused NATS race rerun, 13.786s, with vet/staticcheck/diff passing.
Controls use actual TLS negotiation and verify INFO contents, TLS 1.2/cleartext
refusal, CONNECT credential/schema/route boundaries, verbose defaults, buffered
pipeline preservation, blocked INFO, idle TLS/CONNECT, trickled CONNECT,
preauthentication PING refusal and cancellation before/during TLS/CONNECT.
This supplies authentication evidence only; service activation and native-client
qualification remain unfinished.

## Wire and lifecycle bounds

The framing prerequisite handles INFO/CONNECT, PUB, SUB, UNSUB, MSG, PING/PONG,
+OK and -ERR with CRLF command lines. Operation names are case-insensitive as
specified by NATS. INFO and CONNECT JSON, subjects, subscription identifiers,
decimal payload lengths and trailing CRLF are bounded before allocation.
The selected profile advertises no header support and refuses HPUB, HMSG,
queue-group subscriptions, wildcard subscriptions and ungranted subjects.

The codec bounds are 32,768 payload bytes, 4,096 command-line bytes, 255 subject
bytes, 64 subscription-identifier bytes and one response subscription per
connection. Configuration has 1-128 routes. These are gateway profile limits,
not claims about general NATS limits. Encoders and decoders must enforce the
same boundaries and preserve buffered bytes beyond the declared payload and its
trailing CRLF. Reads consume at most the bounded declared payload length;
truncation, invalid lengths or invalid trailing CRLF terminate the connection.
An overstated length can absorb subsequent command bytes because NATS has no
independent payload delimiter; the decoder cannot reliably identify that case
when those bytes also satisfy the declared framing.

The independent framing prerequisite is implemented in `internal/gateway/natswire`.
It accepts spaces or tabs between arguments, preserves arbitrary payload bytes,
requires literal subjects with nonempty dot-separated tokens, and bounds optional
UNSUB message counts to unsigned 32-bit values. INFO/CONNECT require bounded
JSON objects with duplicate keys and invalid Unicode refused; fixed CONNECT
credential/schema validation belongs to the authentication prerequisite. Encoding
uses the same profile bounds and rejects irrelevant frame fields.

On 2026-10-07, Darwin ARM64 affected race tests passed in 1.301s. Vet,
staticcheck, formatting and diff checks passed. A five-second, two-worker fuzz run
completed 326,601 executions in 5.341s with no failure. Independent wire controls
cover exact encoding, case-insensitive commands, tab separators, zero/binary/UTF-8
payloads, pipelining, every truncation of representative frames, exact size limits,
oversized length refusal before payload consumption, JSON boundaries and unsupported
commands. These results establish codec behavior only; no listener, authentication,
native NATS client or AIRC delivery qualification is claimed.

Commit review identified that general Unicode whitespace splitting exceeded the
selected ASCII argument grammar. The parser now splits only on space and tab;
nonbreaking-space and em-space SUB/PUB/UNSUB/PING refusal controls pass. The
corrected race suite passed in 1.301s, with vet, staticcheck and diff checks passing.
The corrected five-second fuzz run completed 317,734 executions in 5.355s without
failure.

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
