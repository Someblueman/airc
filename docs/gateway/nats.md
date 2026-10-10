# NATS TLS request/reply profile

The selected profile is implemented and can be enabled in gateway configuration.
Native-client qualification remains in [ROADMAP.md](ROADMAP.md); this is not a
qualified-mode claim.
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
these grants; enabled NATS is accepted by the service integration described below. Options on
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
aliases and, at that configuration checkpoint, refusal of unfinished enabled
service. This establishes configuration
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
This supplies authentication evidence only; native-client qualification remains unfinished. Service integration is described
below.

## Connection ownership prerequisite

The connection owner retains the upgraded socket, buffered input, immutable
principal route snapshot and cancellation lifetime. After principal admission,
`connected` emits +OK only when verbose is enabled and starts one heartbeat
writer. Starting it twice is refused. Writes share a mutex and a fixed
three-second deadline, bounded by an earlier caller deadline. A terminal -ERR
claims the final write; subsequent heartbeat/application writes are refused.

Every ten seconds the owner sends PING. At most two unanswered server PINGs
are permitted; PONG resets that count, and the next tick with two outstanding
PINGs cancels the socket. Reads wait at most thirty seconds for a first byte,
then allow at most three seconds for the whole frame. Caller deadlines bound
both waits. Parent cancellation closes the raw socket before TLS close-notify
can block; close joins its cancellation callback and heartbeat writer.

One fixed response subscription belongs to the session reader/dispatcher.
SUB must match a response subject granted to the authenticated principal; a
second subscription or mismatched UNSUB is refused. UNSUB without a count,
with zero, or with a count already reached removes it immediately. Otherwise
its copied unsigned 32-bit count removes it after that many complete response
writes. A session reaching the counter limit also removes the subscription
rather than wrapping. No delivered-message count is a durable acknowledgement.
Verbose SUB/UNSUB receive +OK; PING receives PONG, and PONG receives no response.

The original controls-only loop has been replaced by the signed publication
loop described below. These connection-ownership tests alone do not qualify a
native NATS client.

On 2026-10-07, the broader affected race suites passed: adapter 119.804s,
auth 7.312s, config 4.129s and codec 2.148s, with vet/staticcheck/format/diff
checks passing. A subsequent caller-deadline correction explicitly refuses
expired deadlines before consuming buffered input and checks frame completion
against the fixed deadline. All NATS authentication/ownership controls then
passed with race detection on the final source in 29.269s, with adapter vet,
staticcheck, formatting and diff checks passing. The unchanged other adapter
and credential/configuration/codec checks retain the preceding broader evidence.

Controls cover the actual ten-second heartbeat schedule, terminal write ordering,
two outstanding PINGs and PONG reset, fixed subscription authority and count
ownership, auto-unsubscribe/counter bounds, verbose control replies, publication
refusal before dispatch, failed-open socket closure, blocked read/write cancellation,
joined heartbeat cleanup, trickled-frame expiry, earlier caller deadlines during
heartbeat traffic and writes, and expired-deadline refusal without consuming an
already-buffered frame. The initial closed-pipe test expected SetReadDeadline to
succeed on an already-closed pipe; that fixture expectation was corrected without
changing production socket-close behavior. No enabled service or native NATS
qualification is established by this checkpoint.

## Listener admission prerequisite

The NATS listener now owns the existing shared socket-slot channel directly;
the service must not wrap it with a second acquisition. Slots include sockets
waiting for INFO/TLS/CONNECT. Overcapacity sockets close before receiving INFO.
Authenticated principal-session claims are bounded per listener and reserved
before CONNECT +OK or heartbeat startup. A refused principal receives terminal
-ERR. Principal and global claims release only after joined connection cleanup.

Cancellation closes the raw TCP listener and every connection owner, then joins
all owners. Unexpected Accept failure also cancels and joins accepted owners.
Foreign slots held by other transports remain untouched. Route/operation slices
and the TLS configuration are snapshotted at startup. Public INFO uses one random
server identifier per listener lifetime, the configured realm as server name,
gateway profile version 1.0.0, the actual listener host/port and the negotiated
profile limits; each connection sees the same server metadata.

On 2026-10-07, focused listener race controls passed in 1.727s. After adding
stable INFO identity/address controls, all NATS authentication, connection and
listener race controls passed on the final source in 29.505s. Adapter vet,
staticcheck, formatting and diff checks passed. Real isolated TCP/TLS controls
cover preauthentication capacity, foreign-slot preservation, principal refusal
before success acknowledgement, capacity reuse, bounded subscription/heartbeat
control flow, credential/read/framing/unimplemented-publication failure cleanup,
Accept failure and joined shutdown. These manual wire clients establish listener
admission only. Signed application dispatch is described below; enabled service
and actual native NATS client/AIRC qualification remain unfinished at this
listener-admission checkpoint.

