# Gateway implementation status

## Scope

Implement the complete unranked 72-entry catalogue, with separate evidence for
each named protocol and mode. All AIRC operations each transport can support
are required, including replies, receipt/status lookup, history, and live
updates. Authenticated send alone is sufficient only with a concrete connection
limitation. A staged implementation or configured ingress-only adapter does
not reduce the final scope.

## Latest adapter increment

- WebRTC DataChannels adds HTTPS offer/answer signaling and all four
  reliable/unreliable, ordered/unordered modes. Each peer is bound to its
  signaling bearer principal and AIRC binding; each operation still requires
  a fresh signed/encrypted detached envelope. Native Pion fixtures exercise all
  33 current shared operations in every mode, including replies, receipts,
  history and watches. AGD1 supports 16 KiB requests and 64 KiB responses with
  bounded assemblies, replay state, active requests and cancellable writes.
  TLS 1.3, strict Origin/JSON checks, shared/per-principal peer quotas,
  revocation, idle expiry and joined shutdown are enforced. Configured STUN
  Binding exchange and a selected local TURN relay path pass race checks;
  invalid TURN credentials fail without native intake. Loopback relay evidence
  does not establish remote NAT traversal.
  Signed/encrypted authority tests reject cross-principal frames before native
  intake or journal jobs. Independent native records validate a history response
  larger than 16 KiB, malformed fragment recovery, and a deliberately dropped
  unreliable response followed by a stable-ID mutation retry with one native
  intake. Active watches join on SIGINT and release their native leases on peer
  close. The final WebRTC race group passed in 129.207s, including all four
  operation matrices, four-mode sequenced live/quiet heartbeat and cursor
  recovery, large whole pages, authority, ICE, retry, idle and slow-reader checks.
  The slow-reader control uses a 64 KiB SCTP receive window and 80 paced 3 KiB
  native records; it witnesses blocked consumption, native lease release and
  channel closure before fresh peer recovery. Full normal and race module
  suites pass (gateway integration 952.518s/962.912s), alongside final affected
  CLI and transport checks. The current fixture gateway SHA-256 is
  `0d3bb4b8ab55b827daa03e9919bbefa89dcb15fba80d542f784600ad89bbffd7`.
  Vet, staticcheck, formatting, module tidiness and diff checks pass. A current
  local vulnerability checker found no affected calls or imported packages;
  three module-only SSH/OpenPGP advisories concern unused code.
  The browser client and four-mode fixture are implemented, but Chrome rejects
  the fixture's isolated CA. Browser-control policy requires the user to handle
  certificate warnings; no browser runtime acceptance is claimed. Row 21 remains
  partial, alongside remaining native operation modes and the full catalogue.
  See [webrtc.md](webrtc.md).

- `agents.read` adds explicit global live-agent discovery, separate from durable
  profile-card `directory` reads. Its complete native `AGENTS` snapshot preserves
  nicknames, case-sensitive joined rooms and connection timestamps, excluding
  hidden observers, ephemeral sessions and disconnected cards. Requests require
  their own permission and a 1–256 completeness bound. Count overflow, malformed
  numeric/JSON envelopes, missing completion, cancellation, native wire/output
  over 64 KiB and carrier response bounds return no partial entries. Reads use
  existing finite session leases and create no delivery jobs; revocation applies.
  Bearer HTTP adds strict `GET /v1/agents` with default limit 256. Shared detached,
  MCP, gRPC and JSON-RPC dispatch expose the same result. Existing advertised
  operation order is preserved, with the new operation appended.
  The final native race group passed in 71.976s against gateway SHA-256
  `880e0e41e6696261ea01ad09a2d3e1487867ef52e1e35275524ee92a24dd88b6`.
  Whole independent native snapshots pass over HTTP/1 and HTTP/2 bearer and
  signed/encrypted ingress, framed TCP/TLS, WS/WSS, Unix streams, stdio, MCP
  Streamable HTTP, gRPC, JSON-RPC and HTTP/3. WebTransport bidi, paired uni and
  datagram checks pass; the DTLS/UDP and all three raw QUIC matrices now exercise
  all 33 current shared operations. Separate strict collector/SDK checks include
  restoring typed events, malformed completion, duplicate/null fields and
  encoded-output overflow below the native wire bound (1.514s under race).
  A history-preservation control initially reused a helper requiring another
  seeded room. It now creates and independently observes one native message and
  compares the complete CHECK metadata before and after discovery (2.498s under
  race). Earlier full runs that captured the obsolete helper were interrupted;
  they are not acceptance evidence. Subsequent full runs found six stale
  capability expectations. The corrected six-test race group passed in 48.650s;
  it retains existing authorization checks and verifies the new permission
  fields. Corrected full normal and race module suites passed every package
  (gateway integration 846.319s/854.761s). Vet, staticcheck, formatting, module
  tidiness and diff checks pass. Nothing was pushed, installed or deployed;
  the primary checkout and live daemon remain untouched.
  WHO/WHOIS, presence monitoring, atomic multi-target CHECK, moderation,
  native retained RETRY/STATUS and the complete catalogue remain pending.
  Current runtime evidence is Darwin loopback. See [agents.md](agents.md).

- Authenticated UDP unicast adds the `udp` listener and a fixed encrypted
  return mailbox per principal/listener. Requests require the existing signed
  inner envelope inside JWE. Responses use an Ed25519 signature inside
  ECDH-ES/A256GCM encryption, binding realm, principal, AIRC binding and owner
  route ID. Unverified traffic receives no response and cannot create jobs or
  select a response destination. Response payloads are bounded to 32 KiB,
  encrypted tokens to 64 KiB and wire output to 1 MiB per request.
  All 32 current shared operations pass the native UDP matrix with whole
  native records and independent mutation/read facts. The real config and
  capability CLI checks pass in the same race run (16.596s). A native
  oversized-history control returns an authenticated encrypted capacity error;
  reducing the page size recovers all nine complete native records through
  opaque cursors (6.073s under race detection).
  Retained/live watches, quiet heartbeat/cursor continuity, finite lease
  expiry, invalid-input silence, native zero-intake controls and active-watch
  SIGINT/socket rebind pass. A wider DTLS/QUIC/WebTransport/UDP regression group
  passed under race detection in 139.796s. Bounded source admission expires
  despite continuous unauthenticated traffic; only authenticated requests
  renew it. Shutdown closes the socket and joins request workers before
  releasing admission slots. See [udp.md](udp.md) for provisioning, framing
  and client setup. The final UDP native group passed under race detection in
  49.833s, including actual dropped responses, reversed/duplicated request and
  response fragments, incomplete-request expiry and reconnect replay with
  exact native post counts. The real Go CLI post/receipt/history check passed
  in 9.952s after correcting premature cleanup of parsed public-key bytes.
  A capacity fallback initially consumed an unwritten response sequence. The
  shared handler now advances only after a successful write, preserving a
  sequence-1 terminal error and contiguous transmitted pages. The stricter
  UDP capacity control and final DTLS/QUIC/WebTransport/UDP datagram group pass
  under race detection in 144.104s against gateway SHA-256
  `8443113e57f9dbbd3e5684ab566033c383bdeef0326d0f82458c45aae7cfb032`.
  An earlier full race run exposed a defective existing QUIC tamper control:
  changing the final Base64 character could preserve the signature bytes by
  changing only unused padding bits. The control now flips a decoded signature
  byte, uses canonical encoding and checks both zero jobs and zero native
  messages. Eight corrected race repetitions passed in 15.589s.
  The Go UDP client requires an explicit fixed mailbox and generates a fresh
  random correlation per invocation to distinguish earlier valid responses.
  Consecutive real CLI send/receipt/history calls pass with distinct default
  correlations and whole native CHECK equality (7.500s under race detection).
  Full module acceptance passes: normal integration 814.903s and race
  integration 827.077s. Vet, staticcheck, tidy-diff and staged diff checks pass.
  Remote PMTU, other runtime platforms, physically one-way
  profiles and the remaining native operations remain pending; row 03 is
  partial, and the complete catalogue is unfinished.

