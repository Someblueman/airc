# Poll results

`poll.results` reads one native persisted room poll through HTTP, framed TCP/TLS,
WebSocket and Unix stream intake. Poll creation, voting and closing through the
gateway remain required work; native clients currently supply those mutations.
This increment does not complete any catalogue adapter.

## Request and authorization

Bearer HTTP uses `GET /v1/poll?kind=room&target=%23room&poll_id=ID`.
Only `kind`, `target` and `poll_id` query keys are accepted, each once.
Signed/encrypted HTTP and correlated stream requests carry this intent:

```json
{
  "version": 1,
  "request_id": "poll-read-1",
  "operation": "poll.results",
  "target": {"kind": "room", "name": "#room"},
  "poll_id": "0123456789abcdef0123456789abcdef"
}
```

The ID must be canonical lowercase 32-character hexadecimal. No message,
parent/thread selector, paging/cursor, delivery policy or timing arguments apply.
The principal and listener must both allow `poll.results` and the exact room.
The current authorization snapshot supplies one-use bounded read authority;
reads create no delivery job or receipt. Signed reads cannot outlive their
signed validity boundary. Unix still requires the encrypted envelope profile.

Poll-only listeners require two global and per-principal sessions to preserve
send capacity. A cursor key and watch quota are unnecessary. Authenticated
capabilities report configured `poll_targets` and `max_poll_bytes: 16384`;
upstream compatibility remains unknown until the native session negotiates CHAT.

## Native facts and retention

A successful result contains the exact target, poll `id`, author `from`, optional
stable `account_id`, whole `question`, ordered `options`, corresponding aggregate
`votes`, native `expires_at` and explicit `closed` fact. Guest authors have no
account ID. Voter identities are not returned. The gateway does not recompute
closed state or turn an expiry timestamp into an answer.

Native poll state is persisted independently of CHECK history. Results remain
readable after the original question leaves the history ring, and until more
than seven days after native expiry. Consequently, history/context ownership
proof is unnecessary: the native authoritative poll result itself must match
both the requested ID and authorized room before anything is returned.

The exclusive native session must return exactly one valid results entry and a
complete terminator within its deadline. Missing, duplicate, malformed, foreign
or incomplete responses fail without a partial result. Native question/option,
identity, option count and aggregate voter bounds apply. Known unrelated CHAT
fields are rejected. The pinned native SDK ignores unknown JSON extension
properties; those properties are not included in the gateway result. The canonical encoding of decoded native results and their terminator, and
the complete gateway result, are bounded by 16 KiB. This native accounting uses
known decoded fields; it does not count discarded extension properties as raw
wire bytes. The SDK line ceiling and read deadline still apply. Whole fields are preserved rather than truncated.

## Verification

Collector race checks cover whole expired results, exact wire budget and one-byte
overflow, missing/duplicate/foreign responses, malformed fields, vote bounds and
a withheld terminator under continuous unrelated traffic. Final real transport
race checks passed in 22.102s on the Darwin host. HTTP/1.1, HTTP/2, signed
HTTP/2, framed TCP, WebSocket and Unix streams preserve whole populated results
before and after history eviction. An independent native observer checks the
original poll message; native vote/close operations and clock expiry change the
read snapshot. Foreign poll IDs requested through an allowed room and missing
IDs return typed failures; malformed query selectors fail. Native history and
absence of delivery receipts provide nonmutation evidence. All gateway packages
and the CLI passed race checks, with final parser controls separately covered;
vet, staticcheck and gateway/client build checks passed. Shared stream
conformance checks are recorded in STATUS.md.

The seven-day retention rule is inspected native behavior, not a seven-day
wall-clock acceptance test. Poll writes through the gateway, Linux runtime
acceptance and complete catalogue coverage remain pending.