## Signed publication admission prerequisite

The admission helper accepts a nonempty bounded PUB payload only on the granted
request subject with the exact paired reply subject. It authenticates the existing
compact JWS envelope through the dispatcher, then requires its principal and
binding to match CONNECT, its target to equal the fixed grant and its operation
to be Send, ContextRead, HistoryRead or Search within that grant. Subjects and
connection-local subscription identifiers never replace signed durable intent.

Only after establishing that response scope does it attempt a nonblocking claim
on the existing shared request capacity. Busy admission returns a correlated
transient capacity failure. Execution has one three-second timeout bounded by an
earlier parent deadline, releases its request slot on every exit and uses the
existing current-authority and durable-replay path. Send success requires a
durable journal result. Identical intent coalesces after reopen or granted subject
changes; changed intent under the same request ID conflicts. Durable revocation
refuses both fresh mutations and replay. Read attempts inherit existing signed
validity/cursor rules and cancellation of their real native sockets.

Authentication, metadata or route mismatch has no authorized result scope and
returns a refusal without an echoed request ID. Execution failures after valid
scope establishment carry the verified request ID and existing bounded failure
code. Signed result encoding and subscription dispatch are described below.
Admission alone sends no NATS acknowledgement or response and
does not establish native AIRC acceptance.

On 2026-10-07, final affected race checks passed: all NATS adapter controls
33.827s, dispatcher 1.622s, store 42.012s and auth 5.068s. Vet and staticcheck
passed for those packages; formatting and diff checks passed. Admission controls
verify exact Unicode/multiline body and target persistence, stable upstream identity
after journal reopen and subject changes, conflicting intent, request-slot release,
distinct CONNECT/signature principals and bindings, fixed target/operation/subject
grants, invalid signature/freshness/payload/metadata, capacity and cancellation,
durable revocation before fresh intake or replay, and parent-deadline closure of
real native ContextRead/HistoryRead/Search socket attempts without mutation jobs.
Complete successful native cursor reads and real-client/AIRC delivery evidence
remain integration/qualification requirements.

## Signed result prerequisite

The result builder signs the existing response envelope with the configured
Ed25519 response key. Its inner JSON contains version `1`, protocol `nats/core`,
the verified request ID, both literal subjects, the configured target, and exactly
one result or failure. Subscription identifiers remain connection-local and are
absent from the signed body.

The envelope scope includes realm, principal and binding. Its route identifier is
the lowercase hexadecimal SHA-256 digest of `nats-core-response`, a NUL byte,
request subject, NUL, response subject, NUL, target kind, NUL, and target name.
This ordered, protocol-specific scope prevents accepting a result under another
subject pair, target, identity or protocol. Clients must also check the signed
inner request ID against their pending request.

Send results require durable journal admission and matching request IDs; they do
not turn an unknown native acceptance into success. Context, history and search
results retain the entire dispatcher result, including messages, context omissions,
opaque cursor, page status and gap indicator. The builder refuses a signed token
larger than the 32,768-byte NATS payload limit without truncating the result.
A bounded signed capacity failure can be built instead; selecting and publishing
that failure belongs to the subscription-loop integration.

On 2026-10-07, focused result race tests passed in 1.574s. Controls cover successful
send and failure correlation, scope changes, cross-protocol rejection, expiry,
required operation payloads, complete Unicode/multiline read round trips, and
oversized cursor refusal without mutation. These establish signed representation
only; actual native reads and NATS publication remain qualification requirements.
The affected NATS/response-authentication race suites passed in 35.080s and
1.976s respectively; affected vet, staticcheck, formatting and diff checks passed.

## Signed publication loop prerequisite

`ServeNATS` now requires the existing dispatcher, response sealer and shared
request-capacity channel. The sole session reader handles controls or completes
one publication's admission and response before reading another frame. There is
no application publication queue. PUB requires an active fixed response
subscription; its request/reply subjects must match that subscription's route.
An unauthenticated, mismatched or unsubscribed publication receives terminal
generic -ERR without a success acknowledgement or journal intake.

After valid authority is established, both successful dispatch and bounded
execution failures receive a signed MSG on the fixed response subject and current
subscription ID. Oversized results become a signed transient capacity failure;
the original result is never sliced. Verbose +OK precedes MSG and acknowledges
protocol handling only, including when MSG reports a failure. Each complete MSG
consumes one subscription delivery count. Failed writes end the session and leave
durable admission uncertain to the publisher; replay uses the signed request ID.
Parent cancellation closes the owner socket, cancels dispatch and joins all
listener owners before releasing session capacity.

