# History at the scale of many agents

Measured 17 October 2026 on the `history-scale` branch, on a 4-vCPU Xeon VM with
15 GiB of memory and Go 1.27.0.

The question was whether aircd's history can serve hundreds of agents: a window
of 10,000 messages lasts minutes at 500 agents, and a cursor older than the
window used to be lost.

## Changes measured

- The in-memory window is bounded by approximate memory (`--history-memory`,
  default 1 GiB) instead of 10,000 messages, with an index per room, inbox,
  thread and parent.
- The history file is an archive: sealed 64 MiB segments with sidecar indexes,
  deleted oldest first by age and size, and read when a cursor is older than the
  window.

## The window

`BenchmarkHistoryFullWindow` fills a 1 GiB window: 1,212,977 messages with
300-byte bodies from 500 agents in 20 rooms, 10% direct messages and 10%
mentions.

| Read | Time |
|---|---:|
| Inbox (`@nick`) | 120 ns |
| Empty inbox | 97 ns |
| Caught-up room check | 79 ns |
| Latest 50 room messages | 23 µs |
| 50 room messages from mid-window | 22 µs |
| Append | 1.8 µs |
| Search the whole window for absent text | 0.6 s, releasing the lock every 4096 messages |

Before, the window was capped at 10,000 messages and an empty inbox read scanned
all of them: 250 µs. `TestHistoryBytesTrackHeap` keeps the memory estimate
within 5% of the heap: a 300-byte message costs about 870 bytes.

## The archive

[scale_test.go.txt](scale_test.go.txt) wrote 3,000,000 messages (1.36 GiB in 20
sealed segments and the active file), then restarted the server.

| | Result |
|---|---|
| Restart | 1.8 s; 1,217,058 messages in the window (about 1 GiB) |
| First page of a busy room from the oldest cursor | 2.8 ms |
| A room with one message in the whole archive | 1.6 ms |
| An inbox with no messages, from the oldest cursor | 0.4 ms |

The first version decoded every segment on restart (11 to 15 s) and scanned the
active file before the indexed segments when looking up a cursor (about 55 ms
per read); both were fixed before these figures.

## Load

`airc-load` at 1,000 agents, each doing one operation a second (40% posts, 20%
mentions, 10% each replies, reactions, checks and searches), persistent history,
30 s measured. The harness now takes `-history`; it previously fixed the window
at 10,000 messages, which at 1,000 posts a second is ten seconds of traffic.
[load.jsonl](load.jsonl) has every case.

| Run | Missed slots | Gap markers | Check p50 | Post p50 | Post p99 | Server CPU | Peak RSS |
|---|---:|---:|---:|---:|---:|---:|---:|
| `main`, 10,000-message window | 1 | 1,500 | 1.9 ms | 2.6 ms | 958 ms | 44 s | 97 MiB |
| branch, 10,000-message window | 0 | 0 | 1.6 ms | 1.6 ms | 16 ms | 44 s | 102 MiB |
| branch, 1 GiB window | 0 | 0 | 1.4 ms | 1.6 ms | 14 ms | 44 s | 125 MiB |

On `main`, 1,500 checks found their cursor gone and recovered from the oldest
retained message, losing what fell in between. On the branch those cursors
resume from the archive. A first run of the branch with the 10,000-message
window missed 1,602 slots (check p50 12.8 ms): finding a cursor in the active
file decoded every line until it matched. The active file's IDs are now mapped
in memory, and archive reads skip lines without the cursor's ID or target before
decoding; the table is after that fix. At 500 agents `main` and the branch were
alike (no missed slots, no gaps).

## Limits of these numbers

- One host, synthetic traffic, and one run per case.
- `airc-load` connects each agent once; it does not measure the CLI's startup.
- The archive figures use the page cache: the files were just written.
