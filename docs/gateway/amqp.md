# AMQP 1.0 ingress

AMQP implementation is in progress. Bounded frame I/O and protocol-field type
decoding, encoding, data-message, SASL and transport-performative validation and disabled authority configuration are implemented; session/link handling and
native service acceptance remain in [ROADMAP.md](ROADMAP.md).

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
all sections. Header/properties remain separate from the signed body. UUIDs use
16 bytes; timestamp values retain signed 64-bit wire bits without interpreting
wall time. A response encoder emits one bounded data section.

The session must enforce metadata TTL/absolute expiry, address/publisher policy
and supported content encoding. Transport metadata cannot replace signed
identity, freshness, replay identity or target authority. This terminal ingress
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
principal target; grants require an active bearer and envelope-signing principal.

The initial operation profile is `send`, `context.read`, `history.read` and
`search`. Each route operation must be allowed by both principal and listener;
each listener operation must have a route grant. History/search requires the
existing cursor key. Every route has a response address and the configuration
requires a response signing key, including send-only routes. The planned
response link returns bounded operation results; it does not confer authority
on another principal or authorize arbitrary broker forwarding.

Parsing opens no sockets and reads no credential files. Enabled AMQP is still
refused because session handling is not yet wired. Runtime SASL authentication,
exact link/target/operation enforcement and signed response delivery remain
implementation and qualification work. On 2026-10-06, configuration race tests
passed in 1.335s; vet, staticcheck and diff checks passed. Tests exercise authority
refusals, address collisions, route bounds, disjoint send/read grants and unwired
enablement. Commit review found no actionable issues.
