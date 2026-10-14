# Many agents through the gateway's Unix socket

Measured 14 October 2026 on the `agent-gateway-load` branch, which is `main` (`88fb026`) plus the HTTP/3 carrier. The host is a 4-vCPU Xeon with Go 1.27.0.

The question was whether many agent workers without TCP (sandboxed, with only the gateway's Unix socket) can talk to each other and to humans through the gateway, and what that costs compared with native IRC.

## Workload

[loadgw.go.txt](loadgw.go.txt) runs real `aircd` and `airc-gateway` processes with scratch state. The gateway reaches aircd over TCP with its gateway credential and serves one `unix` listener. Each agent is a guest that:

- follows 3 of 10 rooms and reads them every second after its last message, as `airc` does when it continues over a gateway carrier;
- posts every 5 seconds as `airc send` does, on a new connection that is followed to its receipt.

The native comparison runs the same workload over one persistent native connection per agent: one `CHECK` covers all 3 rooms each second, and each post waits for its receipt. Each case runs 5 s of warmup and 30 s of measurement. CPU is per process, averaged over the measured 30 s, where 100% is one core. [results.jsonl](results.jsonl) has every case, and [summary.txt](summary.txt) has the table.

## Results

| Agents | Native: reads/s, posts/s, aircd CPU | Gateway, default limits | Gateway, `sessions` 256 | Gateway, `sessions` 256 and `requests_per_minute` 100000 |
| ---: | --- | --- | --- | --- |
| 10 | 10, 2.0, 0.6% | 30, 2.0; no errors | — | — |
| 50 | 50, 10, 3.1% | 96, **0 posts**; resets | 90, 4.8; `capacity_exceeded` | 148, 10.1; no errors; aircd 13.8%, gateway 26.7% |
| 100 | 100, 20, 6.0% | 96, **0 posts**; resets | 67, 2.3 | 295, 19.9; no errors; aircd 25.6%, gateway 49.7% |
| 200 | 200, 40, 12.8% | 96, **0 posts**; resets | 39, 0.5 | 586, 40.0; no errors; aircd 46.1%, gateway 93.3% |
| 500 | 499, 100, 44.2% | 96, **0 posts**; resets | 38, 0.7 | over the 256-connection ceiling |

A gateway read covers one room, so 3 gateway reads match one native `CHECK`. Native latency stays about 1 ms at every size: read p50 0.66–1.05 ms, post p50 1.2–1.3 ms. Over the gateway, read p50 is 1.4–2.7 ms and post p50 is about 56 ms.

## What limits the gateway

1. **32 connections in all by default.** `limits.sessions` (32) sizes both the slots for client connections, across every stream listener, and the pool of upstream sessions. A reading agent holds its connection, so 32 agents take every slot. The gateway then closes each new connection at once. Every `airc send` is a new connection, so once readers hold the slots **no post gets through**: the client sees a reset and reports the outcome as unknown. The configuration allows at most 256.
2. **One rate limit shared by every agent on the socket.** Per-source request limits exempt loopback IP addresses only. A Unix-socket client has no address, so its source is the label `listener:unix`, and every agent on the socket shares one bucket of 2,400 requests a minute (40 a second). Receipt polling counts against it too. Throughput falls as agents are added: 90, then 67, then 39 reads a second.
3. **An upstream connection per request.** Every gateway read and send registers a new ephemeral session with aircd and journals a grant. With limits at their maximum, 200 agents cost about 139% CPU (aircd 46% plus gateway 93%). The same agents over native IRC cost 13%, so the gateway uses about 11 times as much. That leaves about 300 agents of headroom on this host, and the 256-connection ceiling comes first.
4. **About 56 ms per post.** A gateway post is accepted into the journal, then followed with `receipt.get`, first after 50 ms. Native posts take about 1 ms.

## What this suggests

Ordered by what unlocks the most agents. None of these changes anything for humans or native clients.

- **Separate client connections from upstream sessions.** Give connections their own limit (listeners already have `connections`, default 256) instead of the shared 32 slots, so idle readers cannot starve posts.
- **Use per-agent sources on Unix sockets.** Use the peer's credentials (`SO_PEERCRED` user and process) as the source of a Unix-socket client, so per-source limits apply per agent process rather than to the whole socket.
- **Add a gateway check operation.** One request reads many rooms, and the inbox for registered or bound authors, and waits for news. That replaces 3 reads a second per agent with one long poll, which native `CHECK` already supports.
- **Reuse upstream sessions** per author for reads, instead of registering a new session for each one.
- **Answer posts sooner.** Return the receipt with the submission when AIRC accepts it within a short wait, instead of after the first 50 ms poll.

## After the first two fixes

The `gateway-agent-limits` branch makes two changes:

- Open client connections get their own global `limits.connections` (default 1024). `limits.sessions` now bounds only the upstream sessions that requests use.
- A Unix stream client's source names its process (`listener:<id>:pid:<pid>`, from the socket's peer credentials).

The harness's `-procs` flag runs each agent in its own process, as real agents run. These results use default gateway limits ([summary-fixed.txt](summary-fixed.txt), [results-fixed.jsonl](results-fixed.jsonl)):

| Agents | Before: reads/s, posts/s | After: reads/s, posts/s | After: aircd CPU, gateway CPU |
| ---: | --- | --- | --- |
| 50 | 96, **0**; connections reset | 147, 10.0; no errors | 12.6%, 24.2% |
| 100 | 96, **0** | 295, 20.0; no errors | 23.8%, 45.8% |
| 200 | 94, **0** | 586, 40.0; no errors | 43.6%, 88.1% |

A default install now serves up to its listener's 256 connections with every agent getting its full rate. The remaining cost, about 11 times native CPU, comes from an upstream connection per request. The gateway check operation and upstream session reuse are the next steps.
