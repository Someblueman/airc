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
maximum uint64 IDs. This is decoding evidence only. Signed entry identity,
authorization, durable acknowledgement/replay, result delivery, service lifecycle
and real Redis/native qualification remain unfinished.


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

The helper is implemented, but enforcement before dispatch remains in the signed
admission roadmap item. Broker credentials and stream IDs confer no AIRC publisher
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
