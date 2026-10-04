# Gateway implementation status

## Scope

Implement the complete unranked 72-entry catalogue, with separate evidence for
each named protocol and mode. All AIRC operations each transport can support
are required, including replies, receipt/status lookup, history, and live
updates. Authenticated send alone is sufficient only with a concrete connection
limitation. A staged implementation or configured ingress-only adapter does
not reduce the final scope.

## Evidence

- Planning baseline: `66f8f2b`; pinned AIRC fixture: `96f16c3`.
- Plan capability requirements corrected to match the attached brief.
- Step 1 fixture implemented: immutable export checksum, isolated daemon
  build/start/stop, disposable account login, negotiated features, independent
  observer, accepted/persisted receipt, receipt lookup, and retained-ID retry.
  Reproduction commands and exact contract are in [upstream-contract.md](upstream-contract.md).
- Step 2 request contract implemented: strict bounded JSON parsing, typed targets,
  send/reply/read operation arguments, normalized explicit-intent fingerprints,
  and scoped stable upstream IDs. The golden fingerprint vector and malformed
  input tests pass. Configuration, result schemas, authentication, and durable
  default resolution remain pending. See [contract-v1.md](contract-v1.md).
- No gateway runtime, listener, authentication layer, delivery journal, or
  catalogue adapter has yet been implemented or verified.

## Remaining work

Step 2 is partial; Steps 3–10 remain. Every catalogue row and every member of a grouped row remains
**proposed**, with implementation and native acceptance evidence pending.
Before claiming any adapter complete, record each supported operation, each
actual transport limitation, the real client/device test, and platform status.
No hardware/platform blocker has yet been established by a concrete probe;
lack of an implementation is pending work, not a hardware blocker.

Independent review covers every task commit. The initial plan review found the
optional-capability contradiction, resolved by the plan corrections. Review of `cec33cb` passed fixture tests and
identified residual HTML/CoAP/ESP-NOW capability wording and missing reply
execution evidence. Those findings are corrected in the subsequent contract
change, including real reply receipt and observer assertions. Independent review
of `e01e6cb` found no parser or fixture defect and confirmed those corrections
against its committed blobs. Its remaining LoRaWAN profile precision finding
is addressed by requiring supported provisioned downlink/result operations;
BLE advertising likewise cannot infer a one-way device from lack of GATT.
Record subsequent review findings and their resolution alongside the relevant
implementation evidence.

Checks passed in the gateway worktree: `go test ./...`, `go test -race ./...`,
`go vet ./...`, targeted fixture/core tests, and `git diff --check`.

Publication is local only: no push, installation, deployment, or live AIRC
integration. The primary checkout, live accounts, state, and daemon remain
outside the fixture boundary.
