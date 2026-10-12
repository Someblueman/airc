# Smaller binaries without lost features: sizes and benchmarks

Measured 12 October 2026. The goal was `airc` under 10 MB, `aircd` under 5 MB
and `airc-gateway` under 20 MB, as installed, without regressing any benchmark.
Base is `fcb0529` (main before this work); head is this branch. Every carrier
and command is still in the default build.

Verdict: **`airc` 11.32 → 9.42 MB (−16.8%) meets its goal. `aircd` 6.99 →
6.74 MB and `airc-gateway` 25.53 → 22.18 MB (−13.1%) do not, for the reasons
below.** No benchmark regressed. Some improved: mention scanning is 91–97%
faster, MCP gateway calls 13% faster, `airc` starts 7% sooner and the daemon's
peak RSS is 1.3% lower at 100 agents.

## What changed

| Change | Binary | Saves (installed, linux/amd64) |
| --- | --- | ---: |
| `internal/mcpserver`: a tools-only MCP server (stdio and Streamable HTTP) replaces the MCP Go SDK, and with it `jsonschema-go`, `segmentio/encoding`, `oauth2` and `encoding/gob` | `airc`, gateway | 1.44 MB, 1.12 MB |
| The gateway speaks gRPC itself (`grpc_wire.go`) on the `net/http` HTTP/2 server it already used, replacing grpc-go and protobuf-go | gateway | 2.22 MB |
| The bot calculator parses arithmetic itself instead of with `go/parser` | `airc` | 0.25 MB |
| `service install` no longer parses its generated gateway config in process; the installed gateway's `config validate` already checks it, and the tests parse what every carrier generates | `airc` | 0.22 MB |
| Mentions are found by two small scanners instead of `regexp`, the only regexps `aircd` linked | `aircd`, `airc-load` | 0.25 MB |

`-tags grpcnotrace` no longer changes any build, so `scripts/install.sh` and
`scripts/binary-size.sh` stop passing it. The `--check` budgets follow the new
sizes at about 1.5×: 14 MiB for `airc`, 10 MiB for `aircd` and `airc-load`,
32 MiB for the gateway.

**Behaviour kept.** Each replacement is checked against what it replaced:

- **MCP**: The MCP SDK's client still drives both MCP carriers in the tests,
  including the 20 `mcp_http` carrier-suite scenarios and the stdio
  cancellation test. `cmd/airc/testdata/mcp_discovery.json` pins `airc`'s
  `initialize` and `tools/list` output to what the SDK server produced, and it
  matches byte for byte. A wire probe of malformed and edge-case requests gave
  the same responses except two wordings: an unknown method now gets the
  standard JSON-RPC code −32601 instead of the SDK's 0, and a top-level array
  as tool arguments gets a different error text (both reject it).
- **gRPC**: Encodings equal protobuf-go's byte for byte. A fuzz target checks
  the decoder against protobuf-go on values, unknown fields and malformed
  input. All gRPC integration tests (malformed protobuf, flow-control
  pressure, slow reader, quotas, watch, replay) and the 15 carrier-suite
  scenarios pass with grpc-go clients.
- **Calculator and mentions**: Fuzz targets compare the calculator with the
  old `go/parser` version (3 M inputs) and the scanners with the old regexps
  (1.6 M inputs). Both reference versions stay in the tests.

## Sizes

[size-compare.txt](size-compare.txt), from
[size-fcb0529.txt](size-fcb0529.txt) (base's own script, so with
`grpcnotrace`) and [size-head.txt](size-head.txt). Installed, in MB:

| Binary | linux/amd64 | darwin/arm64 | linux/arm64 | gzip, linux/amd64 |
| --- | ---: | ---: | ---: | ---: |
| `airc` | 11.32 → **9.42** | 10.65 → 8.81 | 10.42 → 8.65 | 4.50 → 3.84 |
| `aircd` | 6.99 → **6.74** | 6.51 → 6.27 | 6.36 → 6.16 | 2.88 → 2.78 |
| `airc-gateway` | 25.53 → **22.18** | 24.44 → 21.11 | 23.99 → 20.78 | 10.03 → 8.89 |
| `airc-load` (dev tool) | 7.05 → 6.79 | 6.54 → 6.30 | 6.42 → 6.23 | 2.95 → 2.85 |