Real isolated TCP/TLS fixture tests cover durable signed send results, unchanged
upstream identity on replay, signed permanent ID conflicts, auto-unsubscribe after
three results, missing subscription and wrong subject/reply/signature refusal
before journal intake, signed correlated capacity refusal, foreign request-slot
preservation and joined shutdown. Cancellation during an active native history
read closes its socket and joins dispatch with both capacity channels empty.
The affected race checks passed on 2026-10-07: NATS adapter 37.709s, response auth
2.535s and configuration 1.709s. The added active-read shutdown race check passed
in 1.770s. Affected vet, staticcheck, formatting and diff checks passed; unchanged
dispatcher/store behavior retains the prior admission checkpoint evidence.
These manual wire fixtures do not qualify a
native NATS client or establish actual AIRC delivery. Service integration follows below; native qualification remains outstanding.

## Service integration checkpoint

Configured enabled `nats` listeners use the existing service startup, dispatcher,
delivery workers, response signing key and shared socket/request limits. The
service gives the NATS owner a raw TCP listener and a TLS 1.3 configuration with
no framed-protocol ALPN; the owner sends public INFO before upgrading the socket.
The service does not wrap this listener with another capacity acquisition or a
TLS listener. Response-key loading and all listener preparation occur before the
ready record. Preparation failure closes previously owned listeners and journal.

On 2026-10-07, affected service/configuration race checks passed in 3.371s and
1.699s, with affected vet, staticcheck and diff checks passing. The actual service
fixture verifies INFO before trusted TLS, CONNECT/SUB and signed Send dispatch,
one-slot refusal before INFO, exact Unicode/multiline intent in the reopened
journal, listener rebind after cancellation, and journal-owner release. Separate
startup fixtures verify invalid response keys and a later occupied listener
never emit ready or retain the earlier NATS socket/journal. Initial fixture
failures were corrected by supplying the trusted hostname for manual TLS upgrade
and granting receipt inspection explicitly; no production TLS or receipt policy
was weakened.

Full integration acceptance uses frozen Go/module source SHA-256
`49b545538c82b310088c6030eaf2b1504f9b49406234b8f14a2a52a0901aca35`.
Its source baseline is `0e4a69d` plus the service integration changes. The first
attempt's logs and metadata use `/tmp/airc-gateway-nats-service-v1-0e4a69d`;
the successful retry uses the `v2` prefix described below. All required service
integration gates passed. This fixture is a
manual wire client and proves durable intake only, without a native NATS client
or independent AIRC delivery observation.

The first full normal run recorded a failure in
`TestGRPCSlowReaderWriteDeadlineReleasesNativeWatch`: after exhausting HTTP/2
stream credit with 3,913 bytes pending, the native observer count did not fall
within the test's four-second polling bound. That phase ended with terminal exit 1
after 664.187479s; no successful full acceptance
or cause is claimed. Executable source remains unchanged for focused repetition.
The same run also recorded
`TestMQTTRefusesSignedAuthorityFreshnessAndWireBeforeIntake/future` failing
at the healthy TCP reconnect after its negative control, with connection refused.
Both failures remain preserved for focused repetition; neither is
attributed to this integration change without further evidence.

Both exact failed tests then passed five repetitions each with race detection on
unchanged executable source (108.908s, terminal exit 0). The MQTT future control
passed in all five complete refusal-suite repetitions. The output is retained at
`/tmp/airc-gateway-nats-service-v1-focused-repeat.json`. This establishes repeated
success, without proving a cause or fix for the full-run failures. The successful fresh full
normal/race acceptance run uses `/tmp/airc-gateway-nats-service-v2-0e4a69d`
on the same source digest and explicitly references the prior failed run.

The fresh full normal phase passed with terminal exit 0 in 655.391641s; full
race passed in 1587.890822s on the same frozen source. Both recorded 3,419 test
passes, 53 skips and no failures, with 32 passing packages and ten without tests.
The integration package passed in 648.769s normally and 1300.410s under race.
Build, formatting (empty output), vet, staticcheck and diff gates all exited 0;
the runner terminated with exit 0 and the current source digest still matches.
Exact commands and phase timings are in
[testing.md](testing.md#nats-service-integration-checkpoint).
Both previously failing tests passed in these full runs. Neither the repeated
focused passes nor the fresh full passes establish a causal fix for the first
run's failures. Real NATS client and independent AIRC observer qualification
remain outstanding in the roadmap.

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
