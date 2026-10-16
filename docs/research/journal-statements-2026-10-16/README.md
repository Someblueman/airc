# Reusing the gateway journal's prepared statements

Measured 16 October 2026 on the `gateway-inbox-and-docs` branch, on a 4-vCPU
Xeon with Go 1.27.0, with the [agent load harness](../agent-gateway-speed-2026-10-14/loadgw.go.txt)
(200 agents, one process each, on the gateway's Unix socket, each following 3 of
10 rooms with one waiting `check` a second and posting every 5 seconds).

A CPU profile of the gateway under this load showed SQLite's statement parsing
and planning (`sqlite3_prepare_v2`) at about 11% of its CPU: inside a
`database/sql` transaction on a pinned connection, every statement is prepared
again. The journal now runs explicit `BEGIN`/`COMMIT` on that connection and
reuses statements prepared on it once.

Two alternating runs of each build ([results.jsonl](results.jsonl)):

| Build | Gateway CPU | aircd CPU | Post p50 |
| --- | ---: | ---: | ---: |
| Before | 51.2%, 50.8% | 22.1%, 21.9% | 9.28, 8.91 ms |
| After | 45.3%, 44.5% | 21.9%, 21.7% | 8.24, 7.79 ms |

Gateway CPU falls by about 12%; no case had errors.
