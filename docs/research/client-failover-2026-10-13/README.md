# Client failover: racing connections across carriers

Measured 13 October 2026 on the `client-failover` branch against `main`
(`4b6f1f7`), on a 4-vCPU Xeon with Go 1.27.0.

## What changed

`airc` races connection setup across native IRC and the gateway carriers the server advertised, happy-eyeballs style. The request itself is never raced. Native IRC is dialled first. The next carrier starts when the previous one fails or goes 250 ms without progress. The carrier that answered in native IRC's place is remembered for ten minutes and dialled first.

Before, a send or history read waited out the native connect budget (4 s) before trying another carrier. The `http` and `websocket` carriers also honour HTTP proxies. The commands that fall back now include search, the channel list and card reads. `docs/SERVICE.md` describes the behaviour.

## Failover latency

[latency-harness.go.txt](latency-harness.go.txt) runs in each tree's `cmd/airc` package against an in-process `aircd` and gateway (HTTP and WebSocket carriers). There were three interleaved rounds, 24 samples per row ([latency-samples.txt](latency-samples.txt)). "Cold" clears the remembered carrier before each command; "warm" keeps it. Times are in milliseconds.

| Scenario | main p50 | main max | branch p50 | branch max |
| --- | ---: | ---: | ---: | ---: |
| send, native healthy | 2.9 | 5.5 | 2.8 | 3.9 |
| history, native healthy | 1.1 | 2.9 | 1.1 | 1.6 |
| send, native port refused | 58.0 | 118.4 | 57.8 | 60.6 |
| history, native port refused | 2.3 | 3.5 | 2.5 | 6.0 |
| send, native port silent, cold | 4058.7 | 4069.5 | 309.4 | 311.5 |
| history, native port silent, cold | 4003.3 | 4007.2 | 253.8 | 259.4 |
| send, native port silent, warm | 4058.8 | 4061.4 | 57.4 | 59.8 |
| history, native port silent, warm | 4003.7 | 4004.4 | 2.7 | 3.4 |
| send, native goes silent after the post | 3065.8 | 3087.7 | 3062.8 | 3095.8 |

No command failed in either tree. The last row is unchanged by design: the post may have landed, so the client waits out its call timeout and then resolves the outcome over the gateway with the same request ID. Making that faster needs session continuity, which is out of scope here.

## Regression checks

- `BenchmarkAgentCLI` ([agentcli-benchstat.txt](agentcli-benchstat.txt)) and `pkg/irc`'s connect benchmarks ([connect-benchstat.txt](connect-benchstat.txt)): eight interleaved rounds, no significant change in time or allocations. Connect uses about 30 more bytes per connection (+0.06%), for the dialer field in `irc.Config`.
- Installed `airc` (`-trimpath -ldflags='-s -w'`) grows from 9,421,063 to 9,502,983 bytes, still under 10 MB.
- `go test -race` passes for `./pkg/...`, `./cmd/...` and `./internal/server/...`. In `./integration/...`, only the IPv6 and POSIX message queue tests fail, and they fail the same way on `main` in this container.
