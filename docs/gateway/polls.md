# Polls

`poll.results` reads one native persisted room poll through HTTP, framed TCP/TLS,
WebSocket, Unix stream and stdio intake. The shared mutation path also supports
`poll.create`, `poll.vote` and `poll.close`.
This increment does not complete any catalogue adapter.

## Request and authorization

HTTP uses `GET /v1/poll?kind=room&target=%23room&poll_id=ID` with the author
headers. Only `kind`, `target` and `poll_id` query keys are accepted, each
once. `POST /v1/request` and correlated stream requests carry this intent:

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
The listener must allow `poll.results` and the room; AIRC decides whether the
author may read it. Each read takes one bounded read lease and creates no
delivery job or receipt.

Poll-only listeners require two global and per-author sessions to preserve
send capacity. A cursor key and watch quota are unnecessary. Capabilities report configured `poll_targets` and `max_poll_bytes: 16384`;
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

Dated records follow. Where they mention principals, bindings, signed or
encrypted envelopes, signing keys or return routes, they describe the
[removed identity model](removed-identity.md), which no longer exists.

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
wall-clock acceptance test. Linux runtime acceptance and complete catalogue coverage remain pending.

## Mutations

All three mutations require the listener's operation and room permission and
bounded delivery validity. They use the HTTP mutation endpoint or a correlated
stream request. Creation takes
`poll: {question, options, ttl_seconds}`: nonblank question up to 1000 UTF-8 bytes,
two to eight distinct nonempty options up to 80 bytes each, and TTL 1–604800
seconds. Native brief-text rules apply. Vote takes canonical lowercase `poll_id`
and one-based `choice` 1–8; the actual poll determines the valid option count.
Close takes `poll_id` without poll input or choice. Native mute, slow-mode,
capacity and author/admin checks remain authoritative.

Vote and close first obtain a bounded native results snapshot on the same
authenticated session, proving the poll belongs to the requested room before
writing. This works after history eviction because native poll state is retained
separately. A foreign ID cannot redirect a mutation to another room.

Accepted `result.poll` contains version, operation, target, native action
`results`, and the complete historical poll snapshot. Creation also contains
the complete native `receipt`; its generated message ID and poll metadata must
match the snapshot. The top-level creation result preserves actual receipt
persistence facts, which may be false after a history write failure. A room
poll has no recipient-connected fact. Vote and close have no message receipt
and expose no fabricated message ID, persistence or delivery status. Snapshot
`from` and `account_id` identify the poll author, including when someone else
votes. Votes are aggregate counts, never voter identities.

Schema 10 stores this compound evidence as one exclusive `poll_outcome`. Replay
returns the original snapshot even after subsequent votes or closing. Native
polls have no request-ID receipt lookup, so all three mutations use zero lookup
attempts. Default bounded retries preserve the gateway request ID but can create
a second native poll after an uncertain creation; the result records duplicate
risk. Repeating a vote replaces that actor's choice. Receipt-only policy keeps
uncertainty without resending or inventing an acknowledgement.

Mutation carrier race checks passed in 24.086s for bearer HTTP/1.1 and HTTP/2,
signed HTTP/2, encrypted HTTP, framed TCP/TLS, WebSocket, Unix streams and stdio.
An independent native observer compares whole creation metadata and vote/close
aggregate broadcasts. Controls cover author versus voter identity, non-author
close denial, foreign room IDs, history eviction, vote replacement, historical
replay after native changes and process restart, and creation retry producing a
second native ID. Receipt-only recovery neither resends nor fabricates evidence.
Real Chrome WebCrypto/WebSocket acceptance passed in 43.760s for all three
mutations alongside the existing crypto, replay and live-watch checks. Focused
backend race checks passed in 3.771s, including permanent preflight numerics,
deadline expiry and unexpected receipt rejection without writes. Errors after
a partial receipt or result preserve delivery uncertainty and the native numeric
code; no incomplete acknowledgement becomes accepted. Native mutation race
checks after this guard passed in 25.386s. Focused
journal checks passed in 5.402s for whole restart evidence, false persistence
facts, immutable failed-save retry, option copy boundaries, eight-way SQL guards
and migration from schema 9 preserving canonical reply-signal evidence. Full module normal/race suites passed before the final partial-ACK guard
(integration 393.459s/407.117s, store 5.745s/39.216s). Final backend/native
poll race checks cover that guard; module vet and staticcheck pass. No complete
adapter is claimed.
