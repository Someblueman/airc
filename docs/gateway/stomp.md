# STOMP 1.2 ingress

Implementation is in progress; service activation and native qualification remain
in [ROADMAP.md](ROADMAP.md). The selected profile follows the
[STOMP 1.2 specification](https://stomp.github.io/stomp-specification-1.2.html).

The intended endpoint uses TLS 1.3. CONNECT/STOMP login identifies a provisioned
AIRC principal and passcode verifies its bearer credential. Fixed request and
response destinations grant an exact principal, target and operation set.
Each SEND carries signed JWS intent from that same principal and binding.
Wire receipt IDs and connection-local acknowledgement IDs never replace the
signed request ID used for durable replay.

A SEND receipt establishes processing and durable intake for a mutation, not
native AIRC acceptance. Signed response MESSAGE frames expose delivery state
or a bounded History/Search/Context result through an explicitly subscribed
response destination. The selected acknowledgement mode is client-individual;
one pending response bounds storage and request admission. Lost receipts or
responses recover by resubmitting the identical signed request on a fresh
connection. Mutation validity times remain part of that stable intent.

Arbitrary broker forwarding, dynamic grants, transactions, cumulative
acknowledgements and general queue semantics are outside this profile.
No native, platform or full-service qualification is claimed yet.

## Frame bounds

Frames use UTF-8 commands and headers, LF or CRLF line endings, and a terminating
NUL. Header escaping follows STOMP 1.2, with CONNECT/CONNECTED exceptions and
first-value semantics for repeated headers. Unknown escapes are fatal.
Content lengths count bytes and permit embedded NUL only within the declared
body. SEND, MESSAGE and ERROR alone may carry bodies.

The codec limits each encoded frame to 65,536 bytes, each body to 32,768 bytes,
each line to 1,024 bytes and each frame to 32 header entries. Empty EOL frames
represent heartbeats; their timing and connection deadlines belong to the
connection owner. Bounds apply before peer-controlled allocation or iteration.

The frame codec is implemented independently of service activation. On
2026-10-07 (Europe/London), its affected race suite passed in 1.293s; vet,
staticcheck, formatting and diff checks passed. Independent wire examples cover
LF/CRLF heartbeats, pipelined frame boundaries, embedded-NUL content lengths,
first repeated-header values, CONNECT/CONNECTED escaping exceptions and exact
encoded bytes. Refusal cases cover invalid UTF-8/escapes, malformed or excessive
lengths, forbidden bodies, truncation and line/header/body/total-frame bounds.
This checkpoint supplies framing evidence only; no enabled service or native
STOMP qualification is claimed.

Subagent commit review found that escaped frames admitted raw colons in header
values. The parser now rejects them before unescaping while preserving the
CONNECT/CONNECTED exceptions. Exact escaped-colon encoding and both SEND/STOMP
raw-colon refusals pass; the final affected race rerun passed in 1.321s, with
vet, staticcheck, formatting and diff checks also passing.

## Fixed destination configuration

The `stomp` listener options contain 1-128 `routes`. Each route has `principal`,
`request_destination`, `response_destination`, `target` and explicit `operations`.
Destinations are distinct literal ASCII strings of 1-255 bytes; wildcard syntax,
whitespace, controls and cross-route request/response collisions are refused.
The allowed operations are Send, ContextRead, HistoryRead and Search.
Every listener operation must have a route grant, and each grant must fit both
listener and principal permissions for the exact target. Principals require
active bearer and signing credentials; responses require a provisioned signer.
History/Search also require a cursor key. TLS is mandatory and origins are not
accepted. At this configuration checkpoint, enabled STOMP remains rejected
until the adapter is wired into the service.

On 2026-10-07 (Europe/London), the affected configuration race suite passed in
1.371s; vet, staticcheck, formatting and diff checks passed. Controls exercise
revocation, missing credentials, target/operation mismatches, grant collisions,
route/destination bounds, disjoint operation grants, send-only cursor exemption
and the JSON field contract. Service and native qualification remain unfinished.

## TLS authentication prerequisite

The authentication helper requires an actual TLS connection negotiated at TLS
1.3, then reads CONNECT or STOMP under one absolute three-second deadline.
The version list must contain exactly `1.2` as one entry, `host` must equal the
configured realm, and `login` must equal the principal resolved from `passcode`.
That principal must have a fixed route. Leading heartbeat EOLs and partial
frames cannot renew the deadline. Cancellation closes the socket and joins its
callback. The caller retains the buffered reader so pipelined bytes remain
available. CONNECTED and socket/principal admission belong to the connection
owner; authentication alone does not authorize request dispatch.

Heartbeat syntax is two unsigned decimal millisecond values from 0 through
10,000. The planned server offer is `1000,1000`; negotiation takes the maximum
for each enabled direction and zero disables that direction. Actual scheduling,
postauthentication deadlines and joined connection ownership remain unfinished.

On 2026-10-07 (Europe/London), affected race suites passed: adapter 40.239s,
codec 1.197s, auth 2.514s and config 1.829s. Vet, staticcheck, formatting and
diff checks passed. TLS controls verify CONNECT/STOMP identity and buffered
pipeline preservation, TLS 1.2/cleartext refusal, joined cancellation and idle,
partial-frame and repeated-heartbeat deadline bounds. Authority controls cover
realm, version, login/bearer mismatch, route permission and heartbeat grammar.
Enabled service and independent native qualification remain unfinished.

## Owned connection prerequisite

Authenticated connection ownership now sends CONNECTED with version 1.2 and
the `1000,1000` heartbeat offer. The negotiated send direction schedules EOL
heartbeats; zero disables it. A single mutex serializes heartbeat/application
writes under fixed three-second deadlines. A terminal RECEIPT or ERROR prevents
all later frames. Cancellation closes the socket and joins the callback and
heartbeat writer, including a blocked TLS write.

Reads wait for the first byte under a 30-second idle bound, or twice the
negotiated receive interval when enabled. After the first byte, one fixed
three-second deadline covers the whole frame. The caller's earlier operation
deadline bounds both waits, so heartbeat EOLs cannot prolong a pending result.
The buffered reader remains owned across reads. Listener socket/principal
admission and signed dispatch are separate unfinished prerequisites; the
connection owner alone does not authorize native work.

On 2026-10-07 (Europe/London), affected race suites passed: adapter 49.245s,
codec 2.210s, auth 2.628s and config 1.849s. Vet, staticcheck, formatting and
diff checks passed. Focused TLS connection controls passed in 8.195s, covering
heartbeat/zero negotiation, terminal ordering, blocked-writer shutdown,
application framing, receive-idle expiry, partial/trickled frames and heartbeat
traffic under a fixed operation deadline. No service/native qualification is
claimed by this connection checkpoint.

## Listener admission prerequisite

The listener now owns the service's shared socket slots directly, with no second
acquisition by a wrapper. The global bound includes incomplete TLS/CONNECT
negotiation. After authentication, a principal claim is reserved before
CONNECTED is sent; excess principal sessions receive ERROR and close. Claim
release follows joined socket/heartbeat cleanup. Shutdown cancels and joins
all accepted connections, preserving slots held by other transports. An accept
failure also cancels and joins existing owners. Route slices are copied into
the owner.

At this checkpoint, the listener accepts heartbeat EOLs and orderly DISCONNECT,
with an optional bounded receipt and no later frames. Other commands receive
ERROR and close. SEND dispatch remains unfinished; enabled STOMP is still
refused by service configuration. This is admission evidence, not ingestion
or independent native acceptance.

On 2026-10-07 (Europe/London), affected race suites passed: adapter 49.993s,
codec 1.226s, auth 2.189s and config 2.112s. Focused STOMP race controls passed
in 11.427s. Vet, staticcheck, formatting and diff checks passed. Real TLS clients
verify shared/foreign-slot accounting, principal refusal before CONNECTED,
principal-slot reuse, accept/read failures, incomplete authentication shutdown,
orderly receipt/EOF and no frames after terminal ERROR.

## Signed request admission prerequisite

Complete SEND bodies now admit compact signed JWS intent under the fixed route.
The signed principal and binding must equal the authenticated TLS session's
identity; target and operation must fit the exact route grant. The selected
operations remain Send, ContextRead, HistoryRead and Search. The shared request
slot is acquired only after authority checks, released on every outcome, and
durable intake/read work has a three-second deadline shortened by cancellation
or an earlier parent deadline. Successful sends require a durable dispatcher
result. The signed request ID owns replay; receipt IDs never alter stored intent.

Supported SEND headers are `destination`, `content-type`, `content-length`,
`receipt`, `reply-to` and `user-id`. Destination must equal the fixed request
destination. Optional publisher/reply metadata must match the principal and
response grant; content type, when present, must be `application/jose`. Receipt
values are 1-255 bytes. Unknown headers and transaction extensions are refused.
Framing supplies first-header semantics and complete content-length validation.
An authenticated failure retains the signed request ID for the future response;
pre-authority refusals expose no authorized response scope.

On 2026-10-07 (Europe/London), affected race suites passed: adapter 54.616s,
codec 2.351s, dispatcher 2.134s, auth 3.303s and config 1.790s. Vet,
staticcheck, formatting and diff checks passed. Controls reopen the real journal
and replay identical signed intent under changed wire receipt IDs, preserve the
original job on conflict, and leave no job after authentication, route, metadata,
capacity or cancellation refusals. Earlier parent deadlines terminate and join
real withheld native Context/History/Search read attempts. This helper is not yet
connected to SEND receipts or signed response delivery; service/native
qualification remains unfinished.

## Signed result prerequisite

STOMP results use compact EdDSA JWS with the existing gateway response envelope,
provisioned signing key, exact realm/principal/binding and a domain-separated
hash of the ordered request/response destination pair. The signed payload has
version 1, protocol `stomp/1.2`, the stable request ID, both destinations and
exactly one result or failure. Wire message/acknowledgement IDs remain outside
this authority. The envelope lifetime is 120 seconds with the existing 60-second
clock-skew allowance. The complete signed token, including envelope overhead,
must fit the 32,768-byte STOMP body limit.

On 2026-10-07 (Europe/London), affected race suites passed: adapter 53.107s,
auth 2.057s and codec 1.517s. Vet, staticcheck, formatting and diff checks
passed. Controls verify signature opening, stable ID and durable-result/failure
shape, exact destination ordering, principal/binding/realm mismatch refusal,
cross-protocol scope refusal, expiry beyond clock skew and signed-token capacity.
Subscription dispatch, receipt/acknowledgement retention and service/native
qualification remain unfinished; this helper does not enable STOMP ingestion.
