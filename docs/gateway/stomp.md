# STOMP 1.2 ingress

The selected STOMP 1.2 profile is implemented, plain or over TLS 1.3. Remaining
modes and target decisions live in [ROADMAP.md](ROADMAP.md). The profile
follows the [STOMP 1.2 specification](https://stomp.github.io/stomp-specification-1.2.html).

## Authors and access

As on IRC, a client needs no TLS and no credential. TLS is optional
(`tls_cert_file`/`tls_key_file`), and the listener access policy applies
(`password_file`, `guests`, `targets`, `limits`). CONNECT (or STOMP) must offer
version 1.2; any `host` is accepted, and `login` is ignored. The `passcode` is
the listener access password when the listener has a `password_file`; it is
never an identity. A refused CONNECT (wrong passcode, unsupported version,
malformed heart-beat) is answered with an ERROR frame naming the reason before
the connection closes.

Each SEND body is a plain [contract v1](contract-v1.md) request, the same bytes
as every carrier, naming its author (`"author":{"nick":...,"token":...}`); a
nickname alone posts as an AIRC guest. A SEND may instead carry
`account.identify`: its author is then the connection's default until it
closes. Tokens cross a plaintext connection in the clear.

```json
{"version":1,"request_id":"r1","operation":"send","target":{"kind":"room","name":"#probe"},"message":"hello","author":{"nick":"alice","token":"..."}}
```

```json
{"id": "stomp", "protocol": "stomp", "enabled": true, "address": "0.0.0.0:61613"}
```

With no `stomp.routes` the listener takes SENDs to `airc.requests` and
answers on `airc.responses`. A route (`request_destination`,
`response_destination`, optional `operations`) picks other destinations. The
SEND `destination` selects a route, which does not pin a room: the request
names its own target, limited by the listener's `targets`, and one of the
route's operations (send, context, history or search; all of them when the
route lists none). `account.identify` and `capabilities.get` name no target
and use any route. A request outside them, or refused by dispatch, gets a terminal ERROR
whose body is the plain JSON failure (for example `forbidden`), with receipt
correlation and no success RECEIPT. `reply-to`, if present, must be the
route's response destination; `content-type` may be `application/json` or
`text/plain`; `transaction` is refused; other headers (such as `user-id`) are
ignored.

A SUBSCRIBE to the route's response destination (`ack:client-individual`)
returns one MESSAGE with the plain JSON result per request; one pending result
bounds storage. A SEND without a subscription is answered by its receipt only
(after durable intake for a mutation). Receipts and results establish intake,
not native AIRC acceptance; lost ones recover by resubmitting the identical
request, on any carrier.

On 2026-10-11 (Linux AMD64), adapter, service and integration tests cover
plain CONNECT with any host, the passcode as listener password with ERROR on a
wrong one, SEND with and without a subscription, refusals with correlated
failures, and an `account.identify` followed by an authorless SEND posting as
the account against a current-tree `aircd`. Against a scratch `aircd`, stomp.py
9.0 (`vhost` any) posted as a guest over plaintext TCP without subscribing,
identified as a registered account and posted without an author, and posted
over TLS with the listener passcode; a wrong passcode received ERROR `access
password refused`. An independent `airc history` client saw each post under its
author.

The sections below are dated development records. Their principal, grant,
signing and fixed-target statements describe the
[removed identity model](removed-identity.md) and no longer apply.

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

The `stomp` listener options contain 1-128 `routes` (the default route when
omitted). Each route has `request_destination`, `response_destination` and
optional `operations`; the checkpoint below also had `principal` and `target`.
Destinations are distinct literal ASCII strings of 1-255 bytes; wildcard syntax,
whitespace, controls and cross-route request/response collisions are refused.
The allowed operations are Send, ContextRead, HistoryRead and Search.
Every listener operation must have a route grant, and each grant must fit both
listener and principal permissions for the exact target. Principals require an
active bearer credential. (At this checkpoint signing credentials and a response
signer were also required; the plain request model removed both requirements.)
History/Search also require a cursor key. TLS is mandatory and origins are not
accepted. At the configuration checkpoint, enabled STOMP remained rejected
until the adapter was wired into the service.

On 2026-10-07 (Europe/London), the affected configuration race suite passed in
1.371s; vet, staticcheck, formatting and diff checks passed. Controls exercise
revocation, missing credentials, target/operation mismatches, grant collisions,
route/destination bounds, disjoint operation grants, send-only cursor exemption
and the JSON field contract. These configuration controls supplied no native
qualification by themselves; integrated evidence is recorded below.

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
10,000. The server offer is `1000,1000`; negotiation takes the maximum
for each enabled direction and zero disables that direction. Scheduling,
postauthentication deadlines and joined connection ownership are implemented by the connection owner described below.

