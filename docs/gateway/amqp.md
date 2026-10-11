# AMQP 1.0 ingress

The selected AMQP 1.0 service profile is implemented, plain or over TLS 1.3:
optional SASL, fixed link addresses, bounded Flow/multipart accounting, plain
request admission and results, durable dispatch and receiver settlement.
Remaining connection modes live in [ROADMAP.md](ROADMAP.md). No arbitrary
broker forwarding, dynamic nodes, transactional sessions or link recovery is
provided.

## Current service profile

As on IRC, a client needs no TLS and no credential. TLS is optional
(`tls_cert_file`/`tls_key_file`) and the listener access policy applies
(`password_file`, `guests`, `targets`, `limits`). The server reads the client's
protocol header first. A client may skip SASL when the listener has no
password. Over SASL the server offers ANONYMOUS (only without a password) and
PLAIN, whose password is the listener access password; the user name and
authorization identity are ignored. SASL is never an identity.

```json
{"id": "amqp", "protocol": "amqp", "enabled": true, "address": "0.0.0.0:5672"}
```

With no `amqp.routes` the listener reads link address `airc.requests` and
answers on `airc.responses`. Routes pick other address pairs:

```json
"amqp": {"routes": [{
  "request_address": "airc/request",
  "response_address": "airc/response",
  "operations": ["send", "context.read", "history.read", "search"]
}]}
```

History and search need a cursor key. Each message body is a plain
[contract v1](contract-v1.md) request, the same bytes as every carrier, naming
its author; a nickname alone posts as an AIRC guest, `"token"` as the
registered account (in the clear without TLS). An `account.identify` request
makes its author the connection's default until it closes. The body may be one
or more data sections or one amqp-value string or binary (Qpid Proton's default
`Message(body="...")`). `to` and `reply-to`, if present, must be the route's
addresses; content type may be `application/json` or `text/plain`; `user-id`
is ignored.

A route does not pin a room: each request names its own target, limited by
the listener's `targets`. A route's optional `operations` narrow the
listener's; `account.identify` and `capabilities.get` use any route. A request
outside them, or refused by dispatch, gets a correlated failure: disposition Rejected
(`amqp:precondition-failed`, with the failure code as the error description) or
Released for a transient failure, and a plain failure result. Undecodable
bodies and metadata violations detach the link. Accepted means durable intake
for sends, not native acceptance.

The response link is optional. A sender-only client is granted request credit
at once and learns each outcome from its disposition; with a response link a
plain JSON `AMQPResult` is retained until receiver settlement. Any sender
settlement mode is accepted: a pre-settled transfer is admitted at most once,
without a disposition. A Flow sent before the client saw the server's Begin
may omit next-incoming-id (AMQP 1.0 section 2.7.4).

```json
{"version":1,"protocol":"amqp/1","request_id":"r1","request_address":"airc/request","response_address":"airc/response","result":{"version":1,"operation":"send","result":{"request_id":"r1","state":"queued","durable":true}}}
```

On 2026-10-11 (Linux AMD64), adapter tests cover the header-first exchange,
no-SASL and ANONYMOUS admission, PLAIN with the listener password (initial and
challenge), refusals, the Flow without next-incoming-id, sender-only credit with
dispositions carrying failure codes, settlement modes, amqp-value bodies and
guest and registered authors; integration tests repeat durable dispositions,
native delivery and replay, and a plaintext no-SASL sender-only connection
against a current-tree `aircd`. Against a scratch `aircd`, Qpid Proton 0.40 with
its default patterns (an `amqp://` sender only, and a sender plus receiver)
posted as a guest and as a registered account with no TLS and no credentials,
and over TLS with the listener password; a wrong password failed SASL. An
independent `airc history` client saw each post under its author.

The sections below are dated development records. Their principal, grant,
signing and fixed-target statements describe the
[removed identity model](removed-identity.md) and no longer apply.

## Frame contract