- Raw QUIC adds dedicated `rawquic` and `rawquic_datagrams` UDP listeners with
  TLS 1.3, `airc-gateway-quic/1` ALPN and completed handshakes only. The datagram
  listener requires negotiated peer support. All current 32 operations pass
  over bidi streams, paired uni streams and AGD1 datagrams; the strengthened
  native operation matrix passed under race detection in 32.939s. Whole native
  CHECK records, native mutation events, independent room/card/pin/poll queries,
  active waiting signals and message correction/retraction annotations are
  checked. Response-stream cleanup returns uni stream credit for repeated
  receipt lookups. Frame IDs are fresh per datagram submission and separate
  from stable durable request IDs.
  Actual fixed-window slow-reader and quiet datagram lease controls passed
  together with live/cancellation controls under race detection in 28.188s.
  Real gateway SIGINT with active watches, open completed streams and partial
  frames passed in 5.836s, including UDP rebind and native lease cleanup.
  TLS hostname/ALPN failures, absent peer datagram negotiation, tampered
  envelopes and malformed/incomplete frames have zero-job controls; the final
  focused boundary/replay/cancellation group passed in 16.652s.
  Strengthened replay checks passed in 6.625s, comparing complete native records
  and exact post counts after repeated durable IDs.
  The UDP owner is shared in place with HTTP/3/WebTransport, preserving their
  serving contracts and joining parent-cancellation callbacks before admission
  release. Raw and WebTransport wrappers expire guarded deadlines and serialize
  Close with Write as required by the pinned QUIC send-stream API. WebTransport
  native regression passed in 52.823s; its real Chrome 154 regression probe
  passed in 26.161s with independent native acceptance/reply checks.
  Vet, staticcheck, tidy-diff and diff checks pass. The full normal module gate
  passed, including integration in 709.912s and store in 8.639s. The full race
  module gate passed, including integration in 729.189s and store in 24.996s.
  Native fixtures remain pinned at `96f16c3bbb8a3faac2e69684f591af63ceb58450`.
  Remaining native operations, DM reads and the full catalogue remain unfinished.
  See [quic.md](quic.md).

- WebTransport adds a dedicated draft-16 UDP/HTTP3 session listener, pinning
  webtransport-go v0.13.0 with quic-go v0.63.0. Signed/encrypted requests carry
  all current 32 shared operations over bidi streams, uni streams and fragmented
  datagrams. The common dispatcher retains durable IDs, receipts and native
  lease quotas. Origin and application-protocol checks grant no AIRC authority.
  Before-handshake connection admission, finite session/stream/request slots,
  64-KiB receive windows, bounded writes and joined session/QUIC ownership apply.
  Datagram response sequences prevent clients advancing cursors over missing
  pages. Native controls verify all operation paths, whole CHECK metadata,
  independent mutation/read facts, replay, retained/live watches, packet
  duplicate/reorder controls, incomplete-frame non-dispatch, authentication and
  handshake failures, fixed-window slow readers and active-watch SIGINT cleanup.
  Quiet bidi/uni STOP_SENDING cancellation releases native leases and preserves
  the session after orderly request FIN. The real Chrome 154 P-256 certificate
  probe passes under race detection, including uni/datagram watches, session
  reuse/reconnect, signed discovery and encrypted mutations; independent native
  observation and CHECK require exactly four messages and correct reply links.
  The final Chrome probe passed under race detection in 13.534s. An earlier
  full race run exposed a shutdown race in the library's session-gone reader:
  cancellation could leave it waiting after its stream was removed from the
  session. Stream close now expires guarded read/write deadlines before
  cancellation. Ten repeated native mutation/shutdown runs passed in 103.325s,
  and concurrent session close with open and partial streams passed in 6.352s.
  Final whole-module normal acceptance passed (integration 615.642s, journal
  5.363s). Final whole-module race acceptance passed (integration 630.433s,
  journal 30.010s). Both complete commands exited successfully. Vet, staticcheck,
  tidy-diff, JavaScript syntax and diff checks pass. Govulncheck v1.8.0 finds
  no affecting vulnerabilities; three required-module findings are outside
  called code paths. Native
  fixtures remain pinned at `96f16c3bbb8a3faac2e69684f591af63ceb58450`.
  No primary checkout, live daemon, account or state was changed. Remaining
  native operations, DM reads and the full catalogue remain unfinished.
  See [webtransport.md](webtransport.md).

