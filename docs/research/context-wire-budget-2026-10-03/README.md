# MCP capacity and context wire budgets — 2026-10-03

Local implementation and checks on Apple M4, macOS arm64, Go 1.25.0.
No running service was restarted, installed or deployed.

## Results

- Four total MCP calls remain allowed per adapter, with at most two waiting
  checks. Real stdio tests verify that a third wait is rejected while send,
  context and immediate check calls succeed. Cancellation and failed child
  starts release reservations; the total four-call cap still applies.
- Negotiated `CONTEXT_BYTES` bounds complete context responses, including summary,
  terminator, prefix and CRLF. A 1,000-record fixture with 3,500-byte bodies
  produced **29,951 wire bytes** under a 32 KiB budget, retaining six messages and
  reporting 994 omissions ([raw result](wire-size.txt)).
- Exact-fit boundaries, protected root/trigger/correction records, omission counts,
  Unicode and failure without partial output are checked. A real CLI regression
  covers missing intermediate correction links and final-JSON escaping expansion.
- SDK tests exercise capable and legacy peers: bounded calls fail locally if the
  capability is absent, and the published legacy API sends no new request field.
  CLI/MCP use the capability automatically and warn when falling back to local
  trimming on older daemons.

## Measurement

[Three benchmark samples](bench.txt) compare the same build with unbounded legacy
requests and a 32 KiB wire budget. The fixture contains 1,000 messages with
3,500-byte bodies. It measures in-process selection, encoding, enqueueing and
response parsing; it excludes connection setup, TLS and CLI startup.

| Request | Time per operation | Allocated bytes per operation |
| --- | ---: | ---: |
| Legacy unbounded | 82.8–85.1 ms | 89.18 MB |
| 32 KiB budget | 0.94–1.09 ms | 2.78 MB |

These are cumulative allocations, not resident memory or retained heap. The
bounded request returns fewer records with explicit omissions. This comparison
measures work avoided by upstream selection, not faster retrieval of all 1,000
records. Timings varied between development runs; this is a local fixture result,
not a network latency guarantee.

## Verification and reproduction

`go test -race ./...` passed ([output](race.txt)), as did `go vet ./...` and
`git diff --check`. Opt-in resource soaks/load campaigns were not rerun.
The final 30-second, two-worker `FuzzContextWireBudget` campaign passed 14,646
executions ([output](fuzz.txt)); it reused the local interesting-input cache.
The oracle repeatedly encodes and trims a complete response, independently of
production's incremental admission accounting. No failing corpus was found.

```sh
go test -race ./...
go vet ./...
go test ./internal/server -run '^TestContextBudgetRejectsBeforeSendingAndBoundsLargeRetrieval$' -v -count=1
go test ./internal/server -run '^$' -bench '^BenchmarkContextWireBudget$' -benchmem -count=3
go test ./internal/server -run '^$' -fuzz '^FuzzContextWireBudget$' -fuzztime=30s -parallel=2
```

See [protocol contracts](../../PROTOCOL.md), [agent usage](../../AGENT_RELIABILITY.md)
and [MCP limits](../../MCP.md). CHECK and thread wire budgets remain future work.
