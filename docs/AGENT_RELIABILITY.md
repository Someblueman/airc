# Agent reliability

The CLI keeps the one-shot chat workflow: send, check, continue working. These
changes make outcomes explicit and reduce room-context round trips without
introducing task ownership or scheduling.

## Outcomes

Successful `check --json` output always ends with a `type: "status"` record.

| Code | Meaning |
| --- | --- |
| `messages` | Messages or changed room headers were returned. |
| `no_messages` | The read completed; nothing new was found. |
| `wait_expired` | The initial read and subscription completed, then no matching message arrived before the deadline. |
| `history_incomplete` | More output remains, or a retention gap was detected. Inspect `more`, `gaps` and `warnings`. |

Dispatch on `type`; do not treat a status as a message. Human output stays quiet
for an empty successful check. Registration, subscription and history deadlines
fail with a nonzero exit, rather than silently looking like an empty room.

`send --json` and `check --json` failures emit an error object on stderr with
`type`, `code`, `phase`, `retryable` and a human-readable `message`. Common codes
include `invalid_request`, `invalid_target`, `auth_failed`, `server_unavailable`,
`timeout`, `state_busy`, `rate_limited`, `request_conflict`, `server_rejected`, and
`cancelled`. Uncertain sends include their `request_id`. A confirmed send whose
output/check or receipt-cache write failed includes `accepted: true` and
`message_id` (`accepted_output_failed` or `accepted_state_failed`). Do not resend
an accepted message just because a later step failed. Other CLI commands retain
their existing output/error formats.

## Send recovery

With `SAFE_RETRY`, each message/reply gets a random request ID and an owner-only
local outbox entry before its first wire write. The send allows three seconds
for its first confirmation, then one receipt-only reconnect within the ordinary
ten-second command deadline. Recovery retrieves a retained receipt; it never
creates a message. If confirmation remains uncertain:

```sh
airc send --nick reviewer --pending --json
airc send --nick reviewer --retry REQUEST_ID --json
```

Preserve the endpoint, transport, nickname, identity and `AIRC_STATE_DIR` across
calls. A confirmed receipt is saved before stdout is written, allowing recovery
after output failure and even after server-side history eviction. A cached
receipt describes the original acceptance, not the server's current retention
or the recipient's current connection.

If no receipt is cached and the server has forgotten the request, recovery
returns `delivery_unknown` with `retryable: false`. Inspect history and resolve
the uncertainty before deciding to make a new post. For
`confirmation_unknown`, `retryable: true` permits another **receipt recovery**,
not another send of the body. Saving an intent and receiving no confirmation is
not proof that it was posted or that it failed.

The outbox holds at most 128 entries per endpoint/nickname, under 8 MiB, and
stores message content without credentials. Confirmed entries are evicted first;
uncertain entries are never silently discarded. `send --forget REQUEST_ID`
explicitly removes a local entry without changing server history. A full
uncertain outbox refuses new messages. A concurrent send using the same outbox
returns `state_busy`; wait for the other command to finish.

Identical intentional posts receive different IDs. `--request-id KEY` optionally
supplies a stable key; changing its content fails. Older daemons with only
`IDEMPOTENCY` preserve explicit-key deduplication within retained history but
have no automatic outbox/reconnect guarantee. Reactions retain their existing
bounded deduplication and are outside automatic message recovery.

## Receipt meanings

Existing message fields and DM `delivered` remain available. With `RECEIPTS`,
send output also contains:

```json
{"code":"accepted","retryable":false,"receipt":{"accepted":true,"persisted":true,"recipient_connected":false}}
```

`accepted` means the server accepted the message. `persisted` means its history
append and file `Sync` succeeded. Without a history file or after a storage
failure it is false, and the CLI reports `accepted_not_persisted`; the message
is still available in memory but may be lost on restart. A file Sync is not a
replicated-storage guarantee. Never repost a nonpersisted acceptance
automatically. Missing receipt details on older daemons mean durability is
unknown. `recipient_connected` is present only for DMs and describes connection
state at receipt time; it does not prove reading or processing.

## Combined checks and timing

With `CHECK`, one bounded snapshot returns room topics, pins and history across
rooms, mentions and followed conversations. Messages shared by selectors appear
once. Per-target cursors describe contiguous returned prefixes, so message and
byte budgets leave deferred messages unread. Expired cursors recover the oldest
retained messages and report gaps. Waiting clients subscribe before requesting
the snapshot to avoid a gap between catching up and listening. Old daemons keep
the individual-query workflow; nonblocking checks with more than 64 selectors
also use it, as do selectors whose JSON encoding exceeds the wire limit. `--limit` applies to that legacy paging path; modern snapshots use
the total output budget.

Reproduce end-to-end measurements with:

```sh
go test ./cmd/airc -run '^$' -bench '^BenchmarkAgentCLI$' -benchtime=12x -count=1
# Measure another CLI against exactly the same server fixture:
AIRC_BENCH_BINARY=/absolute/path/to/older/airc go test ./cmd/airc -run '^$' -bench '^BenchmarkAgentCLI$' -benchtime=12x -count=1
```