- HTTP/3 adds a dedicated UDP listener with TLS 1.3 and `h3`, using pinned
  quic-go v0.63.0 and QPACK v0.6.0. Its shared HTTP handler tree carries all
  current 32 operations, bearer/signed/encrypted profiles, JSON-RPC and bounded
  SSE. Only handshake-completed connections are served; 0-RTT is disabled.
  Shared service slots bound admission before handshake processing, and joined
  ownership closes QUIC connections, workers and the UDP socket. Explicit
  controller deadlines bound request reads and synchronous writes.
  Native HTTP/3 clients prove TLS/ALPN without TCP fallback, durable mutation
  acceptance, send/reply replay with exact native count, whole native CHECK
  equivalence for history/search/context/watch, live signals, cancellation,
  denied and unknown requests with zero jobs, certificate/name failures,
  active-watch SIGINT cleanup and fixed-window slow-reader cleanup before
  lease expiry. All HTTP/3 integration race tests passed together in 28.351s;
  the later denied/unknown request race check passed in 6.405s. HTTP/3 owner
  race checks passed in 1.506s, including a real QUIC handshake under shared
  slot capacity and UDP rebind after close. Fixtures use native baseline
  `96f16c3bbb8a3faac2e69684f591af63ceb58450`.
  Whole-module normal acceptance passed (integration 572.038s, journal 5.600s).
  Whole-module race acceptance also passed (integration 576.807s, journal
  28.196s). Both full commands exited successfully. Later-added exact
  CHECK/replay and denied-request tests passed
  separately under race detection on the same production source. Vet,
  staticcheck, tidy-diff and diff checks pass. Govulncheck v1.8.0 finds no
  affecting vulnerabilities; three required-module findings are outside the
  called code paths. This is native-client evidence; browser
  HTTP/3, WebTransport and WebSockets over HTTP/3 are separate claims. Remaining
  native operations, DM reads and the full catalogue remain unfinished.
  See [http3.md](http3.md).

- gRPC adds versioned protobuf Call/Capabilities/Watch methods on a dedicated
  mandatory-TLS HTTP/2 listener, carrying the current 32 operations through the
  shared dispatcher. Exact JSON envelopes retain durable mutation identities;
  protobuf result/terminal alternatives preserve uncertainty, native receipt
  facts and opaque cursors. Real generated clients verify all operation paths,
  native mutation acceptance, replay, whole history/search/context/watch records
  against native CHECK, live signals, cancellation and quiet lease terminals.
  Native controls also prove multiplexed per-principal watch quotas, reserved
  discovery capacity, durable revocation before protobuf decoding, zero jobs
  on denied inputs, and slow-reader write-bound cleanup. The complete focused
  gRPC race group passed in 36.264s before regeneration with the final pinned
  generator. Additional final-source TLS race checks passed in 4.715s: peer
  facts confirm TLS 1.3, verified certificate chains and `h2`; wrong CA, TLS 1.2,
  plaintext and duplicate bearer metadata fail. A final-source process-shutdown
  race check passed in 4.938s,
  proving that SIGINT exits cleanly with an active gRPC watch and joins the
  native lease. All fixtures use the pinned native baseline
  `96f16c3bbb8a3faac2e69684f591af63ceb58450`. Whole-module normal/race integration
  checks passed in 549.864s/557.843s on final production/generated source. Later
  added TLS and shutdown tests passed separately on that same source. Both
  full commands exited successfully (journal 5.618s/27.428s).
  Vet, staticcheck, tidy-diff and diff checks pass; govulncheck v1.8.0 reports no
  vulnerabilities. This uses the documented upstream
  experimental ServeHTTP API with Go HTTP/2 and synchronous write deadlines;
  it does not claim gRPC-Web or all native transport options. No live daemon,
  account or state was contacted. Remaining native operations, DM reads and
  the full catalogue remain unfinished. See [grpc.md](grpc.md).

- JSON-RPC over the existing HTTP listener exposes all current 32 operations
  through `airc.request`, plus permission-scoped `airc.capabilities`. HTTP/1.1
  and HTTP/2 preserve raw bounded correlation IDs separately from durable
  mutation IDs. Batches are sequential and bounded; send/reply notifications
  produce no response. Explicit-ID watch calls support bounded live SSE with
  page checkpoints and a correlated terminal response. Real native controls
  cover every operation, durable replay, mutation acceptance, authorization,
  batch revocation, quiet batches beyond the ordinary HTTP write deadline,
  stream cancellation and lease cleanup. The real Chrome probe passed in
  14.931s, including Unicode history, replay, notifications and cursor renewal;
  its native observer verified the exact four-message count. It used an owned
  loopback browser fixture, not remote browser TLS. The pinned native baseline
  remains `96f16c3bbb8a3faac2e69684f591af63ceb58450`. Independent review found and
  corrected oversized-body error classification; focused RPC race checks
  passed afterward (final full RPC group 42.009s, including a quiet lease).
  Whole-module normal/race integration checks passed in 497.138s/499.035s;
  both commands subsequently completed successfully. Those full commands began
  before the final localized classification fix and added terminal test; the
  affected adapter/RPC race checks passed on final source. Vet, staticcheck,
  tidy-diff and diff checks also passed. This implements the JSON-RPC profile of catalogue row 24;
  other RPC protocols, DM reads and the full catalogue remain unfinished.
  See [jsonrpc.md](jsonrpc.md).

- Shared `events.watch` extends current carriers to 32 operations (33 MCP
  tools including capability discovery). It adds explicitly authorized live
  room membership, departure, kick, operator-mode, topic and signal events
  while preserving message-only `watch` routes and permissions. Nonmessage
  pages preserve the exact opaque retained-message checkpoint; live events
  are not durable or replayable. Native controls compare whole signal objects,
  exercise every supported event kind, prove a conversation parent omitted
  from the snapshot, and reject foreign cursors. Current carrier controls cover
  HTTP/1.1 and HTTP/2, signed/encrypted SSE, framed encryption/TLS, WSS, Unix,
  stdio and MCP pages/progress with cancellation cleanup. Full normal/race
  module checks passed (integration 468.652s/469.185s, journal 5.289s/24.862s),
  both exiting successfully. Vet, staticcheck, tidy-diff, diff checks and actual
  read-only CLI capability discovery passed. The fixtures use pinned native
  commit `96f16c3bbb8a3faac2e69684f591af63ceb58450`; no live service or account
  was contacted. This does not complete DM reads, remaining native operations
  or the 72-entry catalogue. See [live-events.md](live-events.md).

- Shared `channels.read` extends the current carriers to 31 operations.
  It is an explicit global room-discovery permission, covering rooms known
  from live members, retained history or topics. Exact request and global
  one-use grant/revocation tests pass. Native race acceptance across current
  carriers, signed TLS modes and the full MCP operation matrix passes in
  20.368s. Independent native controls cover history/topic/live-only rooms,
  case-distinct names, DM exclusion, empty snapshots, global permission denial,
  fresh snapshots after PART and unchanged history. SDK/backend/gateway race,
  static checks and read-only CLI discovery pass. Full normal/race module
  checks passed (integration 460.951s/469.036s, journal 5.794s/25.862s), with both
  commands exiting successfully. This does not complete the
  remaining native operations or catalogue. See [channels.md](channels.md).