[composition-head.txt](composition-head.txt) attributes head's code by package.

### Why `aircd` stays above 5 MB

`aircd` serves TLS (`--tls-cert`/`--tls-key`, required off loopback). A program
that only listens and serves TLS 1.3 with `crypto/tls` is already 5.17 MB
stripped on linux/amd64. With TLS stubbed out, `aircd` would be 4.41 MB. The
rest of `aircd` (JSON, logging, flags, its own server) adds about 1.6 MB, and
replacing `encoding/json` throughout the server would still not reach 5 MB. The
owner chose to keep TLS in `aircd`. Moving TLS termination elsewhere (for
example into the gateway) or behind a build tag were the alternatives.

### Why the gateway stays above 20 MB

What remains is mostly the carriers' own protocol stacks: `modernc.org/sqlite`
for the journal (about 3.9 MB attributed), pion WebRTC (3.0 MB), quic-go
(1.4 MB), go-coap and `x/crypto/ssh` (SFTP/SCP). The 2026-10-11 removal
experiment measured WebRTC behind a build tag at −2.9 MB and the QUIC family
with CoAP at −2.5 MB. Replacing SQLite would need a new storage engine and a
journal migration. The owner chose to keep every carrier in the default build.

## Benchmarks

Same host and method as the [2026-10-11 study](../benchmarks-2026-10-11-final/README.md):
4-vCPU Xeon at 2.1 GHz, Go 1.27.0. Base and head were detached worktrees with
binaries and test binaries built once ([setup.sh.txt](setup.sh.txt)).
Rounds were interleaved and the order alternated each round. Load averages are
in each `*-progress.txt` and stayed between 1 and 2.5. `benchstat` gives every
p-value. The full race suite passed except tests this container cannot run on
base either: IPv6 sockets, POSIX message queues (`/dev/mqueue`) and UDP-Lite
over IPv6. A UART PTY test failed once under suite load and passed alone three
times.

### Micro-benchmarks

12 rounds ([round.sh.txt](round.sh.txt), [micro-benchstat.txt](micro-benchstat.txt)),
then 10 more of every case that came out significant
([rerun.sh.txt](rerun.sh.txt), [rerun-benchstat.txt](rerun-benchstat.txt)).
Allocations are unchanged everywhere ([micro-benchstat-all-units.txt](micro-benchstat-all-units.txt)).

| Case | First pass (12) | Rerun (10) | Verdict |
| --- | --- | --- | --- |
| `Mentions` (scratch benchmark, [mentions-benchmark.go.txt](mentions-benchmark.go.txt)), tagged / addressed / long | 5.6 / 4.4 / 12.6 µs → 0.25 / 0.38 / 0.43 µs, −91 to −97% (p=0.000) | — | Faster: the scanners |
| `Mentions`, plain text | 36.5 → 36.9 ns (p=0.76) | — | Unchanged |
| `AgentCLI/send`, `wait_wake` | −3.9% (p=0.033), −4.5% (p=0.039) | −3.1% (p=0.17), −5.2% (p=0.063) | Same or faster |
| `History/EmptyInbox` | −4.4% (p=0.033) | +6.1% (p=0.39) | Noise |
| `ContextWireBudget/0` | +6.4% (p=0.008) | +2.4% (p=0.075) | Did not reproduce |
| `ValidHistoryID/invalid-first`, `invalid-last` | +7.0% (p=0.010), −7.0% (p=0.032) | ~ (p=0.12, 0.90) | Did not reproduce |

