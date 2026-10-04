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
  input tests pass. Result states/errors preserve unknown receipt facts; trusted
  capability sets intersect with default deny. See [contract-v1.md](contract-v1.md).
- Step 2 configuration and local CLI implemented: bounded strict schema, explicit
  identity/binding references and allowlists, credential-file permission checks,
  retry/retention/resource limits, disabled-by-default instances, `config validate`
  and `capabilities`. The real `go run` commands validate the disabled example
  without state/listeners. The CLI now advertises compiled partial HTTP intake;
  only implemented HTTP operations can be enabled. See [configuration.md](configuration.md).
  HTTP credential snapshots and durable defaults/binding enforcement are
  implemented below; other adapter-native schemas remain pending.
- Step 3 bearer primitive and local provisioning implemented: native Unix
  descriptor ownership/permission checks, bounded token reading, crypto-random
  exclusive file creation, constant-time hash verification, immutable principal
  permissions, configured revocation, and rotation preserving stable operation
  IDs. Focused race tests and real credential-init/no-overwrite CLI processes
  passed. Native behavior is verified on macOS; Linux/Windows cross-builds passed,
  with non-Unix credential operations explicitly unavailable pending native ACL
  implementation. See [authentication.md](authentication.md).
- A partial HTTP runtime and delivery pipeline are implemented as described below;
  no complete catalogue adapter or authentication-flood acceptance is claimed.
- Step 3 signed/encrypted codecs implemented: pinned JOSE library, strict fixed
  Ed25519 JWS and nested ECDH-ES/P-256/A256GCM JWE, provisioned-key file checks,
  byte verification before normalization, audience/validity bounds and exact
  permissions. Published Ed25519 and library JWE/ECDH vectors and negative codec
  tests pass. See [signed-message-v1.md](signed-message-v1.md). This does not
  implement durable replay admission or ingress authentication acceptance.
- Step 4 journal ownership/intake APIs implemented: private local SQLite files,
  single-owner lock, verified WAL/FULL durability pragmas, schema versioning,
  pinned bindings, transactional coalescing/replay/quota recording, scoped status,
  and checkpoint backpressure. Actual process-exit, deferred commit failure,
  SQLite-full and pinned-WAL tests pass. See [journal-v1.md](journal-v1.md).
  Worker wiring, retention maintenance and partial HTTP runtime are described below.
- Steps 3/4 durable revocation and attempt-grant APIs implemented: versioned
  migration, authorization generation, shared transaction gate, independent
  budget reservations, one-use deadline-bound handles and old-marker recovery.
  SQLite/process/race tests cover ordering, failure and persistence. See
  [dispatch-grants.md](dispatch-grants.md). Bounded worker completion/scheduling are implemented below; full acceptance remains pending. Network attempt
  enforcement is described in the Step 5 evidence below.
- Step 4 acceptance primitive implemented: full correlated SDK receipts save
  transactionally with attempt fencing, independent receipt facts and private
  retry copies on save failure. Real pinned-daemon send/reply receipts, independent
  observation, reopen/coalescing and process-exit tests pass. See
  [acceptance.md](acceptance.md). Bounded backend completion and scheduler wiring are implemented below; no catalogue adapter is claimed.
- Step 4 failed-attempt completion implemented: version 3 migration, durable
  prior-acceptance uncertainty, sticky rejection, separate persisted backoff and
  crash-safe lookup reservation accounting. SQLite, abrupt-exit and real SDK
  unknown-receipt/resend tests pass. See [completion.md](completion.md).
  Worker completion wiring and idle expiry are described below; the partial HTTP service lifecycle is described below.
- Step 5 bounded SDK send/retained-lookup backend implemented: immutable account
  and TLS snapshots, grant/binding verification, exact feature checks, exclusive
  event ownership, phase/overall deadlines, joined cleanup and bounded session
  slots. Actual backend send/recovery, observer, stalled-registration,
  withheld-receipt/continuous-traffic, feature-value and TLS-relay checks pass.
  See [backend.md](backend.md). Native TLS listener acceptance, blocked-write injection, richer reads and
  delivery workers remain
  pending; no catalogue adapter is claimed.

- Step 5 reply backend implemented: bounded exact-parent context retrieval,
  room/DM participant authorization, verified root correlation and the same
  stable-ID acceptance path. Real room/nested/DM replies, retained reply lookup,
  zero-write permission denials, parent-read timeout, corrupted-root uncertainty
  and pre-revocation grant completion pass. See [replies.md](replies.md).
  Partial HTTP runtime reply routing uses the same dispatcher and worker path.