- Shared `members.read` extends implemented carriers to thirty operations.
  It adds a bounded exact-room NAMES snapshot to all implemented carriers,
  preserving nickname spelling and operator markers with no fabricated
  delivery receipt. Focused core/config/backend/store/adapter race checks and
  native visibility-boundary tests pass. Native HTTP/framed/WebSocket/Unix/stdio/MCP
  controls now pass, including whole native NAMES comparison, membership
  changes, roles, empty snapshots, permission denials and receipt absence.
  Full normal/race runs passed every package except the stale 29-operation CLI
  assertion (integration 458.721s/470.991s, journal 6.698s/32.388s). Both commands
  returned nonzero for that assertion; after correcting it, affected CLI
  normal/race suites passed in 0.441s/1.465s. Final review found and fixed the
  missing members browser preflight route; the whole adapter race suite passed
  in 3.126s. Fresh full normal/race integration checks pass on that corrected
  source (458.351s/463.658s), with journal checks in 6.070s/29.718s.
  Both full module commands exited successfully.
  Whole-module vet/staticcheck/tidy and
  actual read-only CLI discovery pass. This does not complete the remaining native
  operations or full catalogue. See [members.md](members.md).

- MCP Streamable HTTP carries the current shared gateway operations. The
  `/mcp` endpoint initially exposed twenty-nine shared operations plus
  authenticated `capabilities.get`, with exact gateway request decoding,
  durable results and cursor-based watch progress. Session ownership, current
  durable revocation, quotas and shutdown are tested with public SDK clients.
  The whole gateway and CLI race suite passed (journal 34.743s); affected vet,
  staticcheck and module tidiness checks passed. Stronger native result
  assertions, watch renewal, cursor resume, reconnect replay and active-stream
  shutdown checks passed in 35.526s. Full normal module acceptance passed
  (integration 456.595s, journal 6.998s); full race module acceptance passed
  (integration 478.265s, journal 38.069s). This is not catalogue completion.
  See [mcp-http.md](mcp-http.md).

- Shared `message.action`, `message.correct` and `message.retract`
  extend the implemented carriers to twenty-nine operations. Exact room pins
  and context proof precede correction/retraction; direct proof matches the
  bound native sender account and destination. Whole receipt/message/terminator
  evidence becomes an ordinary durable receipt, with zero native lookups and
  default bounded retries exposing possible duplicate posts. Native failures
  revealed room-only direct rejection, incorrect new retract metadata and
  missing pinned-parent proof after eviction; all were fixed. HTTP/direct/pin/
  recovery checks passed, Unix/stdio restart and framed TLS passed in 10.651s,
  and offline direct plus native mute/unmute controls passed in 8.104s.
  Real Chrome WebCrypto/WebSocket passed in 20.951s with independent native
  new-message and original-annotation comparisons. Whole-backend race checks
  passed in 3.822s; core/auth/config/store passed in
  1.258s/1.639s/1.305s/34.829s. Vet/staticcheck/tidy and actual CLI configuration
  validation and 29-operation capabilities passed. Final review tightened new
  parent/receipt/thread IDs to native canonical lowercase without changing
  legacy send/reply contracts. Focused core/auth/backend/store race checks
  passed in 1.365s/1.406s/1.573s/2.455s; whole core/auth regression passed in
  1.222s/1.742s and all native mutation controls in 36.710s.
  Initial full normal/race module runs failed integration in
  387.458s/404.894s on the stale 26-operation conformance assertion. It now
  expects 29; affected carrier conformance passed in 33.486s and Unix
  conformance in 9.626s. Final full normal and race module suites passed,
  including integration in 422.955s/439.527s and store in 6.533s/33.580s.
  See [message-mutations.md](message-mutations.md).
  Remaining native reads, nonmessage live forwarding, DM reads and complete
  catalogue coverage remain required.

- Shared `poll.create`, `poll.vote` and `poll.close` extend HTTP,
  framed TCP/TLS, WebSocket, Unix streams and stdio to twenty-six operations.
  Vote/close prove the exact room from independently retained native poll state
  before writing. Creation preserves a compound whole receipt/results ACK;
  vote/close preserve whole aggregate results without fabricated receipt facts.
  Schema 10 adds eight-way exclusive canonical outcomes and migrates versions
  1–9. Real carrier race checks passed in 24.086s, including another poll author's
  identity, non-author close denial, foreign-room proof, history eviction, vote
  replacement, replay after independent native changes and restart, and uncertain
  creation retry producing a distinct native poll. Receipt-only recovery does
  not resend. An initial native run exposed missing top-level creation ID
  readback and omitted validity fields in the test envelope helper; both were
  fixed. Real Chrome WebCrypto/WebSocket acceptance passed in 43.760s, comparing
  all three poll ACKs to independent native messages/result broadcasts and
  preserving reply-signal, history/search/live-watch, tamper and abort checks.
  Its initial run exposed a signal-only assertion applied to poll creation;
  the browser test now checks creation's actual message ID/persistence facts.
  Backend review found permanent preflight numerics being retried as outages;
  corrected mapping preserves native codes, bounds proof deadlines and rejects
  unexpected receipts before writing. A final review also found numerics after partial acceptance incorrectly
  clearing delivery uncertainty; receipt-only and results controls reproduce
  the defect and pass after the fix. Final whole-backend race checks passed
  in 3.771s; focused journal/restart/save-failure/eight-way SQL guards and schema 9
  canonical evidence migration race checks passed in 5.402s. Full module
  normal/race suites passed (integration 393.459s/407.117s, store
  5.745s/39.216s). After the final partial-ACK guard, native poll race checks
  passed in 25.386s; vet and staticcheck passed across the module. See [polls.md](polls.md).
  Other native operations, nonmessage live forwarding, DM reads and complete
  catalogue coverage remain required.

- Shared `reply_signal.set`/`reply_signal.cancel` extend implemented
  HTTP, framed TCP/TLS, WebSocket, Unix streams and stdio to twenty-three
  operations. Same-session retained-parent proof covers exact rooms and both
  direct directions, preserving requested and actual native targets separately.
  Schema 9 preserves whole historical ACKs and seven-way outcome exclusivity.
  Initial native runs exposed empty native message-kind validation and omitted
  delivery routing; both were fixed. Corrected native race tests passed for
  HTTP profiles, WebSocket, direct/self parents, throttle, forged IDs, mute,
  no-op cancel and crash recovery. TLS passed its carrier rerun; final Unix/stdio
  race checks passed in 40.849s after giving their independent observer a
  long-lived idle timeout for subprocess restart checks. Core/auth/backend/store
  race checks passed in 1.331s/2.113s/3.039s/95.201s; final core/auth after a
  staticcheck correction passed in 1.352s/2.456s. Vet, staticcheck, tidy,
  configuration validation and actual CLI capabilities passed. Initial full
  normal/race integration runs timed out in 903.368s/901.762s under machine
  load averages above 600, with additional timing failures; these are failures,
  not acceptance evidence. The unchanged CLI login/load tests passed a targeted
  race rerun in 1.882s/2.742s. Fixtures now share one bounded, SHA-logged current
  gateway race executable per test process, while retaining separate native
  daemons, accounts, credentials, journals and lifetimes. Final reply-signal
  plus real two-gateway/native isolation race checks passed in 69.372s. Vet,
  staticcheck and Windows cross-compilation passed for this fixture change.
  Fresh full-module normal and race suites passed with package concurrency one;
  integration passed in 400.383s/443.953s and store in 5.910s/31.645s. After
  the final proof-deadline guard, whole-backend normal/race checks passed in
  1.911s/3.560s and all native reply-signal race checks in 40.529s. The published
  browser helper now adds validity fields for all twelve implemented mutations.
  Real Chrome WebCrypto/WebSocket acceptance passed in 19.772s, including both
  reply signals compared against independent native broadcasts, replay, history,
  search, live watch, tamper rejection and abort handling. The first browser
  rerun caught an incorrect test assertion for null unknown receipt facts;
  the corrected assertion passed. See
  [reply-signals.md](reply-signals.md). Remaining native operations, nonmessage
  live forwarding, DM reads and the entire catalogue remain required.