`internal/server` and `internal/gateway/backend` are identical in base and head,
and the context path does not call the mention scanner. So the two cases that
did not reproduce are code layout or noise, as `ValidHistoryID` was in the
earlier study. Every other case is unchanged: `History/*`,
`HistoryAddWithQuota`, `NoteFailedAddress`, `ContextWireBudget/32768`,
`HistoryPage/*`, every `Connect` and `GatewayConnect` time,
`UIAppendRedraw`, and `AgentCLI/login` and `empty_check_four_rooms`.

### Rewritten carriers

[carrier-latency-harness.go.txt](carrier-latency-harness.go.txt) runs in each
tree's `integration/gateway` package with its fixtures. It makes 400
sequential calls with the grpc-go client and with the MCP SDK client to a
gateway process built from that tree (TLS). Capabilities or `status` calls
measure the carrier; 40 sends measure the full path through the journal and
aircd. 12 rounds ([carrier-benchstat.txt](carrier-benchstat.txt)), then 10 of
the MCP case ([rerun-carrier-benchstat.txt](rerun-carrier-benchstat.txt)):

| Call | Base mean | Head mean | Change |
| --- | ---: | ---: | --- |
| gRPC `Capabilities` | 562 µs | 520 µs | ~ (p=0.089) |
| gRPC `Call` send | 4.51 ms | 4.59 ms | ~ (p=0.98) |
| MCP `status` | 699 µs; rerun 746 µs | 675 µs; rerun 651 µs | ~ (p=0.13); rerun −12.7% (p=0.000), p90 −17.6% |
| MCP `send` | 4.58 ms | 4.52 ms | ~ (p=0.80) |

The p99s are unchanged.

### CLI startup

400 alternating launches of `airc help` ([startup-benchstat.txt](startup-benchstat.txt)):
4.84 → 4.50 ms, −7.1% (p=0.005). `airc` no longer initializes the MCP SDK's
packages or the gateway validators' parser.

### Load generator

`airc-load`, five repetitions of each case, order alternating
([load-benchstat.txt](load-benchstat.txt), [load-summary.csv](load-summary.csv)).
Every scheduled slot completed.

| Case | Post p50 (ms) | Post p99 (ms) | ops/s | Server CPU (s) | Peak RSS (MiB) |
| --- | ---: | ---: | ---: | ---: | ---: |
| persistent, 100 agents | 1.065 / 1.061 (p=0.84) | 4.24 / 3.27 (p=0.22) | 99.66 / 99.66 | 2.90 / 2.87 (p=1.0) | 24.86 / 24.54, −1.3% (p=0.008) |
| memory, 500 agents | 0.643 / 0.626 (p=0.55) | 2.64 / 2.47 (p=0.84) | 498.1 / 498.2 | 17.3 / 16.6 (p=0.42) | 59.7 / 58.8 (p=0.087) |

Server allocation fell 0.05–0.09% (p=0.008), the mention scanners' allocations.

### Gateway send (HTTP)

The earlier harness at shipped flood limits, 10 rounds
([send-benchstat.txt](send-benchstat.txt), [send.jsonl](send.jsonl)). Nothing
changed significantly: c8 139 → 142 sends/s (p=0.63), c1 133 → 136 (p=0.32),
guest c8 147 → 151 (p=0.19); admission p50 4.2 ms in both. No send failed.

## Files

- `size-*.txt`: `scripts/binary-size.sh` output and comparison.
  [size-check.txt](size-check.txt) is head's `--check` against the new budgets.
- `micro-*`, `rerun-*`, `carrier-*`, `startup-*`, `load-*` and `send*`: raw
  results, benchstat output and load averages per round.
- `*.sh.txt`, [carrier-latency-harness.go.txt](carrier-latency-harness.go.txt)
  and [mentions-benchmark.go.txt](mentions-benchmark.go.txt): the scratch
  harnesses. They are not part of the repository.
  [setup.sh.txt](setup.sh.txt) also copies the earlier study's
  `sendbench-head.go.txt` into each tree.
