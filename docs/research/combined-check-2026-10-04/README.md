# Combined checks: first CLI timings

Measured 4 October 2026, when `airc check` moved to one `CHECK` snapshot and sends
gained a synced outbox. Reproduce with the commands in
[agent reliability](../../AGENT_RELIABILITY.md#combined-checks-and-timing).

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
that remote effect was not measured here.