On 2026-10-07 (Europe/London), affected race suites passed: adapter 40.239s,
codec 1.197s, auth 2.514s and config 1.829s. Vet, staticcheck, formatting and
diff checks passed. TLS controls verify CONNECT/STOMP identity and buffered
pipeline preservation, TLS 1.2/cleartext refusal, joined cancellation and idle,
partial-frame and repeated-heartbeat deadline bounds. Authority controls cover
realm, version, login/bearer mismatch, route permission and heartbeat grammar.
These authentication controls supplied no service/native qualification by
themselves; integrated evidence is recorded below.

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
admission and signed dispatch were separate prerequisites at this checkpoint;
the connection owner alone does not authorize native work.

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

At the listener admission checkpoint, the adapter accepted heartbeat EOLs and
orderly DISCONNECT, with an optional bounded receipt and no later frames. Other
commands received ERROR and closed. SEND dispatch was unfinished and enabled
STOMP was still refused by service configuration. This checkpoint supplies
admission evidence; integrated ingestion/native evidence is recorded below.

On 2026-10-07 (Europe/London), affected race suites passed: adapter 49.993s,
codec 1.226s, auth 2.189s and config 2.112s. Focused STOMP race controls passed
in 11.427s. Vet, staticcheck, formatting and diff checks passed. Real TLS clients
verify shared/foreign-slot accounting, principal refusal before CONNECTED,
principal-slot reuse, accept/read failures, incomplete authentication shutdown,
orderly receipt/EOF and no frames after terminal ERROR.

## Signed request admission prerequisite (superseded)

Superseded on 2026-10-10: SEND bodies are now plain contract JSON attributed to
the login principal; see the plain request model.

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
An authenticated failure preserves the signed request ID in its terminal ERROR;
pre-authority refusals expose no authorized response scope.

On 2026-10-07 (Europe/London), affected race suites passed: adapter 54.616s,
codec 2.351s, dispatcher 2.134s, auth 3.303s and config 1.790s. Vet,
staticcheck, formatting and diff checks passed. Controls reopen the real journal
and replay identical signed intent under changed wire receipt IDs, preserve the
original job on conflict, and leave no job after authentication, route, metadata,
capacity or cancellation refusals. Earlier parent deadlines terminate and join
real withheld native Context/History/Search read attempts. At that checkpoint,
the helper was not yet connected to SEND receipts or signed response delivery.
The helper alone supplied no service/native qualification.

## Signed result prerequisite (superseded)

Superseded on 2026-10-10: results are now plain JSON; see the plain request model.

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
At that helper checkpoint, subscription dispatch, receipt/acknowledgement
retention and service/native qualification were unfinished. The helper alone
did not enable STOMP ingestion.

## Subscription dispatch checkpoint

The adapter now dispatches SEND only through one active, exact-principal fixed
response subscription per connection. SUBSCRIBE requires a 1-255-byte ID and
`ack:client-individual`; duplicate, foreign, cumulative and extension grants are
refused. UNSUBSCRIBE requires the active ID and no pending result. Signed request
admission completes before any SEND receipt or response. A successfully processed
request produces a signed result. Authenticated dispatch failures and oversize
signed results produce a terminal signed ERROR carrying the same authenticated
request ID, with no success receipt.

One MESSAGE body remains retained until its matching individual ACK. No second
SEND or unsubscribe is admitted while that response is pending. Fresh random
message/acknowledgement IDs identify each wire response; they do not replace the
signed request ID. NACK closes with ERROR and directs the client to replay its
signed request. Disconnect or response loss likewise recovers by replay. Result
retention has one absolute 30-second deadline, enforced for buffered input as
well as socket reads/writes; heartbeat traffic cannot renew it. Shutdown closes
and joins the existing socket and heartbeat owners.

