# Session continuity: long-running commands across carriers

Measured 13 October 2026 on the `session-continuity` branch against `main`
(`5a93ce7`), on a 4-vCPU Xeon with Go 1.27.0. This is phase 2 of client
failover; phase 1 ([client-failover-2026-10-13](../client-failover-2026-10-13/README.md))
raced one-shot commands across carriers.

## What changed

- **Liveness.** `pkg/irc` can probe a connection: a PING that must be answered (by its PONG or any other line) within a timeout, or the connection is dropped as stalled. `airc` keeps alive at 5 seconds with a 3 second probe, and `pkg/bot` at 15 seconds. A send probes once its receipt is 300 ms late. Before, a silent link was noticed only by the two-minute read timeout, or by a send's 3 second attempt timeout.
- **Sends.** A stalled send is posted again with the same request ID over whichever carrier connects first, native included, instead of redialling native alone.
- **Gateway start positions.** `history.read`, `watch` and `events.watch` accept a message ID as `start`, so a client carries a native cursor, or the last message it saw, to a gateway carrier.
- **Rooms continue.** While native IRC is unreachable:
  - `check` (with or without `--wait`, and the MCP `check` tool) reads followed rooms over a gateway carrier from the stored cursors;
  - `watch` and the full-screen UI follow their rooms there, and the UI posts there;
  - native IRC is redialled in the background, and once it registers each command continues natively from the same messages.

  Rooms are read over one gateway connection, every second while active and backing off to four seconds while idle. This keeps any number of rooms within the gateway's per-author limits: watches are capped at two per author and eight in all.
- **DMs stay native.** Direct messages, mentions and followed threads remain native-only, because the gateway cannot prove which account an old DM was delivered to. A check over another carrier warns about this.

## Latency

[latency-harness.go.txt](latency-harness.go.txt) runs in `cmd/airc` against an in-process `aircd` and gateway; [latency-samples.txt](latency-samples.txt) holds every sample. Times are in milliseconds.

| Scenario | main | branch p50 | branch max |
| --- | ---: | ---: | ---: |
| Send; native goes silent after the post, then refuses | 3076 | 1361 | 1363 |
| Send; native goes silent after the post, then accepts and stays silent | not measured | 1613 | 1635 |
| `watch`: native closes, next message shown | after native returns | 1014 | 1015 |
| `watch`: native goes silent, next message shown | after native returns | 7989 | 7990 |
| `check --wait`: native closes, next message returned | after native returns | 194 | 223 |
| `check --wait`: native goes silent, next message returned | after native returns | 7701 | 8916 |

On `main`, `watch` and `check --wait` only reconnect natively, so a message sent while native IRC is down reaches them after it returns. For a silent link, `main` also first waits out the two-minute read timeout.

The silent cases are dominated by the keepalive: up to 5 seconds idle, plus a 3 second probe. A send detects silence in about 1.3 seconds because it is waiting for an answer.

## Regression checks

- **Benchmarks.** `BenchmarkAgentCLI` ([agentcli-benchstat.txt](agentcli-benchstat.txt), eight interleaved rounds) shows no significant change.
- **Connect benchmarks.** `pkg/irc`'s connect benchmarks ([connect-benchstat.txt](connect-benchstat.txt), eight rounds; [connect-rerun-benchstat.txt](connect-rerun-benchstat.txt), ten more) show no consistent change. Each pass had at most one row of ten significant, and a different row each time (+4.2%, p=0.004 in the rerun only). The connection uses about 100 more bytes (+0.2%), for the probe state.
- **Size.** Installed `airc` (`-trimpath -ldflags='-s -w'`) grows from 9,502,983 to 9,564,423 bytes, still under 10 MB. `airc-gateway` grows by 16 KB.
- **Tests.** `go test -race` passes for `./pkg/...`, `./cmd/...` and `./internal/server/...`. In `./integration/...` and `./internal/gateway/...` only the IPv6, POSIX message queue and UDP-Lite over IPv6 tests fail, and they fail the same way on `main` in this container.