- Shared `activity.set` extends HTTP, framed TCP/TLS, WebSocket, Unix streams
  and stdio to twenty-one operations, with exact room/direct authorization.
  Whole native typing/thinking ACKs preserve actual kind, note, actor and expiry
  under the native two-second cross-kind throttle. Saved replay never refreshes
  activity, even after expiry and an intervening native write. Schema 8 preserves
  earlier outcomes and enforces six-way exclusivity. Uncertain bounded retries
  use zero lookup attempts and can refresh activity; receipt-only does not resend.
  Native carrier race checks passed in 55.382s, including direct delivery, native
  780 observations, throttle/fresh-write controls, invalid/unauthorized intent,
  mute/unmute and journal crash recovery. Final full-package core/auth/backend/
  store race checks passed in 1.412s/2.001s/3.482s/27.566s. Vet/staticcheck,
  tidy, formatting and actual disabled-example CLI checks passed; independent
  source review found no must-fix defect. Both full-module suites passed:
  normal integration/store 512.061s/6.561s and race integration/store
  537.591s/29.425s. No dependency or native source changes were made.
  See [activity.md](activity.md). Remaining native operations, nonmessage live
  forwarding, DM reads and the complete catalogue remain required.

- Shared `pins.add`/`pins.remove` extend HTTP, framed TCP/TLS, WebSocket,
  Unix stream and stdio to twenty operations. Before native CHAT writes,
  exact ID/room proof prevents a permitted room from being used to mutate an
  ID belonging to another room. Stored-pin proof supports removal after history
  eviction; retained context supports add and already-absent no-op removal.
  Whole exact ACK plus terminator becomes durable pin evidence with no new
  message ID or persistence/recipient claim. Schema 7 preserves old outcomes
  and enforces five-way exclusivity. Bounded retry and receipt-only use zero
  lookup attempts. Native quota 461 and mute 485 remain typed permanent failures.
  Real HTTP/1.1/HTTP/2, signed HTTP/2, encrypted HTTP/TCP/WebSocket, framed TLS,
  Unix and stdio tests passed under the race detector in 62.909s. Stdio accepted
  add/remove replay after native unpin/pin and subprocess restarts does not
  overwrite newer state. Native tests cover forged IDs, eviction, no-op removal,
  limits, mute recovery, and accepted-but-unsaved restart under both policies.
  Final focused backend/core/auth/store race checks passed in
  2.029s/1.978s/2.455s/7.607s. Independent source review found no must-fix defect.
  Initial full normal/race runs failed from disk exhaustion during fixture
  builds. Verified inactive gateway fixture cache objects were removed without
  deleting unrelated cache. Native fixtures now share only one checksum-verified
  immutable executable per test process, with `-trimpath`; all accounts, state,
  sockets and daemon lifetimes remain separate. Pin and real two-daemon isolation
  race checks passed in 58.697s. The full normal suite passed (gateway integration
  474.686s, store 5.296s), as did the full race suite (gateway integration
  484.524s, store 37.250s). Vet/staticcheck, tidy, formatting and actual
  disabled-example CLI checks passed. No dependencies or native source changed. See
  [pin-mutations.md](pin-mutations.md).
  The remaining native operations, nonmessage live updates, DM reads and complete
  unranked catalogue remain required; no adapter is complete.
- Shared `room.update` extends HTTP, framed TCP/TLS, WebSocket, Unix stream
  and stdio to eighteen operations. Exact room authorization and native
  account-backed operator/admin authority apply independently. Partial patches
  preserve omitted values through native `-1` selectors; zero slow mode disables
  throttling and zero history removes the room quota while preserving global
  retention. Native whole settings plus terminator form a typed durable
  acknowledgement with unknown persistence/recipient facts and no message ID.
  Schema 6 preserves earlier canonical message/card/topic evidence and enforces
  four-way exclusivity. Default bounded retries expose uncertainty/duplicate
  risk without lookup; receipt-only does not resend a possibly accepted update.
  Final room carrier race checks passed in 25.433s, covering independent native
  snapshots, real HTTP/1.1/HTTP/2 and signed HTTP/2, framed TLS, Unix and stdio
  restart after an intervening native update. Original-token replay keeps the
  saved whole acknowledgement, one send attempt and the newer native settings.
  Native history-limit 33 rejection (461), removed operator blocking (481),
  recovery and valid partial/zero/max-setting controls pass. Invalid intent and
  unauthorized room controls create no job; their race check passed in 5.331s.
  Final collector/auth race checks passed in 1.759s/1.374s; final room/migration
  store controls passed in 3.219s. Vet/staticcheck/build/tidy/format and real
  disabled-example CLI commands passed. Independent source review found no
  remaining concrete defect. The full serial race suite passed, including native
  integration in 668.831s and store tests in 28.367s. Subsequent test-only checks
  passed: actual quota trimming/removal in 8.389s and whole typed topic migration
  evidence in 2.954s. See
  [room-update.md](room-update.md). Other native operations, nonmessage live
  updates, DM reads and the full unranked catalogue remain required; no adapter
  is complete.
