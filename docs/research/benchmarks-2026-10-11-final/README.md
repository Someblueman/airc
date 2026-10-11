# After PRs #1–#21: benchmarks and binary size

Measured 11 October 2026, once PRs #1–#21 had merged. These brought gateway
IRC-style identity, WEBIRC source forwarding and rate limits, the bundled
gateway install, client transport fallback, the bridge and mode removals, a
journal schema squash, one acceptance path, test consolidation and the gateway
transport registry. Base is `9d37a96`, main before the work. Head is
`94bfd1a`, main now. The [earlier check](../benchmarks-2026-10-11/README.md)
compared the same base with `18c00f8` (after #1), then the fixes that followed.

Verdict: **server, load and gateway timings are unchanged or better.** The
gateway send path is faster than base: 11–18% more sends a second at 8
concurrent posters, and an idle send is observed in 7 ms rather than 200 ms.
One small regression reproduced: every `airc` command starts about 0.4 ms
later (+9% of bare startup), because `airc` now links gateway validation
packages that do work at package init. A small fix is proposed below. The
allocation increases are the per-login ones the earlier check already
explained. `airc` grew 16% (2.3 MB), mostly from client transport fallback
(#3). `airc-gateway` shrank 7% (3.5 MB), mostly from #1's mode removals and
the bridge removal (#6). `aircd` is unchanged.

## Environment and method

Same host as the earlier check: 4-vCPU Intel Xeon at 2.10 GHz, 15 GiB RAM,
linux/amd64, ext4, Go 1.27.0, GOMAXPROCS 4. **The machine was shared:** another
worker ran CoAP tests and benchmarks throughout. Load averages ran from 0.3 to
5 and are recorded in each `*-progress.txt`. Only differences inside an
interleaved comparison mean anything.

- Base and head were checked out as detached worktrees. Test binaries and
  executables were built once per tree with `go test -c` and `go build`.
  `pkg/irc/connect_benchmark_test.go` was copied into the base tree, as in the
  earlier check, so `BenchmarkConnect` runs on both.
  `BenchmarkNoteFailedAddress` and `BenchmarkGatewayConnect` exist only on head.
- Micro-benchmarks: 12 interleaved rounds, with base or head first in
  alternate rounds ([round.sh.txt](round.sh.txt)). Every case that came out
  significant, or nearly so, then ran 10 more rounds ([rerun.sh.txt](rerun.sh.txt)).
- `airc-load`: persistent history with 100 agents, and memory-only with 500
  agents (30 s measured, 5 s warmup, rate 1). Five repetitions, order
  alternating ([load.sh.txt](load.sh.txt), [load2bench.py.txt](load2bench.py.txt)).
- Gateway end to end: the earlier harness, unchanged
  ([base](sendbench-base.go.txt), [head](sendbench-head.go.txt)). A scratch
  `aircd` listens on a Unix socket with fsync history, and the gateway's HTTP
  listener uses a fresh journal. Times run from POST until an independent
  native observer sees the message. Head's configuration still compiles and
  runs unchanged after #6, #12 and #20. Head now runs at its **shipped flood
  limits** (1200 author and 2400 source requests a minute) with no `-unlimited`
  flag. No send was refused. Ten rounds, order alternating, then ten more
  ([send.sh.txt](send.sh.txt), [send2bench.py.txt](send2bench.py.txt)). The
  harness's Unix socket path must stay under 108 bytes, so `TMPDIR` is a short
  symlink into the scratch directory.
- `benchstat` (golang.org/x/perf) produced every p-value. Load and gateway
  metrics were fed to it as one custom-unit sample per repetition.

## Binary size

### How the binaries are installed

README.md and docs/SERVICE.md install with a plain
`go build -o ~/.local/bin/NAME ./cmd/NAME`, for `aircd`, `airc` and (for
`airc service install --gateway`) `airc-gateway`. There are no `-ldflags`, no
`-trimpath` and no build tags. `airc service install` does not build anything:
it records the paths it finds. The only embedding is the agent skill
(`skills/embed.go`, about 23 kB in `airc`). The integration tests build the
gateway with `-trimpath`, but no install path does. A host build uses the
toolchain's default `CGO_ENABLED=1` on Linux, so the binaries link libc
dynamically (for the `net` and `os/user` resolvers). Cross builds use cgo 0.
`airc-load` is a development tool and is not installed.

`scripts/binary-size.sh` (added here) measured each binary four ways at each
commit, on linux/amd64 (cgo 1 and cgo 0) and cross-built for darwin/arm64 and
linux/arm64 ([size-all.sh.txt](size-all.sh.txt); raw results are in
`size-<commit>.txt`).

### Sizes

As installed, linux/amd64, in MB (10⁶ bytes). The commits are base, after #1,
the bridge-removal merge (#6), the registry merge (#20), and now.

| Binary | `9d37a96` | `18c00f8` #1 | `0c07095` #6 | `1307953` #20 | `94bfd1a` | Now vs base |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `airc` | 14.29 | 14.81 | 16.36 | 16.56 | 16.54 | +15.8% |
| `aircd` | 10.26 | 10.30 | 10.30 | 10.30 | 10.30 | +0.3% |
| `airc-gateway` | 48.00 | 45.30 | 44.32 | 44.57 | 44.53 | −7.2% |
| `airc-load` (dev tool) | 10.39 | 10.41 | 10.41 | 10.41 | 10.41 | +0.2% |

Base and now on every platform. Columns are as installed, then
`-trimpath -ldflags='-s -w'`, then that stripped build's `gzip -9` size. All
are in MB.

| Binary | Platform | Installed | Stripped | Stripped gzip |
| --- | --- | ---: | ---: | ---: |
| `airc` | linux/amd64 | 14.29 → 16.54 | 9.72 → 11.32 | 3.86 → 4.50 |
| | darwin/arm64 | 13.45 → 15.64 | 9.09 → 10.65 | 3.57 → 4.18 |
| | linux/arm64 | 13.17 → 15.30 | 8.91 → 10.42 | 3.44 → 4.02 |
| `aircd` | linux/amd64 | 10.26 → 10.30 | 6.96 → 6.99 | 2.87 → 2.88 |
| | darwin/arm64 | 9.69 → 9.70 | 6.50 → 6.51 | 2.64 → 2.65 |
| | linux/arm64 | 9.41 → 9.42 | 6.36 → 6.36 | 2.54 → 2.55 |
| `airc-gateway` | linux/amd64 | 48.00 → 44.53 | 32.87 → 30.39 | 12.67 → 11.71 |
| | darwin/arm64 | 45.24 → 43.03 | 30.73 → 29.17 | 11.70 → 11.07 |
| | linux/arm64 | 45.08 → 41.89 | 30.67 → 28.44 | 11.55 → 10.68 |
| `airc-load` | linux/amd64 | 10.39 → 10.41 | 7.03 → 7.05 | 2.94 → 2.95 |

With cgo 0, linux/amd64 binaries are 0.07–0.19 MB smaller. `-trimpath` alone
saves 0.2–0.5%. Stripping (`-s -w`) alone saves 31–32% of every binary, and
halves the gzip size.

**Where the change came from.** `airc` grew 0.52 MB with #1, which brought the
bundled install's gateway configuration into `airc service install`. It grew
1.55 MB more by `0c07095`, mostly client transport fallback (#3). A
go-size-analyzer diff of base and head `airc` shows +626 kB in `net`
(`net/http` client, HTTP/2, `hpack`), +471 kB in airc's own packages
(`pkg/fallback`, `internal/gateway/config` and `core`, `internal/service`),
+120 kB in `compress/flate` and +107 kB for `github.com/coder/websocket`.
`airc-gateway` lost 2.7 MB with #1, which removed the gateway's connection
modes, principals and bindings. It lost 1.0 MB more by #6 (bridges). Its own
code went from 5.0 MB to 3.7 MB. Every third-party dependency stayed the same
size except go-coap, which grew 136 kB. #20 added 0.25 MB.

### What dominates each binary

[composition.txt](composition.txt) gives the go-size-analyzer attribution for
head on linux/amd64, as installed and grouped by module. The analyzer was
installed into the scratch directory, not added to go.mod
([gsasum.py.txt](gsasum.py.txt) groups its JSON output). In every binary about
31% of the file is DWARF and symbol tables, which `-s -w` removes. The rest:

- **`aircd`** (7.4 MB attributed): the standard library is 6.3 MB of it,
  mostly the runtime, `crypto/tls` and `x509`, and `encoding/json/v2`. Its own
  server code is 0.45 MB, and it has no third-party dependency. Little can be
  gained beyond stripping.
- **`airc`** (12.1 MB): the standard library is 8.1 MB. The rest is the CLI
  itself (0.93 MB), the MCP SDK with its JSON schema and `segmentio/encoding`
  dependencies (about 1.1 MB, plus `encoding/gob`, which only the SDK
  imports), the fallback carriers (`net/http`, `coder/websocket`), and the
  gateway configuration and contract packages (0.33 MB).
- **`airc-gateway`** (32.3 MB): the standard library is 10.6 MB.
  `modernc.org/sqlite` and its `libc` (the journal) are 3.9 MB. The pion WebRTC
  stack is 3.5 MB. gRPC with protobuf is 2.8 MB. quic-go with HTTP/3 and
  WebTransport is 1.7 MB. The gateway adapter package is 1.4 MB, go-coap
  0.8 MB, and the MCP SDK 0.6 MB. Those figures are code and data, but they
  understate gRPC's real cost, described below.

**Does `airc` link the gateway?** Not its runtime. `internal/service` imports
`internal/gateway/config` to write `gateway.json` and validate the carrier
list for `airc service install --gateway`. That brings in `gateway/core`,
`jsoninput` and five wire-format validators (`bpv7`, `mqttwire`,
`pgnotifywire`, `snmpwire`, `xmppwire`). `config/webrtc.go` calls
`stun.ParseURI`, which puts `pion/stun`, `pion/dtls` and `pion/transport` in
`airc`'s import graph, but the linker keeps only 7 kB of them. No adapter,
service, store, SQLite, gRPC, QUIC or WebRTC code is linked. The CLI does pull
in `go/parser` for the bot calculator utility (`pkg/bot/utility.go`, about
0.2 MB with `go/ast`).

**gRPC switches off method dead-code elimination for the whole gateway.**
`google.golang.org/grpc` imports `golang.org/x/net/trace`. That imports
`html/template`, and so `text/template`, whose `reflect.Value.MethodByName`
call makes the linker keep every exported method of every reachable type.
`go build -ldflags=-dumpdep` shows `text/template.(*state).evalField
<ReflectMethod>` on head. With `-tags grpcnotrace`, a tag gRPC provides for
exactly this, no `<ReflectMethod>` caller remains. The gateway never enables
gRPC tracing and never serves `http.DefaultServeMux`, where `x/net/trace`
registers its `/debug/requests` page, so the tag changes nothing it uses.
`airc` and `aircd` do not have this problem.

### Size opportunities

These are for the owner to decide; nothing here changes production code. The
savings were measured on linux/amd64, as installed and then stripped with
`-trimpath -ldflags='-s -w'`. Carrier removals were measured by deleting a
group's files from a scratch copy and stubbing what remained
([strip.py.txt](strip.py.txt), results in
[gateway-carrier-removal.txt](gateway-carrier-removal.txt)). A build tag
would remove the same imports.

| Opportunity | Binary | Saves (installed / stripped) | Cost |
| --- | --- | ---: | --- |
| Build with `-trimpath -ldflags='-s -w'` in the documented install | all | `airc` 5.2 MB, `aircd` 3.3 MB, gateway 14.1 MB (31–32%) | No DWARF for debuggers. Panics still show file:line. |
| Build the gateway with `-tags grpcnotrace` | gateway | 7.0 / 4.8 MB (16%) | Install docs must name the tag. No behaviour change. |
| Build tag for the WebRTC carrier (pion) | gateway | 4.2 / 2.9 MB more | Carrier absent from that build. |
| Build tag for gRPC with protobuf | gateway | 3.4 / 2.3 MB more | Same. |
| Build tag for the QUIC family (HTTP/3, WebTransport, raw QUIC) | gateway | 2.4 / 1.6 MB more | Same. |
| Build tag for MCP over HTTP | gateway | 1.8 / 1.2 MB more | Same. |
| Build tag for CoAP, DTLS and the OSCORE RPC | gateway | 1.3 / 0.9 MB more | Same. OSCORE needs a stub. |
| All five carrier groups out, with `grpcnotrace` | gateway | 37.5 → 23.0 / 25.6 → 15.6 MB | A "core carriers" build. |
| MCP adapter behind a tag, or a separate binary | `airc` | 2.4 / 1.7 MB (15%) | `airc mcp` is a shipped feature. |
| Validate STUN URIs without `pion/stun`; split the listener table from full validation | `airc` | about 0.2–0.3 MB | Small. It mainly untangles the import graph. |

The "more" rows are measured against a `grpcnotrace` build. Their effects add
up roughly: all five groups out saves 14.5 MB against their 13.1 MB sum. The
SQLite journal (3.9 MB) is the gateway's store and is not an option. Taking
stripping, `grpcnotrace` and `-trimpath` together, the full gateway goes from
44.5 MB to 25.6 MB (10.1 MB gzipped, from 23.0 MB) with no feature lost.

### Measuring size from now on

```sh
scripts/binary-size.sh > size.txt                    # host, all four builds
scripts/binary-size.sh --target linux/amd64 --target darwin/arm64 --target linux/arm64
scripts/binary-size.sh -C /path/to/old/worktree > old.txt
scripts/binary-size.sh --compare old.txt size.txt    # exact deltas
scripts/binary-size.sh --check                       # fail over budget
```

The output is in Go benchmark format, with units `bytes` and `gzip-bytes`, so
`benchstat` can read it. Builds are deterministic, so there is one sample per
build. benchstat needs six samples a side before it shows a delta, which is why
`--compare` prints the exact deltas itself. `--check` fails when a host binary,
as installed, exceeds a generous budget: 24 MiB for `airc`, 16 MiB for `aircd`
and `airc-load`, 64 MiB for the gateway. That is about 1.5× today's sizes, so
the check catches a new heavy dependency rather than ordinary growth (gRPC
brought into `airc` would trip it). It is opt-in and outside `go test`, so the
normal suite never pays for the builds. A cold host run takes a few minutes.
See also [the scorecard](../../PERFORMANCE_SCORECARD.md#binary-size).

## Benchmark results

### Micro-benchmarks

[micro-benchstat.txt](micro-benchstat.txt) has 12 rounds and
[rerun-benchstat.txt](rerun-benchstat.txt) has 10 more of the flagged cases.
**Unchanged** in both passes (p ≥ 0.05): `BenchmarkHistory/RecentChannel` and
`OldCursor`, `BenchmarkHistoryAddWithQuota`, `BenchmarkContextWireBudget/*`,
`BenchmarkUIAppendRedraw`, gateway `BenchmarkHistoryPage/*`, every
`BenchmarkConnect` time, and `AgentCLI/send` and `wait_wake`.
`BenchmarkNoteFailedAddress` (148 ns known, 527 ns full table) and
`BenchmarkGatewayConnect` (267–457 µs) are head-only and match the earlier
fixed figures.

| Case | First pass (12 rounds) | Rerun (10 rounds) | Verdict |
| --- | --- | --- | --- |
| `AgentCLI/empty_check_four_rooms` | 7.40 → 7.77 ms, +4.9% (p=0.114) | 7.21 → 7.65 ms, +6.2% (p=0.004) | Real: CLI startup, below |
| `AgentCLI/login` | 6.05 → 6.26 ms, +3.6% (p=0.060) | +2.0% (p=0.28) | Same cause, within noise |
| `History/EmptyInbox` | 252.5 → 263.4 µs, +4.3% (p=0.028) | 255.5 → 267.3 µs, +4.6% (p=0.015) | Code layout, not code |
| `History/MentionInbox` | ~ (p=0.80) | +5.1% (p=0.043) | Did not reproduce |
| `ValidHistoryID/invalid-first` | −17.0% (p=0.000) | −17.9% (p=0.000) | Code layout, as in the earlier check |
| `ValidHistoryID/invalid-last` | −4.6% (p=0.014) | −4.9% (p=0.009) | Code layout |

**History scan: layout only.** `internal/server/history.go` did not change
between base and head. `go tool objdump` of every `(*historyRing)` method gives
identical instructions in both test binaries. Only their addresses differ:
`matchesAt`, the per-message predicate of the 10,000-message scan, moved from a
64-byte-aligned address to one 32 bytes past it. The 12 µs is real for these
two test binaries and says nothing about the code. `ValidHistoryID` is the same
effect in the other direction.

**CLI startup: a small real regression, reproduced.** Every `airc` command
now starts about 0.4 ms later. Over 400 alternating launches of `airc help`
([startup.py.txt](startup.py.txt), [startup-benchstat.txt](startup-benchstat.txt)),
startup went from 4.43 to 4.82 ms (+8.9%, p=0.000). `GODEBUG=inittrace=1`
([inittrace-base.txt](inittrace-base.txt), [inittrace-head.txt](inittrace-head.txt))
puts 0.30 ms of that in package initialisation: 1.24 ms on base and 1.54 ms on
head, with 13 more packages. Most of the extra comes from gateway packages that
`airc` links only so that `service install --gateway` can validate a gateway
configuration (see [Does airc link the gateway?](#what-dominates-each-binary)):

- `internal/gateway/bpv7` takes 0.18 ms. `crc.go:8` builds the Castagnoli
  table eagerly with `crc32.MakeTable` at package init.
- `internal/gateway/xmppwire` takes 0.05 ms. `stream.go:12` compiles a
  regular expression at package init.
- `gateway/config`, `pion/dtls` and `pion/transport` take 0.04 ms together.

The rest is the larger binary. This dates from #1, which made
`internal/service` import `gateway/config`. That also explains the earlier
check's first-pass AgentCLI slowdown, which did not reach significance there.
`AgentCLI/empty_check_four_rooms` runs one CLI process, so its +0.44 ms is the
same cost.

**Proposed fix (not applied here):** build both values lazily with
`sync.OnceValue`, as in [lazy-init-experiment.diff.txt](lazy-init-experiment.diff.txt);
`decode_test.go` would need the same rename. In a scratch build, startup fell
from 4.77 to 4.46 ms (−6.5%, p=0.004, [startup-lazy-benchstat.txt](startup-lazy-benchstat.txt)),
recovering most of the 0.4 ms. A fuller fix keeps the gateway's wire-format
validators out of `airc` (the last size opportunity above).

**Allocations, by design and as before:** each SASL login allocates 12 more
times and 0.7 KiB more (`Connect/account_sasl_ephemeral` +5.0%), and each
`AgentCLI` command allocates 6 more times. Both come from the 779 numeric, as
the earlier check showed. Every connect allocates about 50–90 B more.
`UIAppendRedraw` allocates 1.3 KiB less (−0.2%).

### Load generator

No significant difference in any metric
([load-benchstat.txt](load-benchstat.txt), [load-summary.csv](load-summary.csv)).
Every scheduled slot completed in every run.

| Case | Post p50 base / head (ms) | Post p99 base / head (ms) | ops/s | Server CPU s | Peak RSS (MiB) |
| --- | ---: | ---: | ---: | ---: | ---: |
| persistent, 100 agents | 0.992 / 0.988 (p=1.0) | 3.06 / 2.96 (p=0.69) | 99.7 / 99.7 | 2.73 / 2.77 (p=0.55) | 24.7 / 24.7 |
| memory, 500 agents | 0.617 / 0.599 (p=0.095) | 2.31 / 2.16 (p=0.69) | 498.1 / 498.2 | 16.4 / 16.0 (p=0.42) | 60.1 / 59.2 (p=0.087) |

### Gateway send

[gateway-send-benchstat.txt](gateway-send-benchstat.txt) and its rerun
[gateway-send-rerun-benchstat.txt](gateway-send-rerun-benchstat.txt) each have
10 rounds. Head runs with its shipped limits.

| Metric | base | head | Change, first / rerun |
| --- | ---: | ---: | --- |
| registered c8 sends/s | 125.9 | 148.4 | +17.9% (p=0.000) / +10.9% (p=0.002) |
| registered c8 admit p50 (ms) | 4.19 | 4.07 | ~ (p=0.32) / ~ (p=0.17) |
| registered c8 gap p50 (ms) | 6.87 | 6.48 | ~ (p=0.12) / ~ (p=0.11) |
| registered c8 gap p90 (ms) | 8.39 | 7.99 | −4.7% (p=0.037) / ~ (p=0.48) |
| c1 observed p50 (ms) | 199.9 | 7.19 | −96.4% (p=0.000) / −96.4% (p=0.000) |
| c1 admit p50 (ms) | 4.37 | 3.88 | −11.1% (p=0.000) / −14.8% (p=0.000) |
| guest c8 sends/s, admit p50 (ms) | — | 155.9, 4.07 | 155.8, 4.15 in the rerun |

The lane's per-job service time (the c8 gap) and HTTP admission now match
base. These were the two regressions the earlier check found after #1 and
fixed afterwards, and they stay fixed. The throughput gain and the c1 latency
are deliberate: since `1ed3a00`, admission wakes an idle delivery runner, so a
send no longer waits up to 200 ms for the poll ticker. At c8 the per-job gap is
unchanged (its p99 is about 11–12 ms in both trees), so the extra throughput is
consistent with that same wake-up: base's runner is idle after the sequential
warm-up and waits for a tick before the burst starts. The gap p90 improvement
did not reproduce.

## Files

- `size-<commit>.txt`: raw `scripts/binary-size.sh` output for each commit,
  all targets, plus linux/amd64 with cgo 0.
  [size-compare-9d37a96-94bfd1a.txt](size-compare-9d37a96-94bfd1a.txt) gives
  the deltas.
- [composition.txt](composition.txt): per-module attribution.
  [gateway-carrier-removal.txt](gateway-carrier-removal.txt) has the
  carrier-removal experiment, made with [exp.py.txt](exp.py.txt),
  [strip.py.txt](strip.py.txt), [strip.sh.txt](strip.sh.txt) and
  [strip-measure.sh.txt](strip-measure.sh.txt).
- `micro-*`, `rerun-*`, `load-*` and `gateway-send*`: raw benchmark output,
  benchstat comparisons and load averages per round.
- `startup-*`, `head-vs-lazy.*`, `inittrace-*` and
  [lazy-init-experiment.diff.txt](lazy-init-experiment.diff.txt): the CLI
  startup measurement and the proposed fix's scratch build.
