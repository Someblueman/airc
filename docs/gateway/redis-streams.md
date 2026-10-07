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