- Step 6 delivery worker primitive implemented: bounded per-principal execution,
  rotating due selection, reserve/backend/completion wiring and joined shutdown.
  Real automatic outage recovery after caller cancellation, lost-result journal
  reopening/receipt recovery and cancellation during receipt withholding pass.
  See [delivery.md](delivery.md). Ingress/lifecycle wiring and complete Step 6
  acceptance remain pending; no adapter is claimed.

- Step 4/6 maintenance implemented: idle no-write expiry releases pending quota;
  uncertainty and active grants survive; resolved results honor their stored
  retention and replay markers honor the inclusive validity/skew boundary.
  Boundary/reopen/late-acceptance/commit-rollback tests and a real busy-lease idle
  expiry/quota-release scenario pass. See [maintenance.md](maintenance.md).

- Steps 6/7 shared ingress dispatcher and partial HTTP primitive implemented:
  authenticated mutation/scoped-status routing, three-profile coalescing and
  durable revocation, bounded protocol server and durable HTTP `202`.
  Real TLS HTTP/1.1 and HTTP/2 clients, independent post/reply observation,
  scoped status and denial boundaries pass. See [http.md](http.md).
  CLI/config lifecycle, richer HTTP operations and complete adapter acceptance
  remain pending; catalogue rows 13/15/17 are partial, not complete.

- Authenticated HTTP capability discovery implemented: current configured and
  credential permissions intersect with ingress operations; durable revocation
  applies; only caller targets, limits and new-job defaults are returned.
  Upstream compatibility remains explicitly unknown. Real HTTP/2 scoped/revoked
  discovery and permission-snapshot tests pass. See [http.md](http.md).

- HTTP service lifecycle and `serve` CLI implemented: credential/TLS snapshots,
  all-listener bind rollback, shared bounded ingress connections, signal-driven
  stop and joined handler/worker/journal ownership. An owned race-enabled process
  passes real HTTPS HTTP/2 post/status/observer acceptance and SIGINT shutdown;
  the listener and journal lock are released. See [service.md](service.md).
  Real HTTPS outage admission and graceful CLI restart with the same journal also
  pass: pending attempts/expiry survive and delivery resumes after restoring the
  isolated upstream, without resubmission. Abrupt/uncertain-send process restart,
  load/first-use acceptance and richer HTTP remain pending.

## Remaining work

Step 8 has a bounded room-history backend primitive: one-use current-authorized
leases, exclusive SDK CHECK sessions, streaming byte/count limits, exact record
validation, preserved cursor/status/gap and joined cancellation. Real room
pagination, multiline/own messages, eviction and noisy withheld-snapshot tests
pass. See [history.md](history.md). HTTP now exposes this room-history profile
with a dedicated provisioned AES-GCM cursor key, scoped opaque cursor checks,
recent/retained paging, response bounds and current authorization. Real HTTPS
HTTP/1.1 and HTTP/2 history, HTTP/2 reply metadata/gaps/revocation, and an owned
CLI pagination restart pass. Capabilities identify caller room-history selectors.
Native HTTP room search is also implemented over the same bounded read leases;
real HTTPS HTTP/1.1 and HTTP/2 exercise Unicode matching, sender filtering, reply
metadata, query/sender cursor scope, empty pages, expiry and revocation. Owned
proxy checks cover cancellation and oversized pages; owned CLI processes cover
search cursor restart and active-read shutdown. See [search.md](search.md).
HTTP room message watch now uses native OBSERVE before its bounded CHECK snapshot,
with opaque cursor resume and long-poll live wakeup. Isolated HTTP/1.1/HTTP/2
live updates, snapshot/live overlap, quiet polls under noise, retention gaps,
scoped authorization/revocation, cancellation and count/byte overflow pass. Owned
CLI watch cursor restart and active-watch SIGINT checks pass. Configured watch
limits and lookup accounting preserve send capacity. See [watch.md](watch.md).
Safe DM mapping, nonmessage updates and the other transport routes remain
required; this does not complete an HTTP catalogue adapter. Review of `b12cc63`
identified pre-ack buffering and skipped prefix/gap overlap correlation; the
corrective implementation buffers from setup start and validates overlap before
returning any prefix, with delayed/missing/rejected-ack and prefix/gap checks.

Native bearer SSE room messages are implemented at `GET /v1/events`: one
exclusive OBSERVE/CHECK session, bounded initial setup, whole page framing,
opaque Last-Event-ID resume, retained-prefix termination, keepalives and 15-second
authorization leases. Real TLS HTTP/1.1 and HTTP/2 clients verify multiple live
messages on one lease; HTTP/2 resume and quiet expiry, abandoned-body backpressure
and the actual CLI service shutdown are covered by isolated fixture tests.
Browser Fetch/CORS is now implemented for explicit exact-origin allowlists and
verified using real Chrome against isolated loopback fixtures. Capability discovery,
bearer-free denial, JSON send/preflight/Location, receipt lookup, whole Unicode
multiline SSE messages, cancellation and Last-Event-ID resume pass; an independent
AIRC observer confirms the browser post. Real TLS HTTP/2 tests cover configured
origin responses and the actual CLI applies its listener origin policy. Nonmessage
events and complete catalogue SSE acceptance remain pending. See [events.md](events.md).