- Shared `topic.set` extends HTTP, framed TCP/TLS, WebSocket, Unix stream and
  stdio to seventeen operations. It sets or clears one authorized exact room
  header through one native command, preserving whole setter/time metadata in
  a typed durable acknowledgement. Message identity, persistence and recipient
  facts are not invented. Schema 5 preserves earlier message/card evidence and
  enforces three-way exclusivity. Default bounded retries expose uncertainty
  and duplicate risk without native lookup; receipt-only never resends a
  possibly accepted write. Focused HTTP/encrypted race checks passed in 11.314s;
  stdio, Unix and framed-TLS race checks passed in 18.071s, including restart
  replay of the original token after an intervening native update, unchanged
  saved setter/time, one send attempt and no native rewrite. Signed HTTP/2 has
  an actual protocol assertion and a valid 400-byte escaped-text control.
  Collector controls passed in 1.610s; store/delivery race checks passed with
  store in 21.256s, and final topic/migration/exclusivity controls passed in
  3.419s after removing a duplicate guard. Vet/staticcheck/build/tidy and the
  actual disabled-example CLI commands passed. Independent source review found
  no remaining concrete defect. See [topic-set.md](topic-set.md). The full
  serial race command failed only the old sixteen-operation assertions in
  shared framed/WebSocket/Unix/stdio conformance; its other packages passed,
  including the native CLI in 54.173s and store in 20.701s. The assertion is
  corrected and all affected real conformance race tests passed in 58.243s.
  The broad command
  remains recorded as failed. Full catalogue, remaining native operations,
  nonmessage live updates and DM reads remain required, and all adapters remain
  partial.
- Shared `profile.update` and `presence.set` now extend the implemented HTTP,
  framed TCP/TLS, WebSocket, Unix stream and stdio adapters to sixteen operations.
  Mutations are restricted to the authenticated binding's own card. Whole typed
  native acknowledgements are journaled separately from message receipts;
  persistence and recipient facts remain unknown. Schema 4 preserves older
  receipt bytes and supports immutable failed-save recovery. Default bounded
  retries retain the gateway ID, expose duplicate risk, and spend no native
  CHECK lookup budget; receipt-only policy never resends an uncertain update.
  Native escaped cards above 4 KiB, profile clears, presence expiry, rejection
  before admission, and the native 8192-byte acknowledgement-line limitation
  have real fixture evidence. The latter preserves uncertainty without a
  fabricated accepted result, and a subsequent representable update recovers.
  Stdio restart/replay, Unix and framed TLS mutations have focused race evidence;
  bearer HTTP/1.1/HTTP/2, signed HTTP/2 and encrypted HTTP/framed/WebSocket
  mutations and accepted results are covered. Affected gateway/CLI race checks passed, including
  store in 18.849s and backend in 2.825s; vet/staticcheck/build/tidy checks passed.
  The serial full-module race command passed gateway integration in 564.233s
  and every other package except the unchanged native CLI reconnect test, which
  timed out waiting for an outage reconnect. That command remains failed.
  Final review then tightened the wait-phase deadline guard for both card and
  message acknowledgements. Final gateway/CLI race checks passed, card transport
  race checks passed in 48.965s, and existing framed/WebSocket/Unix/stdio message,
  reply, reaction, history and watch conformance passed in 69.548s. The native
  CLI package passed fresh in 49.495s, and three isolated reconnect repetitions
  passed in 3.226s. Final version-1/2/3 migration checks passed in 1.921s using
  a valid old message receipt and preserving its bytes and typed delivery facts. The initial new
  collector test contained an unbounded command wait; that run was interrupted
  and is not counted as passing evidence. Its waits are now bounded.
  See [profile-presence.md](profile-presence.md). Nonmessage live updates,
  remaining native operations, DM reads and the full unranked catalogue remain
  required; no adapter is declared complete.
- Catalogue row 31 now has a partial stdin/stdout pipe adapter using the
  shared fourteen operations and correlated framing. Real child-process tests
  execute all fourteen operations, observe native sends/replies/reactions,
  compare poll/topic metadata and room/waiting facts, and verify signed/encrypted
  authentication. Immediate EOF preserves admitted responses and a durable send
  whose accepted receipt survives restart. Truncated identifiable signed frames
  produce neither output nor receipts. SIGINT during a resumed watch releases
  the journal lock; regular files and ordinary serve reject stdio before journal
  creation. Final stdio integration race checks passed in 28.882s; affected
  gateway/CLI race checks, vet/staticcheck, build and tidy checks passed.
  Darwin pipe deadlines, backpressure and descriptor ownership are verified;
  Linux and Windows adapter/CLI cross-compilation passed, but Linux runtime
  remains unverified and Windows stdio explicitly unavailable. Independent
  precommit review found read/write FIFO acceptance, now corrected with exact
  directional access checks and rejection tests. The frozen serial full-module race run passed every other package,
  including gateway integration in 477.317s. The unchanged native CLI reconnect
  test timed out in that broad run; three isolated race runs passed in 2.961s,
  and the entire native CLI package subsequently passed in 49.675s. The broad
  command therefore remains recorded as failed, rather than a clean full-suite
  pass. The earlier file-split collection failure was resolved before this
  frozen run. See
  [stdio.md](stdio.md). Richer shared native operations and the complete
  unranked catalogue remain required.
- Room `topic.read` now extends HTTP, framed TCP/TLS, WebSocket and Unix
  streams. It preserves whole native header text, setter nickname and UTC
  Unix-second set timestamp, including honest empty-header facts after clearing.
  Exact-room authorization and a bounded exclusive native session apply without
  retained-history proof. Collector race checks passed in 1.513s, and all gateway
  packages plus CLI passed race checks. Final topic transport and shared-stream
  conformance race checks passed in 53.884s: native setter acknowledgements and
  independent observation, whole metadata across HTTP/1.1/HTTP/2/signed HTTP/2/
  framed/WebSocket/Unix, positive history-eviction controls, clear-to-empty, typed
  denials, unchanged history and no delivery receipts. Vet/staticcheck/build and
  formatting checks passed. See [topics.md](topics.md) for native numeric/SDK
  validation boundaries. Topic writes, nonmessage live updates, other native
  operations, DM reads and the complete 72-entry catalogue remain required.
- Room `poll.results` now extends HTTP, framed TCP/TLS, WebSocket and Unix
  streams. A bounded exclusive native results lookup requires exact authorized
  room and poll ID, preserving whole question/options, aggregate votes,
  author/account, expiry and closed facts. It remains readable after the original
  question leaves CHECK history because native polls are independently persisted.
  Final real encrypted transport and bearer HTTP/1.1/HTTP/2 race checks passed
  in 22.102s, including whole results after eviction, independent native poll
  observation, open/vote/closed/expired snapshots, foreign-ID and missing-ID
  denials and no native history mutation or delivery receipt. All gateway
  packages and CLI passed race checks; final parser controls passed in 1.296s,
  with vet/staticcheck/build checks. Shared TCP/WebSocket/Unix stream conformance race checks passed in 46.128s.
  See [polls.md](polls.md), including native SDK extension-field/accounting
  limitations and the inspected seven-day retention rule. Poll writes and
  remaining native operations/DM reads are still required; all adapters remain
  partial and the 72-entry catalogue is incomplete.
