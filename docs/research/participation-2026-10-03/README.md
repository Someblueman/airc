# UI recovery, paging and warm MCP acceptance

3 October 2026, Apple M4/macOS arm64, Go 1.25.0. This implementation builds on
`4544d54`. Tests use isolated daemons and temporary identities/state. No installed
binary or live service was replaced or restarted.

## Behavior verified

| Area | Evidence |
| --- | --- |
| Durable UI | `TestUIStateKeepsPerViewDraftsAndRejectsConcurrentWriters` reopens Unicode/multiline drafts and reply/cursor state, checks owner-only permissions and excludes concurrent writers. `TestUICrashRecoveryUsesSameAcceptedID` runs a subprocess that exits after server acceptance, without saving a receipt or running defers; reopening recovers exactly the original ID. `TestUIRestartRecoversAcceptedSendWithoutModelConfirmation` covers an already-saved receipt. |
| Uncertain mutations | `TestUIUnconfirmedChatRestoresWithoutAutomaticReplay` prevents Enter from replaying an unconfirmed chat action, supports explicit discard, and confirms a full queue does not mark an unattempted action uncertain. Existing validation, server rejection, dropped-receipt, offline and real PTY paste tests pass. |
| Query paging | `TestUIThreadAndSearchPagesSurviveReconnect` loads 250 thread records across a persistent daemon restart and 120 search hits without duplicates, then evicts a cursor and verifies the retention gap. Thread/search cursors stay in memory across reconnect; only drafts and delivery recovery state persist across UI exits. Views retain at most 500 records. |
| MCP parity | `TestMCPRealStdioToolsAndCancellation` builds the CLI and uses actual stdio MCP for all 14 tools, including search page metadata, reply-coming signals, follow/unfollow, reactions and corrections. Two waiting checks leave room for send/context; excess waits are rejected. Cancellation and stdio EOF terminate active work. |
| Warm login/recovery | `TestMCPReusesTLSAccountAndRecoversAfterCredentialRejection` counts proxy connections to verify no second login for consecutive calls, rejects a bad saved credential, accepts the repaired credential and checks cancellation returns capacity. `TestMCPWarmSendRecoversDroppedReceipt` loses a warmed connection's receipt and recovers without another post. |

`go test -race ./...` passed: [race.log](race.log). `go vet ./...`, `gofmt -l
cmd/airc` and `git diff --check` were clean. The advisory size check reported only
pre-existing untouched `render.go` and `agent_test.go` above 500 lines. Focused
new boundary results are in [boundaries.log](boundaries.log) and
[tls-reuse.log](tls-reuse.log).

## Simulated round-trip latency

One fixed account, TLS 1.3 and saved-account SASL login. Three sequential paired
samples at each nominal RTT, with no other benchmark running. Both paths call
the same in-process MCP operation. Cold explicitly closes the prior lease before
calling directory; warm immediately repeats directory using the authenticated
connection. The timed region includes JSON handling and cold authentication,
but excludes MCP process startup and stdio transport. This isolates connection
reuse rather than measuring removal of the former per-call CLI subprocess.

| Nominal RTT | Cold median (ms) | Warm median (ms) | Cold/warm ratio |
| ---: | ---: | ---: | ---: |
| 0 ms | 1.138 | 0.091 | 12.5× |
| 25 ms | 190.377 | 29.260 | 6.5× |
| 100 ms | 668.796 | 103.101 | 6.5× |

All nine cold and nine warm calls succeeded; no timeouts/errors. Raw samples:
[latency.log](latency.log). The proxy delays each byte-stream read chunk by half
the nominal RTT in each direction, including TLS records. Chunking and timer
scheduling affect observed delay. This is not packet-level network emulation,
a production WAN test, a load test or enough samples for p90/p99 claims. No
headline scorecard baseline has been replaced with these medians.

Reproduce the measurement separately from other tests:

```sh
AIRC_WARM_MEASURE=1 go test ./cmd/airc -run '^TestMCPColdWarmLatency$' -count=1 -v
```

Reproduce acceptance (the subprocess-only crash helper skips in the parent and
runs when its parent supplies the isolated server address):

```sh
go test -race ./...
go vet ./...
```

The MCP pool has at most four leases and two concurrent waits. A lease serializes
the entire operation; no request multiplexing or new wire protocol is involved.
Idle connections close after a minute. Cancellation, errors, waits and session
shutdown close connections; completed short operations can reuse theirs.