On 2026-10-07 (Europe/London), the final affected adapter race suite passed in
87.309s. Unchanged auth, dispatcher and codec suites passed in 1.833s, 2.317s
and 2.001s. Vet, staticcheck, formatting and diff checks passed. Real TLS controls
verify durable journal state before receipts, individually acknowledged capacity
release, stable replay after unacknowledged responses, fresh reconnect wire IDs,
subscription/pending/NACK refusals, terminal frame ordering and the production
30-second deadline under continuous heartbeat traffic. The prior broad rerun's
standalone authentication fixture cleanup failure and its separate repair are
recorded in [testing.md](testing.md#stomp-preauthentication-fixture-ownership).
At the subscription checkpoint, STOMP was still disabled in service configuration.
Service/native qualification remained the next roadmap item.

Subagent review of the dispatch checkpoint identified that authenticated intake
failures still received a processing RECEIPT. The profile now reserves SEND
RECEIPT for successful processing: dispatch failures and response-capacity
failures produce only a terminal signed ERROR with optional `receipt-id`
correlation. The signed body preserves exact scope, stable request ID and failure
code/transience. No later response or heartbeat follows ERROR. A missing success
receipt can still follow committed intake (for example result serialization
capacity or connection loss); replay remains the recovery mechanism.
Real TLS capacity/conflict controls prove ERROR is the first response, no success
RECEIPT is emitted, capacity preserves another request-slot owner's claim and
leaves no job, and conflict preserves the original job. Focused race controls
passed in 1.986s; the final affected adapter race suite passed in 88.233s. Vet,
staticcheck, formatting and diff checks passed. The helper alone supplied no
service/native qualification; integrated evidence is recorded below.

## Service and native evidence

STOMP service wiring now uses direct shared socket admission, TLS 1.3 without
framed ALPN, the provisioned response signer and the listener's bounded dispatcher.
The signer loader is shared in place with AMQP; the service owns shutdown of both
adapters before closing the journal. Configuration enables only the selected
fixed-route operations, and CLI discovery reports the configured partial STOMP
surface without starting runtime or exposing credential paths.

The independent native client implements framing separately from the gateway
codec. It verifies exact author, target and Unicode body through a pinned native
AIRC observer; compares whole one-record History/Search pages and their explicit
more/caught-up cursor boundaries against native records; checks bounded Context;
and loses both receipt and response before reconnecting with identical signed
mutation intent. Native receipts are fenced before deliberate shutdown. Journal
reopen and service restart preserve native message IDs and one send attempt with
no duplicate native posting. A locked journal proves no success receipt precedes
committed intake. Refusal controls leave no job or native message after TLS,
CONNECT, signed-authority, publisher/reply, transaction, type or size violations.
Negotiated heartbeat EOL and terminal DISCONNECT receipt are observed directly.
Closure checks require EOF/reset and reject timeout or any extra byte.

Corrected Darwin race native controls passed all 20 tests (four top-level and
16 subtests), with no skips/failures, in 14.594s. The gateway executable SHA256 was
`48760f673ed1892d1783fd53017d5982d4b0061fbf378b464402bebfe81c24de`.
Service/configuration/CLI STOMP and unchanged AMQP race controls passed in 1.982s,
1.360s and 2.037s respectively.

The frozen tracked Go/test/module source SHA256 is
`e98747028c61da8c683e5569625f6d67b8735f5ca5d4c09ea2ca4ec2dbdf13da`.
Linux ARM64 qualification used the existing Ubuntu 24.04 image
`33ceb71981b602c1a7443a53469e4dba065f7503eab3078a2d7a57a2ab987517`,
with network disabled, read-only root/fixtures and disposable disk state.
All 20 corrected native controls passed without skips/failures in 3.160s.
Linux gateway SHA256 was
`2d58ccbc944076d07b964029fc14bd9ea497ed76cf2b46e665c93b9550414267`;
test binary SHA256 was
`5edd9dde8e346dcacac219c037b73b7bb888f929de1997ae8fa007acc6a6a06f`;
native fixture SHA256 was
`1f3aa4a566a23d4326e2a2b3ed81942b5384e17521dc1bf38572511778d7e85f`.
The pinned native baseline/archive are `96f16c3bbb8a3faac2e69684f591af63ceb58450`
and `4f25ee66941bd79c29f936b8deabc120dc0451e5d1b747afee4bbdf593b32829`.
Linux evidence has no race instrumentation. Full Darwin normal and race
acceptance each passed with 3,036 passes, 53 opt-in/platform skips and no failures,
across 37 packages (29 passed, eight with no tests). Build, formatting, vet,
staticcheck and diff gates passed on this unchanged source. Exact terminal
results and the superseded preliminary run are recorded in
[testing.md](testing.md#stomp-integration-checkpoint). Qualification covers this
fixed-route loopback profile; arbitrary broker semantics remain outside it.