- Catalogue row 28 now has a partial Unix stream adapter using the shared
  framed protocol and the then-twelve shared operations. Owner-controlled
  paths, socket mode/ownership, same-UID peer credentials, encrypted per-frame
  authentication, existing-path refusal and inode-safe cleanup apply. Real
  standalone Unix/CLI checks verify native mutation receipts and independent
  observations, populated context/pins/waiting results, shared history/search/
  watch conformance, profile/framing rejection and joined shutdown. Final focused
  Unix integration race checks passed in 29.579s; adapter/config/service/CLI race
  checks also passed. The foreign-UID subprocess test is skipped on the UID-501
  host because root is required; noninteractive privilege access is unavailable.
  Linux arm64 and Windows amd64 adapter test binaries cross-compile but were not
  executed; Linux runtime acceptance remains open because Docker has no running
  daemon. See [unix-stream.md](unix-stream.md).
  The initial concurrent full race run failed an unchanged native 50ms CLI wait,
  a gateway collector's overly short non-timeout test budget and the integration
  package's aggregate ten-minute timeout. The native test passed three isolated
  race repetitions. Non-timeout collector cases now have a three-second budget;
  continuous-noise deadline cases retain 80ms, and focused collector race checks
  passed in 1.711s. The serial full module race run
  `go test -race -p 1 -timeout 20m ./... -count=1` passed, including gateway
  integration in 428.625s. Vet/staticcheck, dependency and formatting checks
  passed. Feature deadlines remain unchanged. Unix transport supports richer
  operations; remaining shared native operations and DM reads remain required.
- Room `room.read` and retained-question `waiting.read` extend HTTP, framed
  TCP/TLS and WebSocket. Settings preserve native stored overrides through an
  explicit nonmutating getter. Waiting snapshots require same-session retained
  question ownership proof even when empty; whole native signals preserve actor
  identity and expiry facts within count and byte bounds. Real encrypted
  transport tests cover nondefault settings, explicit HTTP/1.1 and HTTP/2,
  signed HTTP/2, multiple actors, cancellation, expiry and foreign-parent
  denials. Reads create no delivery receipt or native history mutation. See
  [room-waiting.md](room-waiting.md). The focused integration race group passed
  in 12.025s, and independent review's transport race run passed in 12.624s.
  The final collector test additions passed a focused race run in 1.621s.
  Full normal/race module suites passed (gateway integration 411.213s/417.621s),
  with vet, staticcheck, build and formatting checks. The full runs compiled
  before the final collector test-only additions; their focused race run covers
  those additions. Independent source review found no remaining defects.
  This remains partial adapter coverage; remaining native operations, DM reads
  and catalogue connections still require implementation.
- Room `pins.read` now extends HTTP, framed TCP/TLS and WebSocket. One bounded
  read lease returns zero through five whole native pins with exact room and
  entry identity validation, explicit empty arrays, whole multiline/Unicode
  bodies and retained correction facts. Native pins remain readable after both
  root and correction leave CHECK history. Real encrypted cross-transport,
  explicit HTTP/1.1/HTTP/2 and signed HTTP/2 checks pass; query/permission denials
  create no receipt or mutation. See [pins.md](pins.md).
  Full normal/race module suites passed (gateway integration 339.913s/344.944s).
  The final effective-deadline correction subsequently passed backend and pin
  integration race checks (1.574s/7.717s), with vet/build/staticcheck,
  formatting/dependency checks and independent review. Other native operations,
  DM reads and remaining catalogue connections still require implementation.
- Room history, search and watch now accept paired `parent_id`/`thread_mode`
  selectors on HTTP, framed TCP/TLS and WebSocket. Bounded native ownership proofs
  preserve retained descendants after root/parent eviction; ordinary cursor AAD
  stays compatible and conversation cursors bind the original parent and mode.
  Native CHECK message targets remain rooms while page/OBSERVE markers identify
  selectors. Real encrypted cross-transport, retention-32, foreign ownership,
  query-matching unrelated thread and live reaction/descendant filtering checks
  pass. See [conversation-selectors.md](conversation-selectors.md).
  Full normal/race module suites passed (gateway integration 326.247s/331.069s);
  final strengthened conversation race checks passed in 28.562s, with
  vet/build/staticcheck, formatting/dependency checks and independent review.
  DM selectors and other native operations remain required; this does not
  complete a catalogue adapter.
- Shared room `context.read` now extends HTTP, framed TCP/TLS and WebSocket.
  One bounded session proves exact trigger ownership before returning whole
  conversation records, correction/reaction links, room pins, public cards and
  honest omission/root-gap facts. Context-only configurations require a send
  reserve but no cursor key. Real encrypted cross-transport, signed HTTP and
  explicit HTTP/1.1/HTTP/2 checks pass; native CHECK snapshots remain unchanged.
  Collector acceptance includes a complete valid control at the exact wire
  budget, one-byte overflow, complete malformed responses and cancellation under
  unrelated traffic. See [context.md](context.md).
  Full normal/race module suites passed (gateway integration 311.035s/307.342s),
  with vet/build/staticcheck, formatting/dependency checks and independent review.
  DM context and remaining native operations remain required;
  this does not complete a catalogue adapter.
- Shared room `reaction.add` now extends HTTP, framed TCP/TLS and WebSocket.
  Parent proof, exact native receipt correlation, feature negotiation and durable
  replay use the existing worker/journal boundary. Real cross-transport and
  Chrome/native checks passed; lost-result recovery, receipt-only no-resend,
  parent/feature denials and lost/corrupted receipt boundaries have explicit
  evidence. See [reactions.md](reactions.md). Remaining richer operations and
  DM support still prevent bidirectional catalogue completion.
  Normal/race module suites passed (gateway integration 305.259s/301.301s),
  with vet/build/staticcheck, format/dependency/client syntax checks and the
  real Chrome probe (12.94s). The native recipient-identity fixture now waits
  for the sender's QUIT acknowledgement before nickname reassignment; its
  ten-repeat race check passed without weakening the privacy counterexample.
- WebSocket v1 now carries current shared operations and live watch through
  the same correlated dispatcher as framed TCP/TLS. Native standalone IPv4/IPv6,
  TLS trust/name and origin rejection, malformed/oversized/binary messages,
  replay across disconnect, signed expiry/cursor renewal, pre-auth and missing-pong
  deadlines, and joined shutdown passed. Chrome exercises encrypted browser
  sends/replies/reads/live multiplexing, ambient cookies and cancellation with
  independent native observation (final browser probe 22.19s). Full normal/race
  suites passed (gateway integration 290.937s/289.433s), along with
  vet/build/staticcheck, dependency/format checks and vulnerability scanning.
  See [websocket-v1.md](websocket-v1.md).
