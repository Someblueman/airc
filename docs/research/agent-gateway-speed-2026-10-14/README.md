# Faster, cheaper agents through the gateway

Measured 14 October 2026 on the `gateway-agent-speed` branch against `main` (`ee994cf`). The host is a 4-vCPU Xeon with Go 1.27.0.

[The earlier load test](../agent-gateway-load-2026-10-14/README.md) found that, once its limits were fixed, the gateway served 200 sandboxed agents but cost about 11 times native IRC's CPU. Three costs remained: a post took about 55 ms, every request logged in a new upstream AIRC session, and a follower read each of its rooms every second. This branch addresses each.

## Changes

1. **A post answers with its receipt.** The answer to a mutation now waits up to 250 ms for the job's first delivery outcome. A send that AIRC accepts at once therefore answers `accepted` with its message ID. Before, it answered `queued` and the client polled `receipt.get` 50 ms later. The delivery runner also starts a new job at once while any worker is free.
2. **Each author's upstream session is reused.** A session that finished a send or read cleanly is kept for the same author's next request. It is keyed by author scope, token digest and client source, so it never serves anyone else. Before reuse, a ping proves that every earlier reply has been read. For a guest, `WHO` also checks that no connected client has taken the nickname. Sessions close after 5 s idle or 30 s after login. `limits.idle_sessions` (default 128) caps them.
3. **A `check` operation.** One request reads up to 16 rooms after message IDs with one native `CHECK`. With `wait_seconds`, a check that finds nothing new observes its rooms and answers on the first new message. Otherwise it answers at its carrier's deadline, about 3.25 s on most carriers. While waiting it holds its author's session but no session slot. `limits.waits` (default 256) caps waiting checks, and each listener's requests in flight are now `sessions` + `waits`. A read that finds every session slot busy waits up to a second for one. `airc` uses `check` when it follows rooms over a gateway carrier, starting rounds at most once a second. Against an older gateway it reads one room at a time, as before.

## Workload

[loadgw.go.txt](loadgw.go.txt) runs real `aircd` and `airc-gateway` processes, as in the earlier note. There are 10 rooms, and each agent follows 3 of them. Each agent is its own process on the gateway's Unix socket, and the gateway uses default limits.

Agents follow their rooms in one of two ways:

- **Per-room reads.** Each room is read every second, as `airc` does over a gateway today.
- **Waiting check (`-check`).** One waiting check, at most once a second.

Each agent posts every 5 s ("busy") or every 5 minutes ("quiet"). Native agents use one connection each and a multi-room `CHECK` every second.

The new **deliver** column measures the time from a post to the moment another agent first reads it, using the server's message timestamp. Each case runs 5 s of warmup and 30 s of measurement. CPU is averaged over the 30 s, and 100% is one core. See [results-main.jsonl](results-main.jsonl), [results.jsonl](results.jsonl) and [summary.txt](summary.txt). The summary table comes from [summary.py.txt](summary.py.txt).

## Results

CPU is aircd + gateway. No case had errors.

| Agents, workload | `main` | Branch, per-room reads | Branch, waiting check | Native |
| --- | --- | --- | --- | --- |
| 50, busy | 33.9%, 146 req/s | 25.1%, 148 req/s | 18.5%, 50 req/s | 2.5% |
| 100, busy | 64.9%, 295 req/s | 47.6%, 298 req/s | 35.3%, 100 req/s | 5.1% |
| 200, busy | 128.1%, 587 req/s | 99.2%, 595 req/s | 72.9%, 200 req/s | 11.9% |
| 200, quiet | 81.2%, 594 req/s | 63.3%, 598 req/s | **12.0%, 88 req/s** | 4.5% |

| Latency (p50) | `main` | Branch |
| --- | --- | --- |
| Post, until its receipt | 54–55 ms | 8–9 ms (15 ms quiet) |
| Delivery, quiet rooms, waiting check | 434 ms (reads) | **7.4 ms** |
| Delivery, busy rooms | about 500 ms | about 500 ms |

- **Posts are about 6 times faster** on every workload. The remaining 8 ms is the gateway's journal commit, the delivery, and the outcome's commit.
- **Session reuse alone cuts CPU by 22–27%** with an unchanged workload (the per-room read column). The saving is split between aircd, which no longer registers a session per request, and the gateway, which no longer dials one.
- **In quiet rooms the check costs a fifth of per-room reads' CPU (a seventh of `main`'s) and delivers news in milliseconds.** An agent then costs one request every few seconds instead of three a second.
- **In busy rooms the check costs a third of the requests and about 26% less CPU than per-room reads.** Rounds are paced to once a second, so delivery latency stays about 500 ms. Each round brings back everything that arrived since the last one. Without pacing, every message woke every follower: 200 agents made 1,519 checks a second and overran the session slots.
- In busy rooms the gateway still costs about 6 times native IRC. What remains is a request per agent per second over the socket (syscalls), and the journal's SQLite work for each post: about a fifth of the gateway's CPU, half of that re-parsing statements.

## Not done here

- **Inbox in `check`.** Direct messages still cannot be read through the gateway. AIRC's inbox selector cannot prove which account received an old message, and that needs a server change.
- **`check` over MCP.** MCP keeps its fixed six tools, so agents reach `check` through every other carrier.
- **SQLite statement caching** in the journal is the largest remaining gateway cost per post.
