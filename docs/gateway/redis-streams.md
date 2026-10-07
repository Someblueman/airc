# Redis Streams ingestion prerequisites

Redis Streams ingestion is not yet enabled. Remaining implementation and real
client/native qualification are in [ROADMAP.md](ROADMAP.md).

## Bounded entry decoding

The initial wire profile reads one literal stream with `XREADGROUP COUNT 1`.
[Redis documents](https://redis.io/docs/latest/commands/xreadgroup/) nested RESP2
stream/entry/field arrays and pending history reads. Deleted pending entries retain
an ID while their fields are null. Redis 7.2 emits a null array for those fields
([implementation](https://github.com/redis/redis/blob/7.2/src/t_stream.c#L1717-L1723)).
A deleted entry cannot establish signed intent or durable intake.

`rediswire.ReadStreamEntry` reads exactly one frame, accepts at most one returned
stream and entry, and matches the exact requested stream name. A live entry must
contain exactly one field named `intent` with a nonempty binary-safe bulk payload.
Unknown/duplicate fields, foreign streams, excess entries, incorrect RESP types,
malformed IDs and empty/null live payloads are refused. A null top-level array or
an empty entry array returns no entry. Deleted pending fields return the retained
ID with `Deleted=true` and no payload. All decode errors are terminal; callers
must provide deadlines and reconnect rather than resynchronize.

Entry IDs are canonical decimal uint64 pairs, excluding `0-0`, read cursors,
signs and leading zeros. Payloads retain the existing 32,768-byte bulk bound;
frames retain the 65,536-byte total budget and 128-element per-array bound. This
reader permits the five array levels needed by Streams replies. Ordinary RESP2
`Read` and `Encode` retain their four-level bound. No generic unbounded decoder
or production socket consumer is introduced.

On 2026-10-07, `go test -race ./internal/gateway/rediswire -count=1` passed in
1.173s; package vet/staticcheck, formatting and diff checks passed. Tests cover
binary/max-size payloads, pipeline retention, ordinary-reader depth refusal,
no-entry/deleted distinctions, stream/count/field/type/ID/size/depth refusals and
maximum uint64 IDs. This is decoding evidence only. The independent admission prerequisite is recorded
below; broker acknowledgement/recovery, result delivery, service lifecycle and
real Redis/native qualification remain unfinished.


## Configuration and entry identity prerequisite

The optional `redis_streams` array configures at most 32 outbound owners. Each has
`id`, `enabled`, `namespace`, numeric-IP `address`, verified `tls_server_name`,
optional absolute `tls_ca_file`, one `credential` (`username`, private absolute
`password_file`), fixed `group` and stable `consumer`, and one `route`. The route
contains an active signing `principal`, literal `stream`, distinct `result_stream`,
exact authorized `target` and explicit operations from Send, ContextRead,
HistoryRead and Search. A response signer is required; HistoryRead/Search also
require the cursor key. Signing-only principals need no bearer token. Password
and optional CA references participate in existing private-file/no-alias checking.
Contents will be decoded during service preparation; configuration checking alone
does not authenticate a broker or load a password.

One namespace identifies one logical broker history, independent of its current
dial address. Within a configuration, canonical IP/port equivalence permits only
one namespace per endpoint and one endpoint per namespace. Request/result stream
keys cannot overlap any other configured stream role in that namespace. Names
retain exact UTF-8 bytes, are nonempty and bounded to 255 bytes without controls;
namespace and owner IDs use existing 1-64-byte ASCII identifier rules. Groups are
preprovisioned, and one stable consumer owns each configured request stream. No
consumer reassignment or cluster routing is selected by this prerequisite.

`config.RedisStreamRequestID` computes lowercase SHA-256 hex from the following
exact bytes, with no trailing separator:

```text
redis-stream-request NUL namespace NUL stream NUL canonical-entry-id
```

The vector `broker`, `events`, `1-0` is
`bc549bc91e88964ae21e0728f29da5e4f9da4b5631437db1d59b4520efc6746e`.
Realm, principal and binding retain their existing independent journal scopes.
Owner ID, group, consumer, response stream and endpoint do not mint a new request
identity. Producers choose an explicit Redis entry ID before signing the existing
compact JWS request, put this derived value in its signed `request_id`, then use
that exact ID in `XADD stream entry-id intent token`. Automatic `XADD *` does not
meet this profile's signed entry binding. Namespace/stream history must not be
recreated with reused entry IDs; a new logical history needs a new namespace.

The helper and pre-dispatch enforcement are implemented as recorded below. Broker credentials and stream IDs confer no AIRC publisher
authority. Authentication, freshness, replay retention, conflicts and revocation
remain required. Unaccepted or expired intent and deleted pending entries cannot
be acknowledged as durable Send intake. Pending recovery after a lost ACK must
establish the retained journal outcome under current authority; this configuration
checkpoint does not yet implement that behavior.

Enabling an owner is rejected until service integration is implemented. Existing
configurations that omit `redis_streams` retain their schema. Full service/native
qualification remains outstanding. On 2026-10-07, all configuration race tests passed
in 1.645s. The affected Redis/Streams selection also passed for configuration
(1.559s) and wire (1.295s). Affected vet/staticcheck, formatting and diff checks
passed. Controls cover the fixed identity vector, changed namespace/stream/entry,
invalid identities, strict JSON, signing-only grants and revocation, operation
and target refusal, credential/trust file checking, canonical IPv6/port namespace
collisions, stream role collisions, exact 32-owner bound and premature enablement
refusal. These tests do not qualify a real broker, signed dispatch or native AIRC
delivery.


## Signed admission prerequisite

`adapter.admitRedisStream` rejects deleted entries, malformed IDs, absent/oversize
payloads and stream/result-route mismatches before establishing any correlated
scope. The existing compact signed JWS establishes the active publisher identity,
binding, target, operation, audience and validity interval. Its signed request ID
must equal the namespace/stream/entry digest above. The configured exact principal,
target and one of its explicit four operation grants must also match. No broker
username or unsigned entry metadata authenticates an AIRC author. Invalid intent
supplies no request ID, binding or correlated failure.

Only authenticated authorized intent competes for a shared bounded request slot.
Capacity failures retain verified correlation but do not execute. Dispatch uses
the existing verified-message boundary with an absolute three-second timeout
bounded by the parent. Send success requires the unchanged journal's durable
result; failures are mapped to existing scoped error codes, with transient capacity
or unavailable outcomes. Existing request fingerprint conflicts, configured and
durable revocation, and signed replay retention remain intact. Read cancellation
closes active native I/O and releases request capacity without journal mutation.

This function performs no Redis acknowledgement. A future connection owner may
acknowledge a successful Send only after durable intake and the required result
publication; failures and unsigned/deleted entries cannot prove durable intake.
Fresh signed replay coalesces the existing job. Recovery outside signed validity,
including a lost ACK after expiry, remains an explicit pending-recovery requirement;
this prerequisite does not bypass freshness or claim that recovery is implemented.

On 2026-10-07, the final `TestRedis|TestSigned` race selection passed for adapter
(17.156s), auth (4.898s) and configuration (3.443s). Affected vet/staticcheck,
formatting and diff checks passed. Controls verify exact journal body/target,
stable upstream identity across journal reopen and changed consumer/group/endpoint,
permanent changed-intent conflict, signing-only identity, configured and durable
revocation (new and replay), wrong namespace/entry/request ID/stream/route grants,
signature and freshness refusal, deleted/empty/oversize data, capacity and canceled
Send without intake, and parent cancellation through a real Unix socket for each
supported read operation. Native read sockets close and request slots are released.
These are admission controls, not Redis broker or native delivery qualification.


## Whole signed results prerequisite

`RedisStreamResult` is version 1 with protocol `redis/streams`, exact namespace,
request/result streams, immutable `entry_id`, derived signed `request_id`, target,
and exactly one complete detached result or failure. It uses the existing
Ed25519 response envelope, key and validity interval. Send shaping requires the
matching durable journal request ID. Supported read results retain the complete
Context, HistoryRead or Search representation, including opaque cursor, status,
gap and message metadata. The trusted dispatcher supplies one operation-specific
result; this helper does not invent native acceptance from broker responses.

The response route is lowercase SHA-256 hex of these exact bytes, without a
trailing separator:

```text
redis-stream-response NUL namespace NUL stream NUL result_stream NUL entry_id NUL target.kind NUL target.name
```

The vector `broker`, `events`, `results`, `1-0`, `room`, `#fixture` yields
`052adb17547817c9f58dc07c16f58d0d88abea1895f61fc882414a36ef8d6458`.
Realm, verified principal and binding remain separate envelope scope fields.
Consumer/group/owner and endpoint do not alter that scope. Clients must verify
this exact scope, key, envelope validity and inner correlation before using a
result. Streams and Pub/Sub have different protocol/domain identifiers.

`redisStreamResultMessage` validates canonical entry identity, its derived
request ID, stream-role separation, verified principal and exact response scope
before signing. The whole signed token must fit the 32,768-byte bulk bound.
Oversize returns no payload and `auth.ErrResponseCapacity`, allowing the future
owner to publish a correlated signed capacity failure. Pages and cursors are
never sliced to fit. This helper neither publishes results nor acknowledges
entries; those remain connection-owner work.

On 2026-10-07, the final `TestRedis|TestSigned|TestResponse` race selection passed
for adapter (15.878s) and auth (4.001s); affected vet/staticcheck, formatting and
diff checks passed. Controls cover the fixed scope vector, entry/namespace/stream/
result-stream/target/identity binding, Pub/Sub and NATS cross-protocol refusal,
expiry, literal Unicode/glob stream names, durable Send and failure correlation,
whole read pages/context, shape/entry/ID refusals, nil signer, and oversized cursor
refusal retaining the immutable original with a bounded signed failure. This is
result representation evidence, not service or real Redis/native qualification.


## Retained Send recovery checkpoint

The lookup-only prerequisite passed required full acceptance.
`auth.VerifyRetainedSend` checks the same signature, key/current signing profile,
audience, original validity shape and lifetime bound, inner mutation times,
normalization and current configured authorization as normal authentication. It
permits an expired Send only, retaining the future-issued-time bound. Its separate
`RetainedSend` type contains no executable verified message or replay lease;
normal `AuthenticateSigned` continues refusing expired intent.

`store.LookupRetainedSend` uses a read-only transaction under the journal lock,
current Send authority (including durable revocation), the retained original
fingerprint and existing durable Send result. Missing/garbage-collected jobs,
changed explicit intent and revoked authority fail. It cannot create a job,
resolve new defaults, extend replay markers or schedule native I/O. The dispatcher
matches exact configured principal, request ID and target before that lookup and
returns only the existing result and binding; it never constructs executable
verified intent. A changed consumer/group/endpoint does not change entry identity.

Focused race checks passed on 2026-10-07: adapter 1.770s, auth 2.038s and store
2.203s; dispatch compiled under race (2.264s, no selected tests). Affected vet,
staticcheck, formatting and diff checks passed. Controls retain exact job/upstream
identity across reopen, refuse ordinary execution of the expired envelope, reject
historical conflicts and missing jobs without intake, preserve the journal result
and replay boundary, and enforce configured/durable revocation. These are lookup
controls. Connection-owner recovery/ACK handling and native Redis qualification
remain outstanding. Full acceptance evidence is recorded below.

The frozen acceptance source SHA-256 is
`b52f54ec86628e46dfeaffad2d5a2575ec9265115f6dc47283104a68f605777a`
(sorted tracked Go/module paths and contents, each NUL-separated), baseline
`03adece` plus staged retained recovery changes. Logs/metadata remain under `/tmp/airc-gateway-streams-retained-v1-03adece-*`.
It checks the digest before every phase and after termination. Normal and race each recorded 3,604 passes, 53 skips, no failures, 33 passing
packages and ten without tests. All phases and the runner exited 0; the digest
matched after termination. Normal elapsed 650.645321s; race 1564.731237s. Build,
formatting (empty output), vet, staticcheck and diff checks also passed. Exact
commands and timings are in [testing.md](testing.md#streams-retained-send-recovery-checkpoint).