- Catalogue row 19 remains **partial**, as do other implemented bidirectional
  rows: DM reads and richer AIRC operations still require implementation.
- Framed TCP/TLS v1 now carries all currently implemented shared operations,
  with correlated multiplexing, live watch and signed/encrypted authentication.
  Native IPv4/IPv6 standalone CLI tests and TLS trust/name rejection pass on
  macOS. Framing, assembly, request/connection quotas and joined shutdown are
  bounded. Full normal/race suites passed (gateway integration 239.381s/237.352s),
  as did vet/build/staticcheck and the independent review's focused race runs.
  See [framed-stream-v1.md](framed-stream-v1.md).
- Catalogue rows 01 (TCP/IPv4/IPv6) and 02 (TLS/TCP) remain **partial**: DM reads
  and richer AIRC functionality are still required. No reused carrier is claimed
  without its own implementation and native acceptance evidence.

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
  without state/listeners. The CLI now advertises compiled partial HTTP, framed TCP/TLS, WebSocket and Unix stream intake;
  only current shared implemented operations can be enabled. See [configuration.md](configuration.md).
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
required work. The native account-replacement probe in
[dm-read-boundary.md](dm-read-boundary.md) verifies that an old inbox record
survives nickname reassignment while metadata identifies only the sender account.
Recipient identity at delivery needs a concrete mapping before direct reads can
be exposed. This is an upstream identity prerequisite, not a transport/hardware
blocker or completed DM acceptance. Other adapter work remains unblocked.
This does not complete an HTTP catalogue adapter. Review of `b12cc63`
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
Browser-produced crypto has native Chrome evidence as described below.

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
The WebCrypto Fetch example and real Chrome probe verify browser-produced
signed/encrypted envelopes, delivery/replies and scoped reads, replay, retained/live SSE,
signed-cursor resume and tamper rejection. Go's independent native observer
confirms the six browser posts and native reply parent/thread IDs; the existing bearer Chrome probe also passes.
Other browser engines/remote TLS and nonmessage/DM events remain required work.

DTLS over UDP is implemented for the current 32 shared operations, with the
DTLS 1.2/AGD1 contract in [dtls.md](dtls.md). A real race-instrumented gateway
and immutable isolated native server verify those operations using independent
native records and mutation facts. The final targeted race run also verifies
certificate trust/hostname and ALPN rejection, tampered envelopes, malformed and
oversized records, actual handshake packet loss, response loss, reordering,
wire duplication, same-association and reconnect replay, retained/live watch,
quiet heartbeat positions, fifteen-second lease expiry, and SIGINT joining an
active native watch and incomplete request followed by UDP rebind. All three
raw QUIC operation matrices pass after extracting their unchanged shared test
helpers. The combined native run passed in 73.134 seconds. Unit race tests cover
pre-handshake admission, queue overflow, deadlines, authenticated idle renewal,
stale-reaper protection and explicit slot release. `go vet`, `staticcheck`,
`go mod tidy -diff`, and `git diff --check` pass. A current-toolchain
`govulncheck` scan finds no reachable vulnerabilities; three findings in required
modules are not in imported packages. The initial full normal/race module runs
passed all other checks but exposed regenerated validity fields in the replay
fixture, correctly rejected as changed intent. The corrected fixture retains
those fields and deliberately crosses a clock boundary; its targeted race test
passed three times. Full normal and race module gates pass, including native
integration runs of 742.969 and 744.767 seconds respectively. Acceptance is on
Darwin loopback with ECDSA certificates; remote
networks/PMTU, RSA interoperability, DTLS 1.3 and remaining native operation
modes are pending. No complete catalogue row is claimed.

Exact-commit review found that decrypted application records and error replies
could refresh the DTLS idle lease before envelope authentication. Renewal now
occurs only after a complete authenticated result/page write. A two-slot native
regression passes normally (35.902 seconds) and with the race detector (36.548
seconds): repeated malformed records and fresh invalid signatures cannot retain
their slot, while a renewed authenticated peer and its replacement remain
usable. Adapter/service race tests, vet and staticcheck pass; the DTLS/QUIC
native matrix (68.890 seconds) and WebTransport datagram regressions (8.908
seconds) also pass after the correction.

The shared Step 2 schema and local CLI are implemented, but adapter-native
configuration and runtime enforcement remain pending for other transports.
Step 3 is partial: bearer HTTP runtime authorization/revocation is integrated,
while authentication-flood controls and full
transport acceptance remain pending. Step 4 is partial; operator resolution, backup and full
storage acceptance remain. Step 5 is partial; Step 6 workers are partial; Step 7 HTTP is partial; Step 8 history is partial; Steps 9–10 remain. Catalogue rows 13, 15 and 17
have partial HTTP primitives and native TLS evidence. Rows 01/02, 19 and 28
have partial framed TCP/TLS, WebSocket and Unix stream implementations as
described above; row 31 has the partial stdio implementation described above.
Row 03 has the partial authenticated UDP unicast implementation in [udp.md](udp.md).
Row 22 has the partial MCP Streamable HTTP implementation in
[mcp-http.md](mcp-http.md). Row 04 has the partial DTLS datagram implementation in [dtls.md](dtls.md).
Rows 05/06 have partial raw QUIC stream/datagram
implementations in [quic.md](quic.md). Rows 14, 20, 23 and 24 have partial HTTP/3,
WebTransport, gRPC and JSON-RPC implementations described above;
row 21 has a partial WebRTC DataChannel implementation described above.
Other rows and remaining native operation modes remain **proposed**,
with implementation and acceptance pending.
Before claiming any adapter complete, record each supported operation, each
actual transport limitation, the real client/device test, and platform status.
Unix foreign-UID acceptance is blocked on this host by unavailable root access;
Linux runtime acceptance lacks a running Docker daemon. The local socket probes
in [ipc-platform-evidence.md](ipc-platform-evidence.md) record the tested Darwin
2048-byte Unix datagram ceiling and unsupported Unix sequenced-packet socket
creation; those adapters and named FIFO sessions remain pending. These concrete test
limitations do not explain unimplemented native operations or other catalogue
rows; lack of implementation remains pending work.

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

Earlier milestones passed checks in the gateway worktree: `go test ./...`, `go test -race ./...`,
`go vet ./...`, targeted fixture/core/config/CLI race tests, request fuzzing,
`go build ./cmd/...`, and `git diff --check`. The disabled-example CLI commands
above were exercised as real processes. A partial configured HTTP service is verified; no complete adapter is claimed.

Publication is local only: no push, installation, deployment, or live AIRC
integration. The primary checkout, live accounts, state, and daemon remain
outside the fixture boundary.