The codec follows OASIS AMQP 1.0 [transport framing, section 2.3](https://docs.oasis-open.org/amqp/core/v1.0/amqp-core-transport-v1.0.html)
and [SASL framing, section 5.3.1](https://docs.oasis-open.org/amqp/core/v1.0/amqp-core-security-v1.0.html).
It reads one frame without consuming the next. Declared total size and data
offset are checked before body allocation. The caller supplies an inbound limit
between the initial 512 bytes and the implementation ceiling of 65,536 bytes;
negotiation must keep the initial bound until permitted by the connection state.
Extended headers are ignored. AMQP channel numbers are retained; SASL reserved
header bytes are ignored, its body must be nonempty and its ceiling stays 512.

Writes use the peer's frame limit, omit extended headers, zero SASL reserved
bytes and report partial writes. Callers must provide deadlines, protocol-header
negotiation, frame-type/state restrictions and body validation. Empty AMQP bodies
are representable for heartbeat handling; the future session layer must enforce
when they are allowed. Framing does not authenticate or acknowledge intake.

## Frame verification

On 2026-10-06, Darwin ARM64 affected race tests passed in 1.309s. Two-second
fuzzing with three workers exercised 318,683 inputs without failure. Vet,
staticcheck and diff checks passed. Independent byte vectors cover extended
headers, reserved SASL bytes, heartbeats, every truncated prefix, adjacent frames,
short reads/writes, exact limits and oversized/malformed headers rejected before
body reads. This is codec evidence only; no AMQP interoperability, durable
disposition, native service or Linux runtime acceptance is claimed.

## Protocol-field decoding

The decoder follows the [OASIS type encodings](https://docs.oasis-open.org/amqp/core/v1.0/amqp-core-types-v1.0.html)
for null, boolean, unsigned integers, timestamps, UUIDs, binary, UTF-8 strings, ASCII symbols,
lists, maps, symbol arrays and symbolic/numeric described values. Alternate
encodings normalize while retaining AMQP type distinctions. Reserved descriptor
types, other primitive types and non-symbol arrays are outside this protocol-field
profile. Map keys must be distinct non-null supported scalars; field-specific
key types and performative restrictions belong to subsequent validation.

Input is limited to 65,536 bytes, depth to 16, collection entries to 1,024 and
the complete tree to 2,048 nodes. Counts and lengths are checked before storage
allocation. Collection payloads must be consumed exactly; the top-level decoder
returns the consumed length so the caller can validate trailing data. Binary and
text values own their bytes. Strings reject invalid UTF-8 and a leading BOM;
symbols reject non-ASCII octets.

On 2026-10-06, final affected Darwin race tests passed in 1.294s; two-second
fuzzing with three workers exercised 310,081 inputs without failure. Vet,
staticcheck and diff checks passed. Independent vectors cover alternate integer/string/collection encodings,
descriptors, truncated scalars, copied ownership, invalid text/BOM, malformed
sizes/counts, map keys and exact depth/count/total-node boundaries. Review caught
the BOM requirement before commit. This does not qualify session state,
authentication, message sections or AMQP/native interoperability.

## Protocol-field encoding

The encoder accepts the same canonical value profile as the decoder. It validates
field shapes, integer ranges, text, descriptors, map keys and the complete tree
before allocating output. A caller-supplied byte limit includes the whole value;
it cannot exceed 65,536 bytes. Depth, collection and total-node limits match the
decoder. Cyclic caller-created trees terminate at those bounds. Caller-owned
values must remain unchanged during encoding.

Unsigned fields use their full-width encodings; binary/text, collections and
symbol arrays use 32-bit lengths. Symbol arrays share one symbol32 constructor.
These valid encodings may be larger than compact input forms, so encoding can
refuse a decoded value that cannot fit the outbound byte budget. An enclosing
frame and negotiated peer limit remain the session layer's responsibility.

On 2026-10-06, affected Darwin race tests passed in 1.293s. Two-second semantic
decode/encode/decode fuzzing with three workers exercised 273,212 inputs without
failure. Independent literal wire vectors separately check encoding correctness;
tests cover exact byte/tree limits, unsupported shapes, map duplicates, cycles,
ownership and invalid text. Vet, staticcheck and diff checks passed. This remains
wire primitive evidence; performatives and service/native
acceptance are outstanding.

## Data-message sections

Format-0 messages follow [OASIS message sections](https://docs.oasis-open.org/amqp/core/v1.0/amqp-core-messaging-v1.0.html).
The selected envelope profile uses one or more data sections. Section order,
singleton metadata, descriptors and field types are checked. Header, properties,
annotations, application properties and footer are optional; other body formats
are refused. The reserved rejected delivery annotation requires a typed error.

The decoder joins data bytes into an owned 1–32,768-byte body. At most 32 sections
and 65,536 encoded bytes are permitted, with one shared 2,048-node budget across
all sections. Header/properties remain separate from the request body. UUIDs use
16 bytes; timestamp values retain signed 64-bit wire bits without interpreting
wall time. A response encoder emits one bounded data section.

The session must enforce metadata TTL/absolute expiry, address/publisher policy
and supported content encoding. Transport metadata cannot replace the
connection identity, replay identity or target authority. This terminal ingress
does not forward annotation metadata; no relay preservation claim is made.

On 2026-10-06, final affected Darwin race tests passed in 1.391s. Independent vectors
cover section order/cardinality, metadata types, symbolic descriptors, multiple
data sections, UUID/timestamp bytes, truncation and exact body/section limits.
Additional controls verify the shared node budget, aggregate body bound and
reserved error annotation. Final message fuzzing exercised 189,984 inputs;
extended type decode/encode fuzzing exercised 263,702 inputs, each for two seconds
with three workers. Vet, staticcheck and diff checks passed. Service and native
AMQP qualification remain open.

## SASL performatives

The codec implements the five [OASIS SASL wire messages](https://docs.oasis-open.org/amqp/core/v1.0/amqp-core-security-v1.0.html):
mechanisms, init, challenge, response and outcome. Numeric and symbolic descriptors
are accepted. Mandatory fields, types, optional nulls, field counts and outcome
codes are validated, including omission of additional data on unsuccessful outcomes.
A mechanisms field accepts a single symbol or a nonempty symbol array, following
the composite multiple-field rule. Exactly one performative fills each SASL body;
empty/trailing bodies and bodies above 504 bytes are refused. Encoded frames use
zero reserved bytes and the fixed 512-byte ceiling.

TLS, mechanism-name policy, offered/selected mechanism matching, negotiation order
and credential verification remain caller responsibilities. Parsed credentials
are owned bytes and must be cleared when their owner finishes. These primitives
do not establish an authenticated peer or authorize any AIRC operation.

On 2026-10-06, affected Darwin race tests passed in 1.378s. Two-second fuzzing
with three workers exercised 224,820 inputs without failure. Independent vectors
cover all five types, single/array mechanisms, symbolic descriptors, truncation,
trailing values, required/type/refusal boundaries, optional holes and the exact
outbound frame limit. Vet, staticcheck and diff checks passed. Authentication,
session/service and native qualification remain outstanding.

## Transport performatives and termini

The codec covers the nine [OASIS transport performatives](https://docs.oasis-open.org/amqp/core/v1.0/amqp-core-transport-v1.0.html):
Open, Begin, Attach, Flow, Transfer, Disposition, Detach, End and Close. It checks
required/type fields, settlement ranges, sender initial delivery count, Open's
channel/frame-size constraints and transfer delivery-tag limits. Opaque payload
is permitted only after Transfer and is owned on decode/encode. Empty AMQP
frames are represented separately as heartbeats. Format-0 transfer messages and
non-transactional delivery states form the selected profile.

Nested source/target fields, durability/expiry choices, dynamic-property presence,
field-map keys, unsettled tags, delivery states and typed errors are validated.
A source default outcome cannot be the non-terminal Received state. Terminus
addresses, requested filters/dynamic nodes, role-dependent rules and requested
durability are retained for session negotiation; parsing does not agree to them.

The session must enforce connection order, negotiated channels/handles, frame and
message limits, serial arithmetic, credit, multipart transfer consistency,
mechanisms, authority, durable intake and disposition ordering. Codec evidence
does not establish those guarantees or AIRC acceptance.

On 2026-10-06, affected Darwin race tests passed in 1.368s. Two-second fuzzing with
three workers exercised 261,558 inputs without failure. Independent byte vectors
cover all nine performatives, symbolic descriptors, truncation, transfer/non-transfer
payload boundaries, owned bytes and exact outbound limits. Additional tests cover
mandatory/range refusals, nested termini/states, fields/unsettled map keys and
default outcomes. Vet, staticcheck and diff checks passed. Service/native
qualification remains outstanding.

## Disabled listener authority configuration

The `amqp` listener requires TLS certificate/key paths and `amqp.routes` with
1-128 grants. Each grant specifies `principal`, `request_address`,
`response_address`, `target` and `operations`. Addresses are distinct across
both directions and all routes, 1-255 ASCII bytes from letters, digits,
`/`, `-`, `_`, `.`, `:`. This selected fixed-address profile excludes wildcard
and dynamic address syntax. Targets must exactly match an existing normalized
principal target; grants require an active bearer principal. (At this checkpoint
an envelope-signing key and a response signing key were also required; the plain
request model removed both requirements.)

The initial operation profile is `send`, `context.read`, `history.read` and
`search`. Each route operation must be allowed by both principal and listener;
each listener operation must have a route grant. History/search requires the
existing cursor key. Every route has a response address and the configuration
requires a response signing key, including send-only routes. The planned
response link returns bounded operation results; it does not confer authority
on another principal or authorize arbitrary broker forwarding.

Parsing opens no sockets and reads no credential files. Enabled AMQP is still
refused because session handling is not yet wired. Runtime TLS/SASL and fixed link authority are implemented below. Signed-request
target/operation enforcement, response delivery and service qualification remain
implementation and qualification work. On 2026-10-06, configuration race tests
passed in 1.335s; vet, staticcheck and diff checks passed. Tests exercise authority
refusals, address collisions, route bounds, disjoint send/read grants and unwired
enablement. Commit review found no actionable issues.

## Protocol-header exchange

The server helper advertises its sole supported eight-byte header before reading
exactly one peer header, as permitted by OASIS transport section 2.2. The caller
selects protocol ID 3 for SASL or 0 for AMQP, with version 1.0.0 only. Invalid
magic, security layer or version receives the supported header and is refused;
truncation and write failures also terminate negotiation. No trailing frame byte
is consumed. The caller must close on error and supply deadlines; this helper
does not perform TLS or authenticate. The pure TLS adapter exchanges SASL
headers after TLS, and AMQP headers only after successful SASL.

On 2026-10-06, affected race tests passed in 1.293s; two-second fuzzing with three
workers exercised 414,607 inputs without failure. Tests cover both independent
header vectors, every altered byte and truncated prefix, trailing-byte retention,
short/error writes and unsupported local protocol IDs. Vet, staticcheck and diff
checks passed. Fuzzing initially could not create its sandboxed cache directory;
the approved cache-access rerun completed successfully. Commit review found no
actionable issues.

## TLS/SASL authentication phase

The unwired adapter helper requires a TLS connection negotiated at TLS 1.3,
exchanges SASL headers and advertises only PLAIN. The credential response follows
[RFC 4616](https://www.rfc-editor.org/rfc/rfc4616.html): NUL-separated
authorization identity, authentication identity and password. The password is
the existing 64-character bearer token; authentication identity must exactly
equal its provisioned principal. Authorization identity is either empty or that
same principal, with no delegated identity. A principal must also have a route
in this listener. Credentials are not echoed in refusal frames. The optional
SASL hostname is a transport hint, not an authority selector.

Both an initial response and one empty challenge followed by a response are
supported. Malformed/out-of-order frames terminate authentication. Valid but
unauthorized attempts receive the authentication-failed outcome. Successful
authentication sends the success outcome, then exchanges AMQP headers and leaves
the stream at its first AMQP frame. The phase has one three-second deadline
covering TLS and both header/SASL exchanges. Cancellation closes the underlying
transport and joins its close callback. Success detaches that callback before
ending the phase context, preserving the socket for subsequent negotiation.
Decoded credential buffers are cleared after use.

The listener owner must close on error, set subsequent deadlines, enforce
connection/session order, own and join the whole socket lifetime, and recheck
link and signed-request authority. No dispatcher is called by this helper;
configuration still refuses enabled AMQP. Authentication tests do not qualify
a listening service or native AIRC delivery.

On 2026-10-06, affected adapter race tests passed in 1.846s. Broader auth, config
and AMQP codec race suites passed in 2.023s, 1.385s and 1.186s respectively. Vet,
staticcheck and diff checks passed. Real TLS socket-pair tests cover initial and
challenge success, a usable stream after return, credential/principal/authz/route
and mechanism refusals, TLS 1.2 and cleartext refusal, SASL bypass, out-of-order
frames and cancellation at five exchange stages. Commit review found no
actionable issues.

## Open/Begin negotiation primitive

After authentication and header exchange, the bounded helper reads Open under
the initial 512-byte frame limit, then responds with its caller-supplied
container ID, maximum frame size 65,536, channel maximum zero and idle timeout
15,000ms. The owner must supply a unique container ID and enforce the actual
30-second receive timeout. Peer container IDs are bounded to 128 UTF-8 bytes;
container/hostname fields confer no identity or authority. Nonzero peer idle
timeouts below 2,000ms are refused; runtime heartbeat cadence is one second,
as implemented by the connection owner below.

The helper accepts a remotely initiated Begin on channel zero, retaining the
peer's initial next-outgoing ID and windows. It responds with remote-channel
zero, initial next-outgoing ID one, windows eight and handle maximum one.
Returned peer frame/handle limits apply to outgoing frames/handles; advertised
local limits apply to incoming frames/handles. Defaults and larger peer limits
are capped to the selected profile. Heartbeats after Open are accepted while
waiting for Begin; caller deadlines must bound that wait. No link or request
is accepted, and subsequent flow/transfer code must maintain serial counters.

On 2026-10-06, affected race tests passed in 1.306s; vet, staticcheck and diff
checks passed. Tests cover independently spelled Open/Begin bytes, initial
state, server responses, omitted defaults, heartbeats, wrong channel/order,
mapped Begin, truncation, short writes without consuming Begin and invalid local
container IDs before transport I/O. Commit review found no actionable issues.
Joined socket ownership is implemented below; service qualification remains outstanding.

## Negotiated socket ownership primitive

The unwired connection constructor now composes TLS/SASL authentication and
Open/Begin negotiation. A separate three-second deadline bounds Open/Begin.
After negotiation, one reader owns incoming frames with a whole-frame
30-second deadline, channel zero and the advertised 65,536-byte maximum.
A partial-frame timeout terminates the read; callers must close on any read or
write error rather than resume from residual bytes.

Outgoing protocol frames and one-second heartbeats share a mutex, preventing
interleaved bytes. Every write has a three-second deadline and uses the peer's
negotiated frame limit. Parent cancellation closes the underlying TLS transport
to interrupt blocked reads/writes. Idempotent close joins the cancellation
callback and heartbeat writer. No per-frame goroutine or unbounded write queue
is introduced. The listener must bound accepted sockets, close every
returned connection on all paths, and join all connection owners at shutdown.

On 2026-10-06, affected adapter race tests passed in 3.912s; vet, staticcheck and
diff checks passed. Real TLS socket-pair tests compose the full constructor,
observe a valid heartbeat, prove the returned stream usable, cancel a blocked
read, repeat close, and cancel while the heartbeat holds its write critical
section with an unread peer. Concurrent End/Close writes decode as separate
whole frames. Commit review found no actionable issues. Credit/transfer handling, signed
dispatch and native service acceptance remain outstanding; enabled
AMQP configuration is still refused.

## Bounded listener owner

The unwired `ServeAMQP` adapter now acquires the service's shared socket slots
before creating a connection goroutine; capacity is 1-256 and overflow sockets
are closed. Its TLS listener must not also acquire the same slots. After bounded
negotiation, the configured per-principal limit bounds retained sessions.
Handshake/negotiation work remains under the global socket budget. Principal
and global capacity are released only after connection cleanup joins its
callback and heartbeat writer. Options are snapshotted and each listener owner
generates a random 128-bit container suffix.

Cancellation closes the listener, cancels accepted owners and joins all of them;
an unexpected Accept failure also performs that cleanup before returning its
error. Peer Close is acknowledged, and End is followed by bounded connection
shutdown for this single-session profile. Repeated Open/Begin is refused with
illegal-state; channel/framing errors receive framing-error. Transfer work
is still unsupported and refused, with no dispatcher or durable intake claim.

A closing-state flag shares the write mutex. Once Close starts, later frames,
including heartbeats, are suppressed. Locally initiated Close waits up to three
seconds for the peer's Close, under one absolute deadline. The listener owner
closes on all read/write failure paths and joins every accepted goroutine.
Configuration still refuses enabled AMQP; integration and native AIRC acceptance
remain on the roadmap.

On 2026-10-06, final affected adapter race tests passed in 6.213s; broader auth,
config and AMQP codec race suites passed in 1.757s, 1.499s and 1.282s. Vet,
staticcheck and diff checks passed. Real TLS TCP tests cover shared capacity
without stealing another transport's slot, overflow, cancellation during
incomplete TLS negotiation, Accept/read failures, Close acknowledgement,
per-principal refusal with a fenced existing claim, channel-error shutdown and
no heartbeat or premature EOF while awaiting Close acknowledgement. Socket-pair
tests also prove heartbeat write failure cancels the owner and joins its writer.
Commit review found no actionable issues. This is adapter-level lifecycle
evidence; it does not qualify service activation or AIRC delivery.

## Fixed request/response link negotiation

Each connection may attach one request receiver and one response sender, both
bound to one configured route for the authenticated principal. The peer sender's
Target address selects the request grant; the peer receiver's Source address
selects the response grant. The counterpart must belong to the same grant, so
a read or result cannot cross into another route. Peer-owned source/target
addresses and names confer no AIRC identity or operation authority.

Input handles are 0-1 and cannot be reused while attached. Output handles are
assigned independently, lowest first, within the peer's negotiated maximum. The
initial request delivery-count is retained; the response sender starts at zero.
Outgoing message size retains the peer receiver's nonzero bound, capped at
65,536 bytes. The receiver advertises a 65,536-byte encoded-message ceiling;
the data-body codec separately limits the signed body to 32,768 bytes.

The profile uses unsettled senders and receiver-first settlement. An incoming
request sender must explicitly select unsettled; a response receiver may request
unsettled or mixed, with the actual server sender selecting unsettled. Only
volatile, static termini are supported, with no durable/dynamic nodes, nonzero
retention timeout, never-expiry, copy distribution, filters, default outcomes,
resumption maps or requested extension capabilities. Unsupported semantics are
refused rather than silently agreed to. Local termini advertise link-detach
expiry. A complete Attach reply must fit the peer's frame budget before any
link/route state is changed. Invalid addresses/principals, mixed grants, handle
reuse and insufficient handle/frame limits also leave state intact.

Attach responses grant no credit. Signed dispatch remains outstanding, and
enabled AMQP configuration is still refused.
Signed request target/operation authority must be rechecked during dispatch;
link negotiation alone does not authorize a message or acknowledge intake.

On 2026-10-06, affected adapter race tests passed in 6.314s; broader auth, config
and AMQP codec race suites passed in 1.986s, 1.735s and 1.287s. Vet, staticcheck
and diff checks passed. Tests cover independently spelled Attach bytes, both
link orders, independent handles, route/principal refusals, immutable state on
refusal, frame-budget boundaries, unsupported semantics, symbolic termini and
peer message limits. Real TLS TCP tests observe both fixed Attach replies and
unauthorized-address Close. Commit review found no actionable issues. Native
AIRC delivery and bounded-read qualification remain outstanding.


### Session and link Flow checkpoint

The disabled listener now processes codec-validated Flow on its sole session.
Session transfer counters and link delivery counters remain separate. Serial
credit subtraction clamps exhausted windows to zero, supports uint32 wraparound,
and refuses ahead acknowledgements or ambiguous half-space windows. No peer
window or credit allocates storage. Incoming request credit remains zero pending
the durable signed dispatcher. Response receiver credit is retained as scalar
state; empty-sender drain advances the delivery count and reports zero credit.
Echo reports the actual remaining response credit, uses the local handle and
never echoes the echo flag. Session-only Flow refuses link-specific fields;
unknown handles produce End with `amqp:session:unattached-handle`, then orderly
Close. Refusal leaves flow/link counters unchanged.

On 2026-10-06, adapter and codec race suites passed (35.218s and 1.602s),
then the final focused Flow/Credit race tests passed (1.602s). Tests cover credit
exhaustion, wraparound, ambiguous/ahead counters, echo, drain, unchanged refusal
state and real TLS echo/unknown-handle exchanges. Review identified an incorrect
echo credit report; it was corrected and the final review found no actionable
issues. Signed intake, service activation and native qualification remain
outstanding.


### Bounded multipart transfer accounting

The transport engine follows the [OASIS session and transfer rules, sections
2.5.6 and 2.7.5](https://docs.oasis-open.org/amqp/core/v1.0/os/amqp-core-transport-v1.0-os.html).
The request link owns at most one 65,536-byte encoded assembly, bounded to 256
frames and a fixed 30-second deadline from its first frame. Initial transfers
require delivery ID, binary tag and format zero. Continuations may omit these
fields or repeat identical values. Settled requests, receiver-second settlement,
resumption and sender delivery states are outside the selected profile.
Delivery IDs must advance within RFC-1982's unambiguous half-space, with
wraparound supported; they never substitute for a stable signed request ID.

Each accepted frame consumes one session window entry and advances the implicit
transfer counter; only the first consumes message credit and advances the link
counter. The peer's last-advertised outgoing estimate is decremented on every
frame and can become negative when the sender leaves its own window unchanged.
It does not grant or refuse capacity: our advertised incoming window controls
that boundary. A dispatcher-owned Flow can replenish eight frame entries after
bounded storage processing. Granting another request is refused while a partial
or completed delivery is retained. Failed Flow writes leave capacity unchanged.
An aborted transfer overrides `more`, ignores its payload, clears the assembly
and implicitly settles that wire delivery without an intake acknowledgement.

The response link owns one copied encoded result until receiver settlement,
respecting its negotiated message ceiling and the 65,536-byte local ceiling.
Frame payloads are split to fit the peer's frame budget. A batch stops at the
smaller of the peer's incoming capacity and our eight outstanding-frame limit.
Fresh peer acknowledgements replenish capacity; backwards/ahead acknowledgements
are refused. Link credit and delivery count change once per message, while
session counters change once per successful frame write. Continuations can
proceed with zero remaining message credit. Write failure leaves the failed
frame unaccounted and requires closing that stream. A queued response is sent
before drain consumes unused credit. A fixed 30-second response deadline also
bounds waits for capacity and receiver settlement; every write uses the earlier
of that deadline and the normal three-second write deadline.

The owner applies delivery deadlines to all reads, so heartbeats and empty
continuations cannot extend assembly or unsettled-result retention. The current
disabled listener grants no request credit. An uncredited request receives a
closed Detach with `amqp:link:transfer-limit-exceeded`, then orderly Close.
Completed assemblies are transport bytes only; signed verification, exact
operation/target authority, durable intake, final request dispositions, response
settlement handling, service activation and native qualification remain in the
next roadmap stages. No accepted disposition or native delivery is claimed by
this checkpoint.


On 2026-10-07 (Europe/London), final affected race suites passed: adapter
34.901s, AMQP codec 2.072s, auth 2.650s and config 2.526s. Vet, staticcheck,
format and diff checks passed. Tests exercise copied ownership, per-message
credit versus per-frame windows, matching/omitted continuation fields, abort
precedence, byte/frame ceilings, fixed deadlines, unsettled-result bounds,
short-write accounting, serial wraparound, window replenishment, negative peer
window estimates and deferred drain. An owned TLS connection carried an
independently spelled 32 KiB data section over repeated eight-frame batches;
the listener test observed zero-credit Detach/Close. Expired absolute socket
read/write deadlines refused I/O. Final subagent review found no actionable
issues. This is Darwin transport evidence; Linux native/service qualification
has not been performed for AMQP.


### Signed result envelope prerequisite (superseded)

Superseded on 2026-10-10: results are now plain JSON; see the plain request model.

AMQP results use the existing response signing key and compact EdDSA JWS type
`airc-gateway-response+jws`. The response envelope remains version 1 with purpose
`airc-gateway-response`, exact realm/principal/binding/route scope, issued time,
120-second lifetime and an object payload. The route scope is lowercase hex
SHA-256 of the exact bytes `amqp1-response`, NUL, configured request address,
NUL, configured response address, in that order. This accommodates the fixed
address grammar without changing the established response scope schema.

The signed payload is an `AMQPResult`: `version: 1`, `protocol: amqp/1`, the stable
inner `request_id`, exact `request_address` and `response_address`, and exactly
one `result` (the existing operation-specific detached result) or `error` (the
existing stable failure encoding). Wire delivery IDs and tags are absent.
One format-0 data section carries the compact token; the complete token must fit
32,768 bytes, including signing and envelope overhead. Excess results are
refused by the encoder so the dispatcher can return a bounded signed capacity
failure. TLS supplies confidentiality; no additional response recipient key is
needed for this signing-only carrier.

The shared response code now exposes signing-only emission and scoped
verification, using the same validation as encrypted responses. The encrypted
receiver still strictly requires JWE and retains its existing recipient/type
checks; its schema and encrypted profile have not changed. On 2026-10-07
(Europe/London), affected auth and adapter race suites passed in 2.027s and
34.915s; final focused signed-response and AMQP-result race tests passed in
1.344s and 1.541s. Vet, staticcheck, format and diff checks passed. Tests verify
the literal compact signing input with the standard Ed25519 verifier, envelope
fields, exact scope/refusal boundaries, key/type/time failures, encrypted-profile
refusal of a direct JWS, ordered address-pair scope, signed inner request IDs,
success/error shape and token capacity. Subagent review found no actionable
issues. The listener still grants no request credit; durable signed dispatch,
response settlement, activation and native qualification remain outstanding.


### Signed request admission prerequisite (superseded)

Superseded on 2026-10-10: requests are now plain contract JSON attributed to the
SASL principal; see the plain request model.

The admission primitive accepts a complete format-0 message containing compact
signed JWS intent. It verifies the signed principal and binding against the
TLS/SASL identity, then requires the exact configured route target and selected
operation (send, context, history or search). Optional user-id, destination and
reply-to metadata must match that same fixed grant. Content type, if supplied,
must be `application/jose`; content encodings are unsupported. Unknown mandatory
annotations are refused, while unknown `x-opt-` annotations are ignored.

Header TTL begins at the first transfer arrival. TTL and absolute expiry can
shorten the three-second intake deadline; already expired messages are refused.
The shared dispatcher also enforces signed validity and bounded read authority.
Transport expiration applies to terminal AMQP intake; a durably admitted native
job retains its signed intent and lifetime. Wire message IDs, correlation IDs,
delivery IDs and tags cannot change durable replay identity.

Admission uses the service's bounded request slots and existing dispatcher and
journal. Successful sends require a durable result; signed retries with changed
wire identifiers coalesce to the same job, including after journal reopen.
Conflicting signed intent retains the existing stable ID-conflict refusal.
Authorized capacity and dispatch failures retain the signed inner request ID for
the result stage. Failures before establishing that authority expose only an
AMQP refusal condition. Read expiry cancels and joins the real native attempt.

This prerequisite does not yet grant listener credit or emit dispositions.
The next roadmap item connects admission to request settlement and the authorized
response link; activation and independent native qualification remain outstanding.

On 2026-10-07 (Europe/London), final affected race suites passed: adapter
38.621s, AMQP codec 1.754s, dispatcher 1.634s, auth 1.791s and config 2.472s.
Affected vet and staticcheck, formatting and diff checks passed. Tests use the
real durable journal and reopen it for replay, preserve the original job after
conflicting intent, and exercise route, signature, expiry, capacity, cancellation
and partial-message refusals. An inert Unix socket peer observes actual native
connection closure at the 150-millisecond TTL for context, history and search;
this proves cancellation at the native boundary, not successful native reads.
Subagent review found no actionable issues. These are Darwin admission checks;
AMQP service and Linux native acceptance remain unfinished.


### Listener dispatch and settlement

The socket owner now receives the existing dispatcher, response signer, realm
and shared request slots. Both fixed authorized links must be attached before
it advertises one request credit. Complete signed requests pass through the
admission primitive; successful send intake emits receiver-first Accepted only
after the journal returns durable acceptance. Successful reads also emit
Accepted. Permanent authorized failures emit Rejected, transient failures emit
Released, and each carries its signed inner request ID in a signed result or
failure through the exact authorized response link. Authentication/metadata
refusals close the affected link and connection without a signed result.

Only one response is retained per connection. Missing response credit or session
capacity leaves it queued under the fixed 30-second delivery deadline. Fragment
writes obey the negotiated frame budget and bounded frame windows. A result is
retained until receiver settlement and completion of every fragment; early
receiver settlement records the peer's state without discarding remaining bytes.
Session Flow acknowledgements and non-settled dispositions cannot restore input
credit. Receiver settlement releases the result and grants one new request.
An oversized result becomes a bounded signed capacity failure; if even that
cannot fit the negotiated link ceiling, the response link is closed. Durable
intake remains recoverable with the same signed ID after response loss.

Disposition ranges use serial distances without iterating peer-controlled spans;
ambiguous half-space ranges are refused. Unknown delivery IDs are harmless.
Sender settlement during request assembly is recorded but cannot acknowledge
native acceptance. Sender-proposed states are outside the selected ingress
profile and are refused. Request abort discards assembly and replenishes credit;
peer Detach closes the volatile pair and connection. Recovery uses a fresh
connection and signed replay rather than resuming unsettled link state.

On 2026-10-07 (Europe/London), affected race suites passed: adapter 40.418s,
AMQP codec 1.919s, dispatcher 1.777s, auth 2.644s and config 2.311s. Final added
settlement/TLS conflict tests passed under race detection in 1.783s. Vet,
staticcheck, formatting and diff checks passed. An owned TLS peer negotiated
512-byte frames, submitted fragmented signed requests, observed durable journal
state after Accepted, delayed response credit, verified the signed response,
replayed under a changed wire ID, and observed Rejected with signed ID-conflict
on changed intent. A Flow echo fences non-settled response retention before
settlement restores request credit. Tests also cover serial wrap, unknown IDs,
range direction, half-space refusal and early settlement with remaining fragments.
Subagent review found no actionable issues. This is Darwin listener evidence;
at that listener checkpoint, service configuration remained disabled pending
independent native qualification.
That qualification must include a blocked-journal control for no early disposition,
actual native author/body observation, bounded successful reads, reconnect/loss
replay and the full service acceptance gate.


Service activation is qualified after the listener checkpoint. Enabled
AMQP configuration now selects the owned TLS/SASL adapter and provisioned
response signer. The AMQP listener acquires shared socket slots directly and
uses no framed ALPN. Independent native checks have passed as detailed below;
full acceptance passed as detailed below. This qualifies the selected AMQP
profile; catalogue mode 40's STOMP submode remains unfinished.


The independent client now spells AMQP frame/value/message encodings separately
from the production codec. On 2026-10-07 (Europe/London), current Darwin native
AMQP acceptance passed under race detection in 12.899s. Coverage includes exact
native author/target/Unicode bodies, fragmented transfers across renewed windows,
one-record history and search pages through the explicit caught-up boundary,
bounded complete context, unread acknowledgement/result loss, reconnect and full
gateway restart replay, durable journal identity and one native send attempt.
Signed validity is preserved across retries. Separate bounded AMQP result fences
prove committed native receipts before deliberate shutdown.

A writer-lock control withholds request disposition until release, then observes
Accepted and a committed row, followed by independent native delivery. Its reader
skips legal empty heartbeat frames without extending a fixed deadline; the final
focused race rerun passed in 5.773s. Authentication/authority controls cover TLS
trust, hostname and version, SASL password/user/authorization/mechanism, signed
signature/freshness, exact route target and operation, publisher/reply metadata,
unsupported content encoding/mandatory annotations and body size. These refusals
leave the journal and native room unchanged. Subagent review corrected receipt
fencing and heartbeat handling. Full service race acceptance subsequently passed.


Linux ARM64 native AMQP acceptance also completed successfully in the existing
pinned Ubuntu image, with four top-level tests and sixteen refusal subtests
passing (20 test passes, no skips or failures). The container used no network,
no image pull, read-only fixture binaries and disposable disk-backed state.
Gateway SHA-256: `21ca0c046e8e55a64757113243d18213b75ef2751ff3fc9abdd9b63e2b52a802`;
acceptance executable: `86112e60664597b278ab38f58cbff4a06e1e59d4e5ad300a6c45739de8612085`.
Pinned native SHA-256 remained
`1f3aa4a566a23d4326e2a2b3ed81942b5384e17521dc1bf38572511778d7e85f`.
Full service acceptance results for the same source are recorded below.


Full normal repository acceptance completed with terminal exit 0 in 635.162s,
recording 2,877 test/subtest passes, 53 skips and zero failures. All 36 packages
produced terminal events (28 passed; eight had no tests). The frozen Go/test/module
source SHA-256 is
`eee1a61a721a5d0c3f1d2903b3f88e10640e3009a75df07cc3f20462a96d4013`.
Normal acceptance alone does not close the roadmap qualification item.

The full race run subsequently recorded a failure in the existing CoAP TCP CSM
security test: its gateway exited with SQLite `disk I/O error (8714)`, and three
later subcases could not connect. The run terminated with exit 1 in 1,439.599s:
2,873 test/subtest passes, 53 skips and four failure events (three subcases and
their parent); all 36 packages terminated, with 27 passes, eight without tests
and the integration package failing. Its later build/static phases did not run.
Separate build/static checks passed as recorded below. Five unchanged-source
focused race repetitions of that CoAP test passed in 38.582s. The cause has not
been established, and these focused passes do not replace full race acceptance.
That failed attempt did not qualify AMQP; the successful unchanged-source rerun
is recorded below.

Read-only review of the observer and modernc SQLite close paths did not establish
an unsafe observer lifecycle: the query finishes before close, and mapping
references are local to each process. Error 8714 identifies a caught gateway
WAL-index access fault, not its external cause. Production WAL and durability
settings remain unchanged pending a reproducible diagnosis.

Separate checks on that same source digest subsequently completed with terminal
exit 0: `go build ./cmd/...`, `go vet ./...`,
`go run honnef.co/go/tools/cmd/staticcheck@latest ./...`, `gofmt -l .` (empty)
and `git diff --check`. These results do not replace the failed full race gate.

A fresh full race/build/static run completed with terminal exit 0 on the identical
source digest. Its metadata references the successful normal run and preserves
the earlier failed race result separately; no source, test claim or SQLite policy
changed. Full race acceptance passed in 1,430.023s with 2,877 test/subtest passes,
53 skips and zero failures. All 36 packages terminated (28 passed, eight without
tests). Build, formatting, vet, staticcheck and diff phases each exited 0.
The previously failing CoAP CSM test and all four subcases passed in that full
rerun (parent 6.570s); the original fault's cause remains unresolved. Exact
commands, provenance and limitations are in the
[AMQP integration checkpoint](testing.md#amqp-integration-checkpoint).
