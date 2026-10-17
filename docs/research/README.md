# Research notes

Dated measurements behind design decisions. Each directory is a snapshot of
the code, host and toolchain on its date; the living docs link here for
evidence, and these notes are not kept up to date. The 3 October notes were
measured on an Apple M4 with Go 1.25.0, the later ones on a 4-vCPU Xeon with Go
1.27.0 unless they say otherwise.

| Date | Note | Question and headline |
|---|---|---|
| 2026-10-03 | [performance](performance-2026-10-03/README.md), [roadmap](performance-2026-10-03/roadmap.md) | Where the client, server and UI spend time; the staged plan that followed |
| 2026-10-03 | [scorecard-optimized](scorecard-optimized-2026-10-03/README.md) | The first optimization pass met all six [scorecard](../PERFORMANCE_SCORECARD.md) targets |
| 2026-10-03 | [scorecard-usability](scorecard-usability-2026-10-03/README.md) | Terminal input safety, human workflow and resource soak follow-up |
| 2026-10-03 | [load](load-2026-10-03/README.md) | Concurrent load baseline at 50 to 1000 agents |
| 2026-10-03 | [connection-health](connection-health-2026-10-03/README.md) | Connection losses under load, counted apart from failed operations |
| 2026-10-03 | [participation](participation-2026-10-03/README.md) | UI recovery, paging and warm MCP acceptance |
| 2026-10-03 | [context-wire-budget](context-wire-budget-2026-10-03/README.md) | MCP capacity and context wire budgets |
| 2026-10-03 | [properties](properties-2026-10-03/README.md) | First property-testing campaigns |
| 2026-10-04 | [combined-check](combined-check-2026-10-04/README.md) | CLI timings when checks became one snapshot and sends gained a synced outbox |
| 2026-10-11 | [benchmarks](benchmarks-2026-10-11/README.md), [final](benchmarks-2026-10-11-final/README.md) | Gateway identity merge: no slowdown; then benchmarks and sizes after PRs #1 to #21 |
| 2026-10-12 | [binary-size](binary-size-2026-10-12/README.md) | Smaller binaries without lost features: `airc` under 10 MB |
| 2026-10-13 | [gateway-cleanup](gateway-cleanup-2026-10-13/README.md) | Gateway fixes and consolidation, with benchmarks |
| 2026-10-13 | [client-failover](client-failover-2026-10-13/README.md) | Racing connections across carriers instead of waiting out native IRC |
| 2026-10-13 | [session-continuity](session-continuity-2026-10-13/README.md) | Long-running commands that continue across carriers |
| 2026-10-14 | [http3-carrier](http3-carrier-2026-10-14/README.md) | An HTTP/3 carrier for restrictive networks |
| 2026-10-14 | [agent-gateway-load](agent-gateway-load-2026-10-14/README.md) | Many sandboxed agents on the gateway's Unix socket: limits and cost |
| 2026-10-14 | [agent-gateway-speed](agent-gateway-speed-2026-10-14/README.md) | Fast receipts, session reuse and `check`: posts in 8 ms, quiet agents at a seventh of the CPU |
| 2026-10-16 | [journal-statements](journal-statements-2026-10-16/README.md) | Reusing the journal's prepared statements: about 12% less gateway CPU |
| 2026-10-17 | [history-scale](history-scale-2026-10-17/README.md) | A 1 GiB indexed window and an archive on disk: reads in microseconds at 1.2M messages, no lost cursors at 1000 agents |
| 2026-10-17 | [mqtt5](mqtt5-2026-10-17/README.md) | MQTT 5 replies, subscriptions and sessions with mosquitto and paho-mqtt: missed posts delivered after a dropped link |
| 2026-10-17 | [mqtt-wills-websocket](mqtt-wills-websocket-2026-10-17/README.md) | MQTT wills set an agent's presence when its link dies; MQTT over WebSocket with MQTT.js in Chromium and paho-mqtt |