Public directory snapshots are implemented at `GET /v1/directory`, with an
explicit global public-card permission, current revocation checks and the shared
one-use bounded read/session owner. Whole snapshots preserve native public profile,
self-reported activity, connection and timestamp fields, or fail without partial
cards. Real TLS HTTP/1.1 and HTTP/2, a directory-only standalone CLI without chat
targets/cursor keys, and Chrome Fetch preflight/read checks pass. Owned proxy
checks cover missing feature, malformed/duplicate cards, a withheld terminator
with unrelated traffic, capacity rejection and joined CLI shutdown. Larger-directory
pagination, profile/presence writes and nonmessage updates remain required
work, not transport limitations. Native nickname queries now preserve case-insensitive
selection and unknown cards, reject mismatched/missing results, and succeed even
when a global snapshot exceeds the byte ceiling. HTTP/1.1, HTTP/2, CLI and Chrome
checks cover the query. See [directory.md](directory.md).

HTTP PUT now admits send/reply at the stable `/v1/requests/{id}` resource using
the same journal/worker boundary as POST. URI/body/optional-header IDs must agree;
changed intent conflicts instead of replacing a post. TLS HTTP/1.1 and HTTP/2
checks cover independent native observation, replies, POST/PUT replay coalescing
and unchanged saved send budgets. Invalid credentials, selectors, read operations,
permissions and headers create no request. The actual standalone CLI and Chrome
PUT preflight/coalescing checks pass. PATCH/DELETE resource semantics remain
required pending work; catalogue row 17 is still partial. See [http.md](http.md).
The full race suite passes. The non-race module run passed all gateway packages
and integration cases, but the unchanged native CLI outage test
`TestWaitReconnectsAfterDaemonOutageAndReportsRetentionGap` failed registration
once; an isolated `go test -count=1 ./cmd/airc` rerun passed. That failed module
run is not reported as an uninterrupted green run.

Signed and encrypted HTTP envelopes now expose durable send/reply, scoped
receipt status and the existing room history/search/long-poll watch and public
directory operations. The signer alone supplies authority; unsigned credential,
ID and cursor headers fail. The response union and exact byte/time bounds are in
[signed-message-v1.md](signed-message-v1.md). TLS HTTP/1.1 and HTTP/2, independent
native observation, cross-profile replay, current revocation, wrong-profile and
tamper rejection, signed read expiry, and a signing-only real CLI pass.
Browser-produced crypto remains required pending work.

Signing-only capability discovery is implemented through authenticated
`capabilities.get` envelopes. TLS HTTP/1.1 and HTTP/2 verify caller-scoped
permissions, detached long-poll/SSE modes and durable revocation; the standalone
signing-only CLI fixture verifies both profiles without a bearer credential.
Discovery uses local permissions only and does not claim upstream negotiation.

Detached signed/encrypted SSE room messages are implemented at the envelope
`/events` routes. Native HTTP/1.1 and HTTP/2 verify live messages, signed-cursor
resume, earliest retained-prefix renewal, signed validity and durable revocation.
Initial signed history/watch `start` selectors also work for long polling.
The real CLI verifies signing-only streams and joined shutdown for both profiles.
Detached browser crypto/streaming and nonmessage/DM events remain required work.

The shared Step 2 schema and local CLI are implemented, but adapter-native
configuration and runtime enforcement remain pending for other transports.
Step 3 is partial: bearer HTTP runtime authorization/revocation is integrated,
while authentication-flood controls and full
transport acceptance remain pending. Step 4 is partial; operator resolution, backup and full
storage acceptance remain. Step 5 is partial; Step 6 workers are partial; Step 7 HTTP is partial; Step 8 history is partial; Steps 9–10 remain. Catalogue rows 13, 15 and 17
have partial HTTP primitives and native TLS evidence; other rows and remaining
members/modes remain **proposed**, with implementation and acceptance pending.
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
`go vet ./...`, targeted fixture/core/config/CLI race tests, request fuzzing,
`go build ./cmd/...`, and `git diff --check`. The disabled-example CLI commands
above were exercised as real processes. A partial configured HTTP service is verified; no complete adapter is claimed.

Publication is local only: no push, installation, deployment, or live AIRC
integration. The primary checkout, live accounts, state, and daemon remain
outside the fixture boundary.