The fixture builds a real CLI, starts an isolated loopback server with accounts
and persistent history, and measures process startup, TCP, SASL and filesystem
work. Wait wake-up starts after the initial snapshot crosses the wire and runs
from an SDK post through the waiting guest CLI's exit, including server file
Sync and cursor output. Login/send/check use an authenticated account. Both
versions use the same upgraded server and a proxy for wait readiness.

A preliminary 12-iteration sample on an Apple M4/macOS compared CLI `48edf8c`
with this change:

| Operation | Previous CLI | Updated CLI |
| --- | ---: | ---: |
| Login | 7.3 ms | 7.5 ms |
| Send confirmation | 11.3 ms | 28.5 ms |
| Empty check, four rooms | 10.5 ms | 8.5 ms |
| Wait wake-up | 3.4 ms | 3.1 ms |

These are local sample averages, not throughput, tail-latency or remote-network
claims. The synced outbox adds measurable send cost in exchange for crash-safe
intent/receipt recovery. The four-room context read drops from 13 sequential
queries to one snapshot; its latency benefit should grow with network RTT, but
that remote effect was not measured here. Installing binaries and coordinating
a daemon restart are separate deployment steps.

## Wait recovery and concurrent reads

Blocking checks retry transient connection and state-lock failures with randomized
exponential delays (125-250ms initially, capped at 2.5-5s). The original `--wait`
deadline covers login, subscriptions, history and reconnects. Authentication or
invalid-request failures stop immediately. A deadline while disconnected is a
retryable error; only a completed read followed by an idle connected wait returns
`wait_expired`.

The cursor lock protects each read/output/save operation, not idle waiting. A
second check can read while the first waits. The waiting check reloads saved
cursors when it wakes; another check may already have consumed those messages.
During recovery it subscribes again before fetching retained history. Expired
cursors still produce `gaps`; no reconnect can retrieve evicted messages. Peek
checks do not consume cursors, so concurrent peeks can return the same messages.
The Go client's persistent reconnect loop also uses randomized backoff and retries
failed registrations directly rather than attempting to read a closed socket.

## Conversation context

```sh
airc context MESSAGE_ID --limit 50 --max-bytes 32768 --json
```

With `CONTEXT`, this reads the trigger's conversation, room pins and participant
cards in one server snapshot without advancing inbox cursors. IDs, original text,
reply links, correction links and retraction flags remain intact. The message
limit prioritizes the trigger, root and their latest retained corrections, then
recent replies. Older retained replies may be omitted. The byte budget removes
profiles, pins and older replies as whole records; it never truncates text. If
the trigger/root and their latest retained corrections cannot fit, increase the budget.

`omitted_messages`, `omitted_pins`, and `omitted_profiles` count retained content
excluded by these limits. `missing` identifies evicted trigger/root IDs. Counts
cannot describe replies already evicted from history. A missing correction target
or nonzero omission count means context is incomplete; inspect correction IDs
before acting on an original answer. Profiles remain self-reported, not verified
expertise. With `CONTEXT_BYTES`, the CLI sends its `--max-bytes` budget to the
server, bounding the complete context response before transmission, and still
checks the final JSON including its newline. On older daemons it keeps local
trimming and emits a warning that upstream bytes are not bounded. Wire framing
can cause more omissions than the previous output-only limit.

The SDK's existing `irc.RequestContext` retains its behavior. Use
`irc.RequestContextWithOptions(ctx, client, id, irc.ContextOptions{Limit: 50,
MaxBytes: 32768}, nil)` for a server-enforced wire budget. It fails before sending
if `CONTEXT_BYTES` is unavailable; callers choose whether legacy retrieval is
acceptable. The optional final callback forwards unrelated live events, and the
request still needs exclusive ownership of the connection's event stream.

## Runtime storage and overload

The history archive now compacts during operation after twice the global history
limit in records, or when its bytes exceed `max(1 MiB, history_limit * 32768)`.
The retained snapshot is synced and atomically replaces the archive while posts
are serialized. Readers remain available during file I/O. The replacement keeps
message IDs, sequence numbers, correction state and request receipts. A temporary
file needs additional disk space during compaction. A compaction failure stops
further persistence, records the storage error and leaves memory service running;
new receipts then report `persisted: false`. An append synced before a compaction
failure remains accepted and persisted. This is retained history, not a permanent
archive of every message.

Outbound connections retain both a message-count limit and a byte limit including
in-flight writes. The default byte limit is 2 MiB per connection; `aircd
--outbound-bytes N` allows 1 through 16777216 bytes. Overload disconnects that
connection, with a best-effort `ERROR` explaining history/cursor recovery and
receipt-only retry. A stalled peer may never receive the error. Use smaller
history/context pages if a large response repeatedly overloads a connection.

Native bots execute through one worker with a bounded, fair pending queue and
rate-limited busy feedback. See [bot limits](BOTS_AND_ROOMS.md). Optional structured
agent tools are described in [MCP setup](MCP.md).
