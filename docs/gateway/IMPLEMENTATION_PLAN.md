# AIRC gateway implementation plan

This is an implementation handoff for a separate service that accepts many connection types and sends authenticated messages into AIRC through one consistent backend interface. It accompanies [the interactive architecture and connection catalogue](airc-connection-gateway.html). Implement **all AIRC functionality each underlying connection type can support**. History, receipt/status lookup, replies, and live updates are required wherever technically supported; search, presence, and other AIRC operations must likewise be assessed against the transport. Authenticated send alone is sufficient only where a concrete connection limitation prevents richer functionality. An initial adapter’s missing implementation is not such a limitation.

The user has clarified that an occasional duplicate is usually preferable to a missing chat post. Accordingly, **ordinary message delivery uses automatic, bounded retries by default**, reusing a stable request ID. Receipt lookup can avoid unnecessary retransmission, but an unknown result does not prohibit another send. AIRC only deduplicates while it retains the request, so this is a delivery-oriented, at-least-once retry policy with possible duplicates, not an exactly-once guarantee. A receipt-only policy remains available explicitly.

This plan is maintained in the dedicated AIRC worktree `/Users/sws/Code/airc-gateway`, on branch `codex/connection-gateway`, created from commit `96f16c3bbb8a3faac2e69684f591af63ceb58450`. Workspace preparation is complete; gateway implementation and fixture validation below remain to be done. Live deployment is a separate action requiring authorization.

## 1 Scope and operating boundaries

The implementer must preserve these decisions:

1. Build a separate gateway service in the dedicated AIRC worktree, using the existing Go module. Keep gateway listeners and state owned by that service; do not add them to the running `aircd` process.
2. Preserve the primary checkout `/Users/sws/Code/airc`, its checked-out branch and dirty files, installed binaries, running daemon, identities, state directories, and active agent conversations. Use `codex/connection-gateway` in its own worktree for task changes. No reset, stash, primary-checkout branch switch, restart, installation, account creation on the live daemon, or posting test messages there is implied.
3. Work in `/Users/sws/Code/airc-gateway`. All fixture accounts, logs, build outputs, and test daemons belong in its dedicated ignored test directories or owned temporary directories. Keep durable runtime state and secrets out of version control.
4. Treat connection types as an unranked catalogue. The implementation sequence below follows dependency order. Adapter tracks can proceed independently after their shared prerequisites exist.
5. One gateway service does not mean one identity, one upstream connection, or a claim that every platform supports every protocol. Preserve caller identities across the boundary.
6. Determine required capabilities from the underlying transport, including configured request/result mailboxes and bidirectional modes. Record concrete reasons for every excluded AIRC operation. A connector with only authenticated send is complete only where connection limitations prevent richer functionality. Reject genuinely unsupported operations locally; do not implement empty history, pretend subscriptions, or substitute a different transport silently.
7. For normal posts, retry transient and ambiguous failures within a configured budget. Do not resend an already confirmed accepted post merely because delivering the response to its caller failed. Do not retry permanent authentication, authorization, or validation failures.
8. A send-only ingress can have no return channel. Authenticate from a provisioned message credential or verified link identity. Record outcomes inside the gateway; do not imply that its sender can observe them.
9. AIRC remains the chat authority. The gateway is not another chat database, task scheduler, agent execution service, or federation system.
10. Keep supported, disabled, unavailable on this platform, awaiting hardware validation, and only proposed distinct in all output and documentation. A list of adapter names is not an implementation.

### Completion scope

There are two independently reportable outcomes:

- **Gateway foundation complete:** the real routing, identity, journal, retry, limits, and backend boundary work end to end; distinct stream, request/response, and send-only packet adapters exercise that boundary against an isolated AIRC daemon.
- **An adapter complete:** its declared capabilities work through an actual transport implementation with the relevant platform or hardware evidence. The catalogue in section 11 specifies the path for every one of the HTML page's 72 entries. Entries that describe HTTP methods, carrier networks, or overlays reuse an implementation rather than requiring 72 separate plugins.

Do not report the entire catalogue implemented when only the foundation is finished. If the implementation brief asks for all entries, continue through the matrix and list concrete hardware or platform blockers individually; do not quietly reduce scope to the demonstration adapters.

## 2 Evidence from the current AIRC implementation

Inspected on 4 October 2026. The checkout was actively changing: HEAD advanced from `3f54f29c8efcb577c91fc217b35299cb3070060b` to `78ebd3b5dc8e828be6d34ccc3c69feda7c4c14b4` during inspection. These notes are not an atomic source snapshot. Step 1 freezes the exact revision used for implementation and repeats the narrow contract checks.

The prepared worktree starts at the newer commit `96f16c3bbb8a3faac2e69684f591af63ceb58450`, whose `go.mod` declares Go 1.27.0. The table below records the earlier investigation; its source links now resolve within this worktree. Revalidate those contracts against the recorded implementation baseline before coding.

| Observed boundary | Implementation consequence | Source |
|---|---|---|
| Go module is `github.com/Someblueman/airc`; the initial inspection used Go 1.25.0, while the prepared worktree declares Go 1.27.0. | Use the worktree's existing module and toolchain directive, importing its exported client. Record the source revision. | [go.mod](../../go.mod) |
| `irc.DialContext` accepts Unix or TCP endpoints, TLS, a connection token, and an account credential. | One backend package can own all AIRC-specific connectivity. | [client configuration](../../pkg/irc/client.go), [registration](../../pkg/irc/connect.go) |
| `Ephemeral` sessions do not claim presence and can send without joining. | Gateway requests need not appear as dozens of temporary room members. Verify that the server actually negotiated the mode. | [protocol](../../docs/PROTOCOL.md) |
| `SendWithID`, `ReplyWithID`, and `RetryRequest` are exported. | Use the real client operations, with explicit gateway retry policy. Do not invoke the CLI per message. | [commands](../../pkg/irc/commands.go) |
| `SendWithID` returning nil means the wire write succeeded, not that AIRC accepted the message. | Read and correlate `SendReceiptEvent` before reporting acceptance. | [send confirmation](../../cmd/airc/send_confirmation.go), [events](../../pkg/irc/events.go) |
| Receipts distinguish `accepted`, `persisted`, and an optional recipient connection snapshot. | Preserve those facts separately. Connected is not read or processed. | [receipt data](../../internal/protocol/metadata.go) |
| Deduplication keys are account ID or guest nickname plus request ID; keys disappear with retained history. `RETRY` only looks up receipts. | Use registered accounts, stable IDs, and a durable gateway ledger. Automatic reposting after an unknown lookup is a gateway policy, and can duplicate a post. | [retry implementation](../../internal/server/retries.go) |
| Public request/result aliases are available as `irc.X`; CLI outbox and MCP connection helpers remain in `package main`. | Use `pkg/irc` for the AIRC boundary, rather than server/protocol internals or `cmd/airc` as a library. Implement gateway-specific persistence and sessions under `internal/gateway`. | [exported types](../../pkg/irc/types.go), [CLI outbox](../../cmd/airc/outbox.go) |
| `RequestCheck` and `RequestChat` require one outstanding request per connection. | A connection lease exclusively owns its event stream. Watching needs separate sessions. | [check](../../pkg/irc/check.go), [chat](../../pkg/irc/room_chat.go) |
| `aircd` owns one listener and a `net.Conn` for each session. | The gateway connects as an ordinary client; no daemon listener refactor is necessary. | [server](../../internal/server/server.go) |
| Direct remote TCP requires verified TLS and a separate access token; account and admin credentials are distinct. | Preserve the existing upstream access boundary. Never give adapters an AIRC operator token. | [access policy](../../internal/server/access.go) |
| Current MCP runs over stdio with a fixed configured identity. The pinned SDK contains an HTTP handler. | Remote MCP is feasible, but multi-caller auth and session isolation must be implemented in the gateway. | [MCP adapter](../../cmd/airc/mcp.go), [SDK at v1.6.1](https://github.com/modelcontextprotocol/go-sdk/blob/v1.6.1/mcp/streamable.go) |

AIRC's existing CLI chooses receipt-only recovery after an uncertain send. That behaviour is useful reference evidence, **not the gateway's default policy**. The user's delivery preference above supersedes that inherited choice for this new service. No change to the existing CLI is required.

## 3 Target architecture

```text
External agent or device
    |
    | Native protocol, authenticated session, or protected message
    v
Transport adapter or narrowly trusted bridge
    | Decode, authenticate, establish operation and return capability
    v
Gateway dispatcher
    | Authorize principal + operation + target
    | Validate limits, expiry, correlation, and duplicate request identity
    v
Durable delivery journal and bounded worker queue
    | Schedule sends, recovery, and delivery-policy retries
    v
AIRC backend binding
    | Per-account exclusive sessions; existing client and wire protocol
    v
AIRC daemon

Result path, where supported:
AIRC receipt/event -> journal -> adapter encoder -> caller
```

The gateway initiates the upstream connection. AIRC does not need to call the gateway, discover plugins, or know which ingress protocol was used. A single logical backend interface can have multiple physical connections for separate callers and event streams.

Use a static adapter registry with explicit configuration. Each listener has a defined protocol; do not inspect arbitrary bytes and guess a protocol. Routing is deterministic configuration plus capability checks, not an LLM, score, or automatic network probe.

### Proposed project layout

These are ownership boundaries, not a requirement to create empty packages up front. Create each module with its first real implementation.

```text
airc-gateway/                  dedicated AIRC worktree; existing go.mod
  cmd/airc-gateway/           daemon and local administrative CLI
  internal/gateway/
    config/                  strict config decoding and validation
    core/                    operation types, capabilities, dispatch policy
    auth/                    principal resolution and credential verification
    store/                   durable journal, replay records, retention
    delivery/                worker scheduling, retry state machine
    backend/                 AIRC SDK sessions, receipts, history, events
    adapter/                 concrete HTTP, stream, packet, broker adapters
    bridge/                  fixed local protocol for optional sidecars
    telemetry/               bounded metrics and redacted structured logs
  docs/gateway/              this plan, contracts, capabilities, operator guide
  examples/gateway/          placeholder configurations and working clients
  testdata/gateway/          public protocol vectors and non-secret fixtures
  integration/gateway/       real daemon and transport acceptance tests
```

Use Go for the gateway because it can directly use AIRC's exported client. Use ordinary functions and concrete types. The genuinely necessary interfaces are the backend submit/query boundary, authenticated ingress submission, and optional event/response sinks. Do not make every connector implement a large interface containing history, watch, and administration.

Native libraries or platform APIs can live in separate bridge processes. They must use the same authorization and delivery path. No dynamically downloaded plugins, arbitrary shell commands from requests, or discovery of executable adapter code in writable directories.

## 4 Common contracts to freeze before adapter work

### Operations and capabilities

Define `send`, `reply`, `receipt.get`, `history.read`, and `watch` from the outset, together with the remaining supported AIRC operations such as `search` and `directory`. Each operation needs its own permissions, validation, and acceptance tests. Implement the full supported set on every capable transport; staged development does not make unfinished capabilities optional. Do not expose raw AIRC commands or administration as an escape hatch.

Each enabled adapter instance exposes a manifest containing:

- Stable adapter ID, protocol version, instance ID, and platform constraints.
- Implemented operations and available response modes: none, one response, stream, or configured mailbox.
- Authentication method and how a verified identity is obtained.
- Maximum encoded frame size, decoded message size, outstanding operations, and connection count.
- Delivery modes it can express and any native acknowledgement meaning.
- Whether its transport needs setup or reverse traffic even when its AIRC feature set is send-only.

Effective capabilities are the intersection of adapter implementation, instance configuration, authenticated principal permissions, and upstream AIRC features. Never trust capabilities supplied by a client. Missing history support is not a degraded send capability.

GET and SSE can be read-side companions. They do not by themselves meet auth + send. Ethernet, a VPN, and HTTP POST also are not three interchangeable plugin implementations: preserve the layer distinctions in the catalogue.

### Request model

Use one internal normalized request, with an explicit schema version. External serializers need not all use JSON, but their decoded operation must have the same meaning.

| Field | Rule |
|---|---|
| `version` | Gateway contract version, initially 1. Reject unknown versions. |
| `principal_id` | Set only by successful authentication or a narrowly authorized trusted bridge. Never take it from an unverified body. |
| `request_id` | Stable per logical operation and principal. Required on retry-capable ingress. A native event ID can be mapped deterministically if it has a documented scope and reuse window. |
| `operation` | Explicit allowlisted operation. Baseline is `send`. |
| `target` | Typed room or direct-message target. Exactly one. No comma-separated multi-target sends. |
| `message` | Valid UTF-8 text. Normalize line endings once; reject NUL and excess bytes rather than truncating. |
| `issued_at`, `expires_at` | Authenticated operation lifetime. Expiry stops future submission attempts, not recording a receipt that arrives later. |
| `delivery_policy` | `retry` by default for ordinary posts; optionally `receipt_only`. Principal and adapter configuration can restrict it. |
| `reply_route` | Optional handle established by the adapter. Never an arbitrary client-supplied URL, hostname, filesystem path, or broker topic. |
| `cursor` | Only for a read/watch operation. No shared cursor advancement as a hidden send side effect. |

The storage key is `(gateway_realm, backend_binding, principal_id, request_id)`. `backend_binding` is a stable configured AIRC destination and identity binding, not an ephemeral socket address. Freeze that binding on first ingestion. Do not silently move pending operations to another daemon or author when config changes.

Compute a content fingerprint from normalized operation, target, message, relevant arguments, delivery policy, and expiry. Define the encoding explicitly and test it; avoid hashing arbitrary map iteration or reserialized JSON. Reusing an ID with different content is a conflict. Retrying the same operation over HTTP and UDP coalesces if both credentials map to the same principal and binding.

Resolve omitted policy/lifetime defaults once at first admission and persist them. A repeated session-authenticated request with the same omitted fields reuses those stored values; it must not receive a newly computed expiry and conflict with itself. Preserve which fields were explicit in the fingerprint contract. Signed profiles require explicit authenticated validity; cross-adapter coalescing requires the same effective intent and lifetime. Perform lookup/admission atomically so simultaneous first requests cannot establish different defaults.

Derive an AIRC-compatible request ID deterministically from that scope, for example a domain-separated SHA-256 hex digest, which fits the inspected 64-character limit. A credential rotation must not change the logical request ID or AIRC author. Key identity and operation identity are separate.

### Results

Return a structured result where a channel exists, otherwise retain it internally:

- Gateway state and request ID.
- Whether the gateway durably accepted responsibility for retrying.
- Original AIRC message ID and receipt when known.
- `accepted` and `persisted` as separate upstream facts; unknown is not false.
- Duplicate risk, separate send/lookup attempt counts, deadline, and the next permitted action.
- A stable error code with permanent/transient classification.

`gateway_queued`, `airc_accepted`, `airc_persisted`, and `recipient_connected` describe different observations. None means a human or agent read the message. A failed client response does not undo an accepted post.

### Proposed initial resource limits

These are design defaults for implementation and measurement, not measured performance claims. Keep them configurable within validated hard ceilings.

| Resource | Starting value |
|---|---|
| Decoded message body | 4096 UTF-8 bytes, or a smaller adapter/upstream bound |
| General request envelope | 16 KiB before expensive processing; no compression initially |
| UDP wire packet | 1200 bytes maximum; no gateway-level fragmentation initially |
| Delivery workers | 8 globally; at most 1 actively submitting mutation per principal |
| AIRC connections | 32 globally, at most 4 per principal, with a send reserve |
| Watch connections | At most 8 globally and 2 per principal, within the total limit |
| Pending durable sends | 4096 globally and 128 per principal |
| Journal storage | 256 MiB cap including a monitored allowance for WAL/checkpoint growth |
| Ordinary post expiry | 24 hours after authenticated creation; adapter may require shorter validity |
| Transient retry delay | Exponential backoff with jitter, starting at 1 second and capped at 60 seconds |
| Maximum automatic send attempts | 100 per operation, also bounded by expiry |
| Maximum automatic receipt lookups | 100 per operation, counted separately from sends and bounded by the same expiry |
| Overall automatic backend attempt deadline | 30 seconds, capped at the operation's stored expiry |
| Backend phase limits | Dial/registration 10 seconds; write 5 seconds; receipt or lookup response 10 seconds; each capped by the remaining overall deadline |
| Terminal result retention | 7 days, subject to documented capacity; never silently prune unresolved records |
| Shutdown drain | 10 seconds, then preserve pending/uncertain work for restart |

Enforce byte limits as well as counts. A UDP connector can accept a substantially smaller body than 4096 bytes because authentication and framing consume packet space. Measure the encoded size before submission; do not split a chat message into multiple posts automatically.

## 5 Authentication and permission design

Keep three credential classes separate: external gateway credentials, AIRC connection/account credentials, and AIRC operator credentials. The runtime gateway requires the first two only. An account-provisioning command is a distinct local administrative action; request processing never creates accounts implicitly.

For the foundation, implement high-entropy per-principal bearer credentials over verified TLS and provisioned signed message envelopes. Store credential references and verifier data, not plaintext secrets in request records or logs. Owner-only files or platform-equivalent ACLs hold private keys and upstream account credentials. Prefer one registered AIRC account per independent external principal. An intentional shared identity must be explicit in configuration.

For browser access, define allowed origins, CORS behaviour, and an authentication mechanism browsers can actually send. Do not put long-lived secrets in URLs. If cookie sessions are added, add CSRF protection and origin checks. Native WebSocket and EventSource browser APIs do not give arbitrary HTTP-header control; a tested same-origin session or fetch-based client is required rather than an impossible example.

For message-based or physically one-way links, authentication must survive without an interactive login. Use an established signed-envelope format and a mature library. A concrete default is compact JWS with Ed25519 signatures and an allowlisted algorithm, protected key ID, and signed version, audience, request ID, operation, target, payload, and validity window. Verify the received signed bytes before normalization; do not reconstruct signed JSON from parsed fields. Reject duplicate object members, unknown critical headers, untrusted embedded key URLs, algorithm substitutions, and unsigned forms. The library's protocol vectors plus independent tamper/replay checks are required. [JWS](https://www.rfc-editor.org/rfc/rfc7515.html) and [Edwards-curve JOSE](https://www.rfc-editor.org/rfc/rfc8037.html) define the underlying formats; this envelope policy is a proposed application contract.

Signatures authenticate data but do not hide it. Preserve remote confidentiality: use TLS, DTLS, QUIC, or a standard encrypted envelope on exposed packet/carrier paths. For a generic one-way public packet profile, a proposed supported combination is signed JWS inside JWE, using a pinned gateway recipient key, `ECDH-ES` with an approved P-256 implementation, and `A256GCM`. Use a maintained JOSE implementation, exact algorithm allowlists, bounded decryption, and published test vectors; do not implement cryptographic primitives. Encryption alone does not authenticate the sender. [JWE](https://www.rfc-editor.org/rfc/rfc7516.html) and [JWA](https://www.rfc-editor.org/rfc/rfc7518.html) supply the primitives, not an automatic AIRC identity policy. A constrained device that cannot fit this profile needs a separately specified native security profile such as OSCORE or an authenticated local device bridge; it must not silently downgrade.

Require expiry/freshness or a native monotonic replay scheme appropriate to the carrier. Persist replay acceptance together with the accepted request. A duplicate authenticated ingress with the same ID and body returns or references the existing delivery job; it never schedules concurrent duplicate workers. This does not prohibit the gateway's authorized automatic retransmission of that job to AIRC. Separate replay protection from the chat delivery policy.

For native events without a caller request ID, define the stable ID from authenticated native sender/session/counter/event fields. If that protocol cannot distinguish a repeated delivery from a new identical event, document that duplicates may enter as distinct messages; do not claim exact deduplication. Reject reuse with conflicting data whenever the native identity scope makes that detectable.

Key rotation adds a new key to the same principal; revocation stops new ingestion and queued dispatch according to explicit policy. It must not make pending work appear to belong to a different AIRC account. A source IP, MAC address, device label, email From header, or file name is not sufficient authentication.

Gateway target and operation allowlists apply before contacting AIRC. Configure explicit rooms and DM recipients or a documented matching syntax; do not allow an arbitrary destination because the underlying account could technically reach it. AIRC still makes the final chat permission decision. Its trusted archive and all-DM model means the gateway must not advertise private multi-tenancy without a separately designed access boundary.

## 6 Durable delivery and retry state machine

The journal is required even for send-only adapters: it lets the gateway distinguish accepted responsibility from a transient in-memory read. Use a local SQLite database through a pinned Go driver, with explicit schema versioning, short transactions, durable commit settings, and one daemon owner. A pure-Go driver such as [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) is a proposed dependency, to be pinned to a Go-compatible version when implementation starts.

Use WAL with `synchronous=FULL`, verify actual pragma results, and test disk-full/checkpoint failures. Keep database, WAL, and shared-memory files on local storage with restrictive permissions; do not place WAL state on a network filesystem. These are requirements derived from [SQLite WAL behaviour](https://www.sqlite.org/wal.html) and [synchronous settings](https://www.sqlite.org/pragma.html#pragma_synchronous), not claims that every disk failure is survivable.

The initial schema needs request identity/fingerprint, normalized content, pinned upstream binding, state, separate send/lookup attempt counts and next-attempt times, expiry, last error, accepted receipt, and duplicate risk. Each dispatch marker records an attempt ID, its absolute deadline, and the principal/binding authorization generation under which it was granted. Add adapter-source delivery identity and replay records where needed. Optional read cursors and callback jobs arrive only with those features. Do not store credentials in this database.

| State | Meaning | Allowed next action |
|---|---|---|
| `queued` | Authenticated, authorized, validated, durably recorded; no submission is currently in progress. | Submit when ready, unless expired/revoked. |
| `dispatching` | An attempt marker was committed before any network write. | Await receipt; after a crash assume it may have posted. |
| `retry_wait` | Retryable failure or unresolved submission under delivery-oriented policy. | Try receipt recovery if appropriate, then repost with the same ID when due. |
| `uncertain` | Submission may have occurred; receipt-only policy prohibits reposting, or retry budget has ended. | Receipt lookup or explicit operator/client resolution. |
| `accepted` | Matching AIRC acceptance receipt durably saved. | Return/replay the saved result; never automatically post again. |
| `rejected` | Definitive permanent failure or policy denial. | Return/report failure; a corrected message is a new operation. |
| `expired` | No more sends are allowed by lifetime policy. | Preserve what is known; if prior writes occurred, say acceptance is unknown. |

Keep acceptance uncertainty as a field independent of the scheduling state. An expired request that attempted a write is not proof of no post. Preserve `persisted=false` as accepted-but-not-persisted; do not retry a known accepted post just to make it durable.

A permanent rejection of a later attempt does not prove an earlier uncertain attempt failed. Preserve that earlier uncertainty. If an acceptance receipt was seen but storing it fails, keep it in memory, stop further sends for that job, and retry saving the result; do not knowingly post again. If the process then crashes before persistence, recovery can only use durable evidence and may still duplicate under the default policy. Document that storage-failure boundary rather than promising exactly-once behavior.

### Dispatch algorithm

1. Bound and parse ingress, authenticate, authorize, and derive the normalized operation and stable ID.
2. In one transaction, reject conflicts, coalesce duplicates, enforce quotas, and record the new request plus replay identity. Commit before returning `gateway_queued` or acknowledging a broker delivery as gateway-owned.
3. Acquire a fair worker slot and an exclusive session for the stored principal/backend binding within the attempt deadline. For uncertain jobs, choose a bounded lookup or resend according to the recovery rules below. Recheck expiry and current revocation policy when granting the selected action.
4. Before a send, use the per-principal/binding dispatch gate to atomically order the final authorization check and committed `dispatching` marker against revocation. Increment the send count and record the attempt deadline and authorization generation. A commit failure means no network write is permitted. Release the gate before network I/O; a grant committed before revocation permits only this one bounded attempt, not a later retry or reconnect.
5. Await a matching receipt. On acceptance, persist the receipt before sending a success response. If response delivery fails, retain accepted state; only replay the response on another request.
6. On a permanent server rejection, record it. On transient unavailability, rate limit, timeout, partial write, disconnection, or crash uncertainty, classify the result without assuming no post occurred.
7. Under `retry`, schedule a bounded retry. For an uncertain attempt, first use `RetryRequest` on a clean session where practical, subject to its separate budget. A matching retained receipt resolves the job. An unknown receipt, an exhausted lookup budget, or a transient lookup failure followed by a usable send connection permits reposting the same body with the same request ID while send budget and lifetime remain. Set duplicate risk whenever prior acceptance cannot be excluded.
8. Under `receipt_only`, perform bounded lookup-only recovery and retain uncertainty; never repost automatically. This is opt-in for ordinary chat sends. Lookup exhaustion leaves the job unresolved without further automatic recovery traffic.
9. After restart, reclassify unfinished `dispatching` attempts as uncertain. An old dispatch grant cannot authorize a new network attempt after restart; recheck current authorization. Resume according to the stored policy, counters, deadlines, and schedules, not new defaults or reset budgets.
10. Exhausting the send budget stops reposting; remaining lookup budget may still recover a receipt before expiry. Expiry stops all new automatic network attempts. Retain a visible unresolved/expired result, while allowing a valid acceptance receipt already observed or subsequently obtained by an explicit authorized lookup to resolve it. Do not claim guaranteed delivery. An operator may explicitly create a replacement post, with a new ID and clear duplicate risk, but no silent indefinite loop.

### Receipt lookup scheduling and limits

Persist `lookup_attempts` and `next_lookup_at` independently of send attempts. Increment the lookup count before starting each automatic recovery probe, including its dial/registration, so a failed connection or a crash consumes that probe rather than granting unlimited free attempts. Probes use the same bounded exponential backoff with jitter as sends; lookup time counts against the stored operation lifetime. Restart preserves both counts and next-attempt times.

Allow at most one recovery probe before each scheduled resend; a missing or transiently unavailable receipt must not start an unbounded inner lookup loop. Under `retry`, exhausting the lookup budget does not consume or disable remaining send attempts. Under `receipt_only`, exhausted probes or expiry stop automatic work and leave uncertainty visible. Permanent authentication or authorization failure still blocks the prohibited action; it is not permission to fall back to sending.

Ordinary status reads return the journal result and do not replenish either budget or extend expiry. An optional upstream lookup triggered by a normal client status request uses the same bounded lookup budget. After budget/lifetime exhaustion, an explicitly authorized local operator may request a separately rate-limited, single receipt lookup; it has a fresh read deadline of at most 30 seconds with the same phase limits, independent of the expired send lifetime. That read cannot reset budgets, authorize a resend, or reopen the delivery schedule.

Classify a known rate-limit rejection with the server's retry delay. Authentication failure, permission denial, malformed content, conflicting request ID, or unsupported operation is permanent until configuration or the request changes. Retrying identical bad credentials creates no useful delivery opportunity.

Keep retry scheduling outside caller request cancellation. Once durable responsibility is accepted, a dropped HTTP connection does not cancel delivery. An explicit cancellation, if implemented, can prevent queued future attempts but cannot retract a write already in progress. Shutdown stops new admission, drains for a bound, and leaves unfinished jobs recoverable.

### Duplicate and retention rules

Maintain the same request ID on every automatic submission attempt, including after a missing receipt. While AIRC retains that ID it can return the original message; after eviction the resend can create a new message. That is an accepted tradeoff for the default policy. Two newly generated IDs for the same intention are two operations; message-text similarity is not deduplication.

Retain terminal results for the advertised gateway window and replay markers at least through signed validity plus clock skew. Do not promise deduplication beyond those windows. Requests using expired signed envelopes cannot become fresh merely because the result record was pruned. For session-authenticated callers, document that a request ID must not be reused after the published retention window and expose that window in capabilities. Unknown old IDs are not evidence of a past failure.

Broker acknowledgements and upstream retries have separate meanings. A broker ACK after a durable journal transaction transfers retry responsibility to the gateway; it does not certify an AIRC post. An ACK that fails can produce a redelivery, which the journal coalesces. A packet never received by the gateway has no journal entry: one-way senders need their own bounded repetition policy if they want higher delivery probability.

## 7 Implementation sequence

Each step specifies a deliverable and an exit check. Steps 1–7 establish the shared foundation. The adapter tracks then follow the same contract without ranking the connection types. All paths below are relative to the dedicated AIRC worktree unless explicitly absolute.

### Step 1 — Establish an isolated, reproducible workspace

1. Read the instructions applicable to this worktree and inspect its Git state. Record the exact AIRC baseline commit, dirty status, Go version, and exported SDK contract in `docs/gateway/upstream-contract.md`. Do not assume the earlier investigation in section 2 is still current.
2. Use the prepared worktree `/Users/sws/Code/airc-gateway` on `codex/connection-gateway`; verify both before editing. Keep the primary checkout's branch, dependencies, and files untouched. Worktree creation and plan import are already complete; do not create a second project or worktree to repeat them.
3. Use `github.com/Someblueman/airc/pkg/irc` from the existing module in this worktree. Keep gateway code under `cmd/airc-gateway` and `internal/gateway` as above; do not add a self-dependency, nested Go module, or `replace` pointing at the primary checkout. Record any deliberate later baseline change and recheck affected contracts.
4. Build a fixture `aircd` from an immutable export of the recorded baseline, using `git archive` into an owned temporary or ignored fixture directory when needed. Record its full commit and export checksum; preserve notices. Give tests their own temporary home/config/state directories, registered accounts, TLS certificates, room names, and Unix socket or dynamically assigned loopback ports. Never fall back to `127.0.0.1:6667`, the installed CLI's defaults, or the user's existing AIRC state.
5. Put fixtures under explicit process ownership, record the PID of every child started, and terminate only those children. Do not use broad process-name kill commands.
6. Pin dependency versions and checksums. Select compatible libraries for SQLite, JOSE, and each implemented transport when the relevant step begins. Check their actual Go/toolchain/platform requirements; do not upgrade the AIRC module to obtain an unrelated gateway feature.

**Exit check:** a clean checkout of the gateway branch can build its pinned fixture and start/stop that fixture without touching the primary AIRC checkout or listener. Record reproducible commands and versions. Workspace creation alone does not satisfy this fixture acceptance check.

### Step 2 — Freeze the external contract and configuration

Create `docs/gateway/contract-v1.md`, `docs/gateway/configuration.md`, and a placeholder-only example config. Implement the small types in `internal/gateway/core` and strict configuration parsing in `internal/gateway/config`.

1. Define operation/result enums, the exact JSON field names and types, the canonical fingerprint encoding, request ID syntax/length, and error codes. Distinguish `authentication_failed`, `forbidden`, `unsupported_operation`, `invalid_request`, `id_conflict`, `capacity_exceeded`, `upstream_unavailable`, and `delivery_uncertain`.
2. Define target validation using the pinned AIRC contract. Reject embedded protocol commands, CR/LF/NUL, empty or multi-target destinations, and reserved administrative/audit selectors. Do not lowercase room names: inspected AIRC room matching and nickname matching differ.
3. Define config for gateway realm, immutable backend bindings, principal-to-account mappings, credential references, target/operation allowlists, adapter instances, limits, retry/retention settings, and explicit return routes. No request may override the upstream address, account credential, or adapter destination.
4. Start with explicit configuration reload by restart. If live reload is later implemented, make it atomic, keep admitted jobs' bindings stable, and specify revocation effects. Avoid a half-reloaded identity/credential map.
5. Add local `config validate` and `capabilities` commands. A daemon must reject unknown fields, duplicate listener definitions, invalid key permissions, insecure credential-bearing remote listeners, and advertised operations without implementations.
6. All network ingress is disabled until configured; enabled IP listeners bind loopback by default. Exposing a listener requires an explicit address and its configured authentication mechanism. Unix sockets live in an owner-controlled directory. No generated example starts a public unauthenticated service.

**Exit check:** table-driven contract tests cover normalization, UTF-8 byte boundaries, target injection, conflicts, unknown versions/fields, capability intersection, and safe defaults. Validate examples through the same parser used by the daemon.

### Step 3 — Implement principal resolution and authorization

Implement `internal/gateway/auth` and a local, owner-authorized credential provisioning command. It can create gateway credentials and public-key bindings; it must not create accounts on the live AIRC daemon.

1. Implement bearer verification with high-entropy credentials, constant-time comparison where relevant, redacted failures, and no credential reflection. Resolve a stable principal independently of the current credential ID.
2. Implement the signed-message profile from section 5 using a pinned library, published test vectors, audience binding, bounded clock skew, expiration, replay identity, and explicit algorithm/key allowlists. Default clock skew allowance: 60 seconds; a signed request may not choose a lifetime beyond the configured maximum.
3. Implement the encrypted-message profile where an untrusted one-way carrier requires confidentiality. Invalid ciphertext/signatures never reach the dispatcher. Rate-limit verification by listener and authenticated identity without treating a spoofable source address as an identity.
4. Implement exact operation and destination permissions. Apply them to send, receipt lookup, history selectors, streams, and every trusted-bridge mapping. Default deny. A valid credential alone does not authorize all rooms or other principals' receipts.
5. Define revocation using one per-principal/binding dispatch gate shared by intake authorization, send grants, and the local revocation command. While holding that gate, commit the revoked state and increment its authorization generation, or commit a dispatch grant after verifying the current generation and expiry. Revocation takes effect at its commit: new intake and new attempt grants then fail. A grant committed earlier may finish its single attempt within the original deadline, even if its first network write follows the revocation commit; record it as authorized before revocation. Never hold the gate during network I/O. Restart, reconnect, and resend require a new grant and cannot reuse the old exception. Retain job state for explicit local resolution and preserve any acceptance received from the permitted in-flight attempt. Do not invalidate a known result merely because a client token rotates.

**Exit check:** actual requests with valid, invalid, expired, wrong-audience, tampered, replayed, rotated, and revoked credentials behave as specified. Failed authorization makes no backend send call. Test revocation before the grant, after the grant but before the write, and during the write; only an already committed grant may finish its one bounded attempt. One principal cannot read another's request or supply its identity in a body. Exercise both session authentication and detached message authentication.

### Step 4 — Implement durable intake, replay coalescing, and bounded storage

Implement `internal/gateway/store` with migrations, transactions, and a single owner lock for the database directory.

1. Create the schema in section 6, unique scoped request identity, and indexes for runnable jobs and retention. Use integer or explicitly encoded times with one documented precision. Do not rely on lossy floating-point timestamps in a signature or fingerprint.
2. Make new-job admission, replay recording, and quota accounting one transaction. The same ID/body through simultaneous adapters returns the same job. A mismatched body or policy is a conflict. A transaction failure must never produce a queued response or broker ACK.
3. Persist dispatch attempt markers before I/O and acceptance receipts before returning acceptance. Persist lookup reservations before their network work. Order dispatch grants and revocation commits through the shared gate described in Step 3. Guard state transitions so an old worker cannot overwrite a newer accepted result. Use a generation/attempt token when an asynchronous completion can race recovery.
4. Implement restart recovery, pruning, disk-full handling, and WAL/checkpoint limits. Stop new intake before exceeding capacity. Preserve unresolved jobs and the minimum advertised deduplication window; if those cannot fit, apply backpressure instead of silently shortening the promise. Allow deletion of expired signed replay entries only after their full validity and skew window.
5. Version the schema and refuse to run a binary against a newer unsupported schema. Supply a SQLite-aware online backup procedure or a stopped-service backup that preserves a consistent database, rather than copying only the main file while WAL writes continue.

**Exit check:** real on-disk tests survive process termination at each transaction boundary. Concurrent duplicate intake yields one durable operation. Disk-full or commit failure produces no successful intake claim. Restart recovers pending work and saved acceptance results without depending on memory.

### Step 5 — Build the AIRC backend and prove acceptance semantics

Implement `internal/gateway/backend` against the public `pkg/irc` contract. It may use the gateway's own internal packages; do not bypass the client boundary by importing `internal/server`, `internal/protocol`, or `cmd` implementation packages.

1. Create sessions from a configured account binding using `DialContext`, `Ephemeral: true`, explicit network/address, TLS verification and access token where required, and `CreateAccount: false`. Use `Reconnect: false` initially so delivery recovery has one owner. Always call `Close`: cancellation of the dial context alone does not own the established client's lifetime in the inspected SDK.
2. Verify ephemeral mode and the required features after registration. The initial backend requires `RECEIPTS`, `IDEMPOTENCY`, and `SAFE_RETRY`; inspect actual values rather than merely seeing a token. AIRC currently ties the last two to enabled retention. This internal requirement does **not** require a connector to expose history. Report incompatible upstream configuration rather than silently removing stable IDs or altering the running daemon.
3. Give one operation exclusive ownership of a session's `Events()` stream. Do not let multiple goroutines compete for events while calling synchronous SDK helpers. Start with bounded short-lived leases; add idle reuse only if it remains simple and measurably useful.
4. Send with `SendWithID`, then await the correlated `SendReceiptEvent`. Match request ID, account binding, target, and normalized content; preserve the full receipt and message ID. A nil write error, self-echo, socket flush, or a broker ACK is not the gateway's acceptance signal.
5. Give each automatic send attempt or receipt lookup an explicit overall deadline of `min(start + 30 seconds, stored expiry)`; section 6 defines the separate one-shot operator lookup exception. Within it, cap dial/registration at 10 seconds, writes at 5 seconds, and waiting for a receipt/lookup result at 10 seconds. Configure SDK `ReadTimeout`/`WriteTimeout` explicitly and pass a deadline-bearing context to `DialContext`; SDK idle/read timeouts alone do not bound a receipt wait while unrelated traffic continues. The backend owner must enforce the absolute deadline, close the SDK client to interrupt a blocked write/read, and wait for owned work to stop before releasing its slot or starting another attempt. On a deadline, partial write, disconnected stream, or unmatched/unparseable receipt, preserve acceptance uncertainty where a write may have occurred and close/discard the lease. A valid correlated acceptance already observed must still be persisted even if expiry passes during that commit; timeout completion cannot overwrite acceptance. Late replies must not be consumed as the next request's result. Reject permanent correlated AIRC errors and carry their codes through a stable gateway classification.
6. Implement receipt-only lookup with `RetryRequest`: retained receipt resolves acceptance; numeric `488` means unknown, not rejected. Apply the separate durable probe counter, backoff, and expiry rules in section 6. Scope all lookups to the same registered account used for the original send. Do not invent a new nickname/account to retry a pending operation.
7. Cap total and per-principal sessions and reserve capacity for sends. Never borrow an idle session authenticated as someone else. Saturated pools return/schedule bounded backpressure, not an unbounded goroutine queue.

**Exit check:** an independent observer of the fixture sees the correct author, room/DM target, body, and message ID. Verify retained-ID deduplication, receipt lookup, `persisted` distinctions, identity isolation, and server feature rejection through the real wire protocol. Stall registration, block a write, and withhold a receipt while continuing unrelated traffic: each attempt must exit by its configured deadline, close its session, release its worker, and preserve uncertainty without requiring a process restart.

### Step 6 — Wire the dispatcher and delivery workers

Implement `internal/gateway/delivery` and the application lifecycle around the state machine in section 6.

1. Connect authenticated ingress to authorization, normalization, durable admission, and the worker scheduler. Adapters cannot call the AIRC SDK directly.
2. Use bounded workers and per-principal fair scheduling. A principal flooding the queue cannot consume every execution slot while other admitted principals starve. Backoff uses jitter and respects any valid upstream rate-limit delay.
3. Attempt receipt recovery before an uncertain resend where possible, at most once per scheduled resend and within the independent lookup budget. Repost the same request ID under the default policy after unknown acceptance, including after a crash. Cap send attempts, lookup attempts, and lifetime separately as specified in section 6. Under `receipt_only`, recover only; do not repost.
4. Store retry policy, lifetime, binding, both attempt counters, and both next-attempt times with the job. New defaults or a restart must not reset budgets or change old jobs' semantics. Persist enough timing information to avoid a restart causing every job to retry simultaneously; clamp unreasonable clock changes without extending authenticated expiration.
5. Once admitted, request cancellation closes the caller's waiter only. Worker lifetime belongs to the daemon. Before admission, cancellation may abort work. Graceful shutdown stops listeners, finishes safe transactions, drains workers within the configured bound, closes SDK sessions, and leaves remaining work recoverable.
6. Add structured logs and low-cardinality metrics for intake, auth denials, queue size/bytes, attempt outcomes, duplicate risk, disk usage, session usage, and dropped/overflowed read streams. Omit message bodies and secrets by default. Do not use request IDs, target names, or credentials as unbounded metric labels.

**Exit check:** transient outages resume automatically and eventual posts are visible to the independent observer. Accepted-response loss does not produce another post. Unknown acceptance after AIRC retention loss can produce another post under the default policy, and that outcome is reported honestly. Shutdown and restart leave no orphaned fixture connections or lost admitted jobs.

### Step 7 — Implement representative ingress and a reusable conformance suite

Implement the HTTP request/response, framed Unix/TCP stream, and authenticated one-way UDP paths described in section 8. They exercise different semantics; their role here is to validate the common core, not to rank the full catalogue.

1. Drive each through its real socket/client. The common suite sends under two principals, tests unsupported operations and limits, repeats an ID, conflicts an ID, simulates an upstream outage, and confirms the observed AIRC post.
2. Repeat one logical signed operation through two distinct adapters and verify it coalesces into one journal entry. Revoke its principal and verify that switching transports does not bypass the restriction.
3. Demonstrate a one-way sender that closes immediately after transmission. After the gateway records it, retry must continue through an AIRC outage without any session or return path to that sender.
4. Make capabilities discoverable where a return path exists, and publish equivalent static manifests for one-way links. Unsupported operations are rejected before upstream allocation.

**Exit check:** the foundation completion criteria in section 1 pass against the real fixture. No adapter has its own retry database, identity map, or direct AIRC command path.

### Step 8 — Implement required replies, receipt, history, and live delivery

This step is required wherever the underlying connection supports these capabilities. HTTP, bidirectional streams, broker request/result topics, and mailbox request/result paths must expose them. A configured ingress-only profile does not excuse missing richer modes of its underlying transport. Record concrete transport limitations for every exclusion.

Implement replies with `ReplyWithID` through the same authorization, durable intake, stable-ID, receipt, and retry path as sends. Authorize the retained parent’s room or DM participants before submission; never let a parent ID bypass target permissions.

1. Implement receipt retrieval from the gateway journal with principal/binding authorization. An optional AIRC lookup must not turn a status query into a new send, reset budgets, or bypass section 6's lookup limits. Return unknown/expired honestly after gateway retention ends.
2. Implement bounded history via the pinned SDK's check/history APIs, preserving server cursors, limits, and retention-gap status. Use an exclusive session. Do not read or advance the existing CLI's cursor files.
   AIRC's trusted-local archive access is broader than an external agent's permissions. Authorize selectors before querying and verify returned records against the principal's allowed rooms or DM participants before encoding them. In particular, do not expose all-DM audit selectors or another nickname's inbox through a generic target field. If the pinned metadata cannot establish the required participant boundary, disable that read profile until a concrete safe mapping exists; send support remains independent.
3. Define opaque authenticated cursors bound to principal, backend binding, operation, selectors, and version. Never interpret an untrusted cursor as a filesystem path, query, or permission grant. A cursor resumes position; current permissions must still be checked.
4. For watch, dedicate one connection and one event-stream owner. Call `Observe` for the authorized targets, then consume and validate a raw numeric `765` acknowledgement for every requested target before starting catch-up. `Observe` returning nil confirms only the command write. Buffer live events from the start of subscription setup, including events arriving between individual acknowledgements. Missing acknowledgements, rejection, timeout, or buffer overflow fail the whole setup and close its session; do not silently continue with a partial subscription. Once all acknowledgements arrive, prefer `RequestCheck` on that same connection and route its `other` callback into the bounded live buffer. If using `HistoryAfter` instead, collect matching history events explicitly and stop only at `EndOfHistoryEvent` within the snapshot deadline; that method does not collect a reply itself. Preserve end status/cursor and buffer unrelated live messages throughout. Recheck these concrete protocol details against the pinned AIRC version during Step 1.
5. Merge catch-up and buffered events by AIRC message ID and the available upstream sequence/cursor semantics. Do not sort opaque IDs lexicographically or claim a total order that AIRC does not provide. Continue from a proven watermark; report a retention gap instead of silently skipping it.
6. Reconnect a failed watch with bounded backoff and explicit catch-up. A slow consumer or buffer overflow gets a resumable disconnect/gap outcome; it cannot hold an unlimited buffer or block other principals. Cancellation releases its lease.
7. Keep history bodies, metadata, and omission/gap fields intact within the negotiated byte budget. Unsupported selector combinations are errors, not partial success. Return routes use preconfigured authenticated destinations only.

**Exit check:** messages emitted at every subscribe/snapshot/reconnect boundary appear without silent loss within available retention. Inject messages before, between, and after target acknowledgements and during catch-up; verify bounded buffering and merge behavior. A missing or rejected acknowledgement must prevent snapshot startup and leave no partial live subscription. Retention expiry and slow consumers report gaps. No principal receives another's unauthorized stream, and a watch failure never triggers a repeat send.

### Step 9 — Complete the independent adapter tracks

Apply sections 8–11 to every entry included in the implementation brief. Track each concrete protocol separately when one catalogue row names several standards.

For each adapter, produce its implementation, manifest, example client or device procedure, auth/provisioning instructions, native failure mapping, and a transport-level acceptance test. Reuse the common conformance suite. Platform-only adapters use build tags and explicit unavailable status on other systems. Optional dependencies and device permissions belong only to enabled adapters.

If a sidecar is necessary, implement the narrow bridge in section 10 first. Do not claim a protocol supported merely because a generic stdin importer could hypothetically receive bytes from it. A carrier/overlay entry instead needs a verified deployment recipe showing the existing adapter working through that path.

**Exit check:** each row has a status and evidence. A simulated radio validates framing logic only; actual platform/hardware validation is required to claim that device path works. Unavailable hardware is a concrete open item, not a reason to relabel the row complete.

### Step 10 — Run integrated acceptance, package, and hand off

1. Run the failure and security scenarios in section 12 and the required project checks. Use fixture-only fault injection and clean-state first-use procedures.
2. Package the gateway with config validation, migration/version checks, a capability/status command, and owner-controlled state directories. Test on each claimed target OS. A cross-compiled binary is not proof that its native IPC/device adapters work.
3. Write `docs/gateway/operations.md`: provisioning, starting/stopping, disabling an adapter, credential rotation/revocation, queue inspection, uncertain outcomes, retry policy, storage limits, backup/restore, schema compatibility, and a rollback procedure. Include working authenticated client examples with placeholders and generated fixture credentials, never real user secrets.
4. Prepare a staging config and demonstrate the complete experience against a disposable AIRC fixture. The handoff states what works, what was verified, and what is blocked. Keep production installation and live AIRC connection separate from this fixture demonstration.
5. Commit only task-owned changes in the dedicated worktree after required checks pass. Never stage the primary AIRC checkout. Do not push, publish, install globally, or connect this gateway to the user's live daemon without authorization for that action.

**Exit check:** another agent can check out the gateway branch, follow the documented setup, authenticate from a supported client, and observe the resulting AIRC message using a separate observer. The handoff records commit, dependency versions, commands/results, adapter evidence, remaining limits, and deployment state.

## 8 Direct adapter recipes

These recipes are implementation specifications. They do not imply that the gateway or any listed listener already exists.

### HTTP, polling, SSE, WebSocket, RPC, and MCP

Use one HTTP handler tree behind explicitly configured HTTP/1.1 and HTTP/2 TLS listeners. HTTP/3 can reuse the semantic handlers through a separately tested QUIC-capable server. Pin actual library/protocol versions when implemented.

| Proposed endpoint | Meaning |
|---|---|
| `POST /v1/messages` | Authenticated send with a required stable request ID. Return `202` plus the durable request resource after journal commit. If an explicitly requested bounded wait completes, return a structured accepted/result response. |
| `GET /v1/requests/{id}` | Authorized gateway status lookup; never sends or retries a post as a side effect of this read. |
| `GET /v1/capabilities` | Effective capabilities and limits, scoped after authentication. |
| `GET /v1/history` | Required authorized cursor-based history. |
| `GET /v1/events` | Required ordinary gateway SSE stream with resumable cursor semantics. This is separate from MCP transport. |
| `GET /healthz` | Minimal liveness, no secrets or message data. Detailed readiness remains local/authenticated. |

Return `401/403` for authentication/permission failures, `409` for ID/content conflicts, `413` for oversized content, `429` for principal admission limits, and `503` for unavailable durable intake. A job already durably admitted should remain discoverable even when AIRC is offline; do not report it as definitively unsent. Disable shared caching for authenticated responses. If both header and body provide an ID, require agreement.

GET is a safe read method: do not implement chat sends in GET URLs. URL crawlers, retries, caches, and prefetch would make that interface misleading and leak parameters. `PUT /v1/messages/{id}` may be offered as an idempotent resource-creation equivalent with the same fingerprint rules; it is not required for POST support. PATCH/DELETE need explicitly defined editable/cancellable resources and cannot retroactively undo an AIRC post. These distinctions follow [HTTP semantics, RFC 9110](https://www.rfc-editor.org/rfc/rfc9110.html).

Long polling is a bounded read with a deadline. SSE is a downstream stream paired with POST for sends. WebSocket carries versioned bounded messages with separate request/result IDs and heartbeat/deadline handling; authenticate the handshake or a strictly bounded pre-auth phase before admitting any operation. Enforce origins for browser credentials. Do not accept arbitrary upstream headers, URLs, or CONNECT destinations from chat requests.

For gRPC, define concrete request/result and required server-streaming messages with the same IDs, limits, auth, and retry contract. JSON-RPC maps named methods onto the operation allowlist and defines notification behaviour: an authenticated notification can be send-only and receives no result. RPC request IDs are not necessarily durable send IDs; include the latter explicitly.

For MCP, expose only implemented tools, with per-caller principal binding and session isolation. Use a pinned official Go SDK HTTP handler and the protocol version it actually supports; verify lifecycle, initialization, cancellation, authentication, and notification behaviour. The inspected AIRC SDK is v1.6.1, and the [2025-11-25 Streamable HTTP specification](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports) is a versioned reference, not a claim to be the latest specification. Later MCP versions may change GET/stream behaviour; do not copy ordinary gateway SSE assumptions into MCP or silently upgrade the existing AIRC project. A cancelled MCP call after durable send admission does not erase the job.

### Framed streams and local IPC

For a new generic binary-stream adapter, define a versioned frame containing a 4-byte unsigned big-endian payload length followed by one UTF-8 JSON envelope, maximum 16 KiB. Bound frame assembly time and idle time. Reject zero/oversized/incomplete lengths and close cleanly on partial frames. TLS authentication or a per-frame signed envelope establishes identity before dispatch. A connection may carry multiple correlated requests, subject to bounded concurrency and response serialization.

Reuse that framing over TCP/TLS, Unix streams, socket pairs, inherited descriptors, VM sockets, RFCOMM, and serial-like bridges where suitable. Do not claim these carriers supply authentication by themselves. Plain network streams require an authenticated protected message profile or a configured secure wrapper; do not send bearer credentials over exposed plaintext TCP.

Use bounded newline-delimited JSON for a documented stdio adapter if easier for CLI callers, with control newlines escaped inside JSON. Keep diagnostic logs off stdout. Child ownership/inherited file descriptors can bind a configured local principal; where that cannot be verified, require a signed envelope. Do not treat any process able to write a shared FIFO as the intended agent.

Unix sockets use owner-controlled directories, restrictive modes, and peer credentials where the platform supports them. Avoid symlink races and deleting a socket owned by another process. Named pipes require tested platform permissions: Windows uses explicit security descriptors and a verified client identity; POSIX FIFO layouts avoid shared-response interleaving and may deliberately be ingress-only. Bound kernel/message-queue buffers and shared-memory ring lengths, validate every offset/length before use, and require authenticated producer identity plus a crash-recovery protocol.

### Datagram, QUIC, WebRTC, and constrained internet protocols

The minimal UDP adapter accepts one complete authenticated operation per datagram. For public/untrusted transit, use the encrypted signed-envelope profile; provision keys out of band. No interactive reply is required. Enforce the 1200-byte starting wire cap including security overhead, discard truncated/oversized packets, and implement no fragmentation initially. Use stable IDs on sender repetition. Do not trust source IP/port, send authentication errors to unverified return addresses, or create an amplification service.

Follow loss, size, and congestion guidance in [RFC 8085](https://www.rfc-editor.org/rfc/rfc8085.html). A sender's successful `sendto` is not gateway intake. Multicast/broadcast need explicit interfaces/group configuration, scoped TTL/hop limit, no reply storm, and sender-specific trust. Shared group membership alone does not distinguish authors.

DTLS adds secure datagram sessions and requires a handshake; that is distinct from a physically one-way link. QUIC streams reuse the framed semantic contract; QUIC datagrams remain unreliable despite authenticated encrypted connection setup. Do not advertise their delivery as stream reliability. See [DTLS 1.3](https://www.rfc-editor.org/rfc/rfc9147.html), [QUIC](https://www.rfc-editor.org/rfc/rfc9000.html), and [QUIC datagrams](https://www.rfc-editor.org/rfc/rfc9221.html).

WebTransport needs a real HTTP/3/WebTransport session implementation, certificate/origin checks, and separate stream/datagram conformance tests. WebRTC needs configured signaling, peer authentication, ICE/STUN/TURN deployment where needed, and bounded data-channel messages; ICE reachability is not agent authorization. Pin and test the chosen versions rather than assuming all browsers support the same path. See the [W3C WebTransport specification](https://www.w3.org/TR/webtransport/) and [WebRTC data channels](https://www.rfc-editor.org/rfc/rfc8831.html).

CoAP exposes a configured submission resource; map confirmable/non-confirmable request semantics honestly. OSCORE or authenticated DTLS supplies the defined security profile. Non-confirmable POST with OSCORE supports notification ingestion, but non-confirmable mode does not make the underlying CoAP transport one-way. Bidirectional CoAP must also expose supported query/result and live-update operations. Document any concrete one-way constraint separately; OSCORE provisioning and replay windows are still required. A CoAP transport ACK is not an AIRC acceptance receipt. Use bounded standards-based blockwise transfer only if deliberately implemented and tested; do not assume one constrained packet fits a 4096-byte chat body. See [CoAP](https://www.rfc-editor.org/rfc/rfc7252.html) and [OSCORE](https://www.rfc-editor.org/rfc/rfc8613.html).

## 9 Broker, mailbox, and service bridge recipes

All importers pass through the same auth, authorization, journal, and retry path. Native delivery acknowledgement means the gateway owns the accepted job, unless a separately documented stronger condition is explicitly requested.

### Brokers and event buses

Implement each concrete broker API rather than a universal consumer abstraction hiding its semantics. A publisher needs a stable operation ID in the payload; delivery tags, packet IDs, partition offsets, and reconnect session IDs have different scopes. Map them only where that scope is documented and persisted.

- MQTT/MQTT-SN: use authenticated, ACL-restricted topics and either signed publisher envelopes or a trusted per-principal topic mapping enforced by the broker. A subscriber's broker login does not prove the original publisher's identity. Define retained-message handling and QoS acknowledgement timing; select a library that permits the required durable handoff before ACK. If it auto-ACKs earlier, report the weaker reliability and choose another mode/library for durable intake.
- AMQP/STOMP: manual delivery acknowledgement after journal commit; redelivery maps to the same logical ID. Bound prefetch and reject/dead-letter invalid messages without repeated poison-message loops. Receipt headers and broker ACKs are not AIRC receipts.
- NATS/Redis/Kafka/DDS: declare the exact product and mode. Core ephemeral Pub/Sub can lose messages before intake. Durable stream/log modes require tested consumer state and ACK/offset management. For partitions, commit only a contiguous durably ingested prefix; concurrent completion must not skip earlier deliveries. DDS identity/security and QoS depend on the selected implementation.
- Managed queues/event buses: verify service signatures or consume through authenticated APIs, then establish the allowed publisher mapping separately. Test redelivery, visibility timeout, expired leases, dead-lettering, and stable IDs. Scope cloud credentials to the necessary queue/topic; never accept an arbitrary subscription URL from a chat request.
- ZeroMQ/NNG: select and document a supported pattern and secure transport/security mechanism; reconnect or a successful socket send does not imply durable delivery. Preserve application IDs and backpressure through the pattern.

Use [MQTT 5.0](https://docs.oasis-open.org/mqtt/mqtt/v5.0/os/mqtt-v5.0-os.html) and [AMQP 1.0](https://docs.oasis-open.org/amqp/core/v1.0/os/amqp-core-complete-v1.0-os.pdf) as protocol references; pin the distinct MQTT-SN/STOMP specifications when those adapters are implemented.

### Syslog and SNMP

Signed syslog needs verification of signature blocks and certificate/key trust before any covered log entry is posted. Buffer only within strict limits and a deadline; missing verification is not authorization. Support authenticated TLS syslog as a separate profile where the verified connection identifies the permitted sender. Plain unauthenticated syslog alone does not satisfy auth + send. Use [RFC 5848](https://www.rfc-editor.org/rfc/rfc5848.html).

For SNMPv3 traps, configure an authenticated security level, modern supported authentication algorithms, engine/user trust, timeliness/replay handling, and a fixed allowlist of notification OIDs and fields that can become chat text. Use encryption where required by the configured trust boundary. A community string or `noAuthNoPriv` does not qualify. Engine discovery/provisioning is distinct from the later one-way trap. Replayed notifications must not become fresh posts merely because the transport has no response. See [notification applications](https://www.rfc-editor.org/rfc/rfc3413.html) and [USM](https://www.rfc-editor.org/rfc/rfc3414.html); select currently supported algorithm profiles at implementation time.

### File, database, object, upload, and offline mailboxes

Files use per-principal owner-controlled inboxes or signed envelopes. Writers atomically rename a completed bounded file into the inbox. Open safely without following arbitrary symlinks; require a regular file, bound size, and reject paths outside the configured inbox. Journal before marking consumed; a crash between those actions must re-import to the same request. For append-only logs, persist file identity/generation and byte offsets, handle incomplete trailing records and rotation, and do not mistake truncation for a fresh authenticated operation.

SFTP/SCP/FTPS should terminate in a maintained upload service with isolated authenticated inboxes; the importer preserves that account-to-principal mapping and/or verifies signed content. Do not write a new SSH/FTP server inside the gateway. Upload completion means the file reached that service, not AIRC. Required results, history, and live-update batches use a separate configured readable outbox where downloads are supported.

Object storage uses a configured bucket/prefix and immutable object identity/version. Notifications are hints and can duplicate; fetch and verify the actual bounded object. Define listing/reconciliation to recover missed notifications. Database mailboxes use scoped database permissions, a stable primary key, and a transaction/lease or change-feed cursor with replay. A database notification alone may not carry durable content.

Offline/removable-media import does not execute files or trust media ownership as sender identity. Verify a signed bounded bundle, its principal, audience, target permission, validity, and individual operation IDs. For long-delay links, select an explicit longer validity/replay-retention profile; extending the window has storage and replay consequences. Bundle Protocol uses a maintained implementation and a documented security binding rather than a home-grown protocol. See [Bundle Protocol version 7](https://www.rfc-editor.org/rfc/rfc9171.html).

### Webhooks, chat networks, email, and SMS

Inbound webhooks verify the provider's actual signature specification over the exact received bytes, timestamp/replay rules, and authenticated event ID. Map a trusted provider event identity to the authorized principal; the source service account is not automatically the message author. If using HTTP Message Signatures, specify required covered components and content digest rather than merely checking that a signature header exists. See [RFC 9421](https://www.rfc-editor.org/rfc/rfc9421.html).

Outbound results, wherever supported, go only to preconfigured allowlisted destinations, with bounded retries separate from message submission. A callback failure never re-posts to AIRC. Apply DNS/IP destination checks and redirect policy to avoid turning the gateway into an arbitrary network-fetch service.

Chat bridges use authenticated stable account IDs and room IDs, not mutable display names. Keep an origin/event-ID ledger and direction rules to prevent bridge echo loops. Scope rooms and participants explicitly; do not bridge an entire remote account by default.

Email From headers and SMS originating numbers alone do not establish the required identity. Use signed application content or a trusted provider integration with a defined account/device mapping and enrollment process. Handle MIME/segmentation within a strict bound; attachments and commands are outside the baseline. Correlate repeats by verified operation identity, not body similarity. Required reply emails or SMS are separate configured outputs where supported.

## 10 Platform and device bridge boundary

Run privileged or platform-specific capture in a narrowly scoped helper when the main Go process cannot safely use the native API directly. The gateway itself runs without root/device-wide privileges.

Use an owner-controlled Unix socket, Windows named pipe, or verified TLS channel between sidecar and core. The bridge frame contains a version, bridge instance ID, stable native event ID, payload, verified source identity evidence, receive metadata, and the original signed envelope where available. Authentication of the bridge is mandatory. Configure which principals and targets that bridge may assert; it cannot supply arbitrary account credentials or become an unrestricted identity oracle.

Prefer end-to-end signed messages where the device can produce them. If a trusted device/network server authenticates instead, document exactly what it verified and how replay protection survives reboot. Device MAC address, BLE address, serial-port path, CAN identifier, NFC UID, or source IP alone is not an authenticated author.

Each hardware recipe must specify:

1. Supported OS/API/library, adapter hardware and firmware, permissions, and reproducible provisioning.
2. Framing/version, maximum transmitted and decoded size, integrity/authentication format, replay identity/window, and optional confidentiality.
3. Where fragmentation/reassembly occurs, if supported, with total bytes/fragments/time bounded and authenticated message verification before dispatch. Keep the baseline UDP adapter unfragmented; a hardware extension is a separately tested profile.
4. Actual behavior on loss, duplication, reordering, partial records, disconnect, device reset/counter rollback, and gateway restart.
5. Whether the link has setup traffic, return traffic, or truly only one direction. Send-only AIRC capability does not imply a handshake-free carrier.
6. Native hardware evidence: a real authenticated frame/device action becomes the correctly attributed AIRC message; a tampered/replayed frame is handled as specified.

Constrained links need constrained payloads. A compact JWS/JWE envelope will not fit every BLE advertisement, classic CAN frame, or low-rate radio packet. Use an established suitable security format/native authenticated mode (for example a reviewed COSE/OSCORE profile or verified network-server identity), or a bounded authenticated reassembly bridge. Provision fixed route aliases if full targets do not fit. Do not invent truncated signatures or assume that a CRC provides authentication. If the chosen hardware cannot fit any permitted protected message, that profile is unavailable until a concrete encoding/bridge is provided; the connection family remains feasible through other profiles.

BLE advertising uses a scanner and provisioned sender trust; extended advertising and encrypted advertising support vary by hardware and OS. ESP-NOW unicast peer protection must not be assumed for broadcast. LoRaWAN uses a trusted network/application-server integration and provisioned device identity, with frame-counter/reset behavior checked. NFC verifies the trusted signature and freshness of the message record; a static signed tag may authenticate its origin yet remain replayable. Relevant references are the [Bluetooth LE primer](https://www.bluetooth.com/bluetooth-le-primer/), [Espressif ESP-NOW documentation](https://docs.espressif.com/projects/esp-idf/en/v5.5/esp32/api-reference/network/esp_now.html), [LoRaWAN specifications](https://lora-alliance.org/lorawan-for-developers/), and [NFC Signature RTD](https://nfc-forum.org/build/specifications/signature-record-type-definition-technical-specification/).

For raw Ethernet/IP/ICMP, use explicit configured interfaces, protocol identifiers, destination scopes, and least-privilege capture/send helpers. They are paired application protocols or carriers, not native AIRC interfaces. Shared memory/RDMA needs explicit producer isolation, validated lengths, fences/ownership, and recovery; a writable memory region is not a trust boundary. Optical/acoustic/QR receivers treat decoded content as untrusted bytes until the common envelope verification succeeds. A data diode requires pre-provisioned trust and sender repetition; do not specify a TLS handshake across a physically absent return path.

## 11 Complete catalogue-to-implementation map

This is the complete mapping of the accompanying HTML page's **72 entries**, without preference ranking. It is not a claim that these are 72 independent protocols, nor that a finite catalogue can enumerate every future or proprietary way to move bytes. Any additional carrier can join through the same authenticated-send contract once it has a concrete framing, trust, and ingestion implementation.

Every actual protocol implementation needs conformance for every supported AIRC operation plus the native proof below. The status ledger must list each protocol member separately, with send, reply, receipt/status, history, live updates, search, directory/presence, and other supported AIRC features assessed. Missing implementation is pending work, not a transport limitation. Request/response mailboxes and configured broker result topics count as return paths. One-way exclusions require evidence of an actual connection constraint. A row containing several protocols must report each one's status separately. Read-only companions and underlying carriers are labelled as such; they do not inflate the implemented connector count.

| # | Catalogue entry | Implementation path and distinguishing acceptance evidence |
|---|---|---|
| 01 | TCP / IPv4 / IPv6 | Framed stream adapter; test IPv4 and IPv6 listener/client paths, partial frames, deadlines, and protected identity. |
| 02 | TLS over TCP | Same stream contract over verified TLS; test certificate name/trust failure and credential binding. |
| 03 | UDP unicast | One protected envelope per bounded datagram; test real packet loss/repetition, authenticated query/result routing, live updates, and duplicate intake. Test physically one-way mode separately. |
| 04 | DTLS over UDP | Pinned DTLS implementation; test handshake identity, replay, fragmentation limits, and reconnect independently of plain UDP. |
| 05 | QUIC streams | QUIC adapter carrying framed operations; test per-stream limits, cancellation, and connection identity. |
| 06 | QUIC datagrams | Negotiated datagram extension with envelope IDs; drop/reorder datagrams and prove honest unreliable delivery semantics. |
| 07 | SCTP | Platform-supported SCTP adapter or sidecar; preserve message boundaries, authenticate above transport, test association loss. |
| 08 | DCCP | OS-specific datagram sidecar; retain congestion behavior, authenticate payloads, prove operation on a supporting kernel. |
| 09 | UDP-Lite | OS-specific adapter; cryptographic verification must cover the complete operation despite partial transport checksum coverage. |
| 10 | IP multicast / IPv4 broadcast | Datagram importer with explicit interfaces/groups and sender trust; test multi-receiver repetition and absence of reply storms. |
| 11 | Raw IP / custom IP protocol | Privileged paired-protocol sidecar, explicit protocol/interface configuration, protected messages, and real raw-packet evidence. |
| 12 | ICMP / ICMPv6 encapsulation | Paired encapsulation helper; bound payload/rate and verify authors independently of ICMP identifiers. |
| 13 | HTTP/1.1 / HTTP/2 / HTTPS | Shared HTTP API; test negotiated versions, auth, body limits, durable `202`, and independent AIRC observation. |
| 14 | HTTP/3 | HTTP semantic handlers over a tested HTTP/3 server; validate actual QUIC negotiation and fallback reporting. |
| 15 | HTTP GET / polling | Read-only companion for status/history. Pair with a send path to meet baseline; tests prove GET never creates a post. |
| 16 | HTTP long polling | Required bounded read companion; test timeout, cancellation, resume position, and auth changes. |
| 17 | HTTP POST / PUT / PATCH / DELETE | Implement POST; PUT uses stable resource identity; PATCH/DELETE remain pending until explicit resource semantics and evidence exist. PATCH/DELETE require explicit resource semantics, not arbitrary chat mutation. |
| 18 | Server-Sent Events (SSE) | Required downstream stream paired with send ingress; test resume/gaps and a browser-compatible auth mechanism. |
| 19 | WebSocket / WSS | Bounded correlated duplex messages with authenticated session; test origins, slow readers, disconnect, and repeat IDs. |
| 20 | WebTransport | HTTP/3 session adapter; separately validate stream and datagram modes on claimed clients/browsers. |
| 21 | WebRTC data channels | Authenticated signaling/peer binding plus data-channel adapter; test configured ICE/TURN topology and loss modes. |
| 22 | MCP over Streamable HTTP | Pinned MCP SDK and protocol version; prove initialization, tool auth, caller isolation, cancellation, and send outcome. |
| 23 | gRPC | Versioned service schema with auth interceptors and limits; test real generated client, retries, and stream cancellation. |
| 24 | JSON-RPC / other RPC protocols | Explicit method-to-operation mapping; distinguish RPC correlation from durable send ID and verify notification mode. Additional RPCs need concrete bindings. |
| 25 | CoAP / secure CoAP | Configured CoAP resource with DTLS/OSCORE security profile; test confirmable acknowledgements versus AIRC receipts. |
| 26 | CoAP non-confirmable POST + OSCORE | Protected non-confirmable submission plus supported bidirectional query/results; verify OSCORE replay handling and repeated logical operation identity. Record any concrete one-way limitation. |
| 27 | Inbound / outbound webhooks | Provider-specific verification and fixed principal/route mappings; independently test intake and result-callback retry. |
| 28 | Unix stream sockets | Framed stream with directory permissions and supported peer credentials; test unauthorized local process and socket ownership. |
| 29 | Unix datagram sockets | Bounded packet importer with verified local identity or signed envelope; test truncation and sender lifecycle. |
| 30 | Unix sequenced-packet sockets | OS-supported packet-preserving stream adapter; prove actual socket type and limits on each claimed platform. |
| 31 | stdin/stdout / anonymous pipes | Bounded NDJSON or declared framing with trusted process binding/signed messages; test EOF, partial records, and clean stdout. |
| 32 | Named pipes / FIFOs | Restricted per-principal ingress and isolated response path wherever supported; test interleaving, permissions, and writer disconnect. |
| 33 | Windows named pipes | Native pipe security descriptors and verified caller identity; real Windows client test, including unauthorized SID. |
| 34 | Socket pairs / inherited file descriptors | Explicit supervisor-owned descriptor and principal mapping; test ownership, lifecycle, and no accidental descriptor leaks. |
| 35 | Shared memory / memory-mapped queues | Native bounded ring/queue bridge with producer isolation and validated offsets; test crash, wraparound, and corrupt records. |
| 36 | OS message queues | Separate implementations for selected POSIX/System V/Windows APIs; verify permissions, native size limits, and restart behavior. |
| 37 | Mach / XPC / Windows RPC / COM / D-Bus / Android Binder | Per-platform native bridge and authenticated caller binding; real OS acceptance for each claimed API, no universal stub. |
| 38 | VM sockets / virtio-vsock / Hyper-V sockets | Guest/host channel adapter with explicit guest identity plus app auth where needed; test on the relevant VM runtime. |
| 39 | MQTT / MQTT-SN | Separate broker/device protocols feeding durable intake; prove publisher attribution and native ACK/QoS behavior. |
| 40 | AMQP / STOMP | Separate configured consumers with post-commit ACK; test redelivery, poison messages, and bounded prefetch. |
| 41 | Syslog / signed syslog | Signed blocks or verified TLS sender profile; test delayed/missing signature verification before posting. |
| 42 | SNMPv3 authenticated traps | Verified engine/user/timeliness plus allowed OID translation; test valid trap, replay, bad auth, and no response requirement. |
| 43 | NATS / Redis Pub/Sub / Redis Streams / Kafka / DDS | Implement named products/modes separately; demonstrate each one's identity, durable or lossy delivery, and cursor/ACK behavior. |
| 44 | ZeroMQ / NNG | Specific secure messaging pattern feeding the core; test disconnect/backpressure and application-level ID persistence. |
| 45 | Managed queues / cloud event buses | Provider-specific authenticated consumer; test duplicate delivery, visibility lease, principal mapping, and dead letters. |
| 46 | IRC / XMPP / Matrix / other chat bridges | Network-specific authenticated identity/room mapping with origin ledger; prove no echo loop and correct AIRC author. |
| 47 | Files / watched directories / append-only logs | Safe bounded importer with atomic completion/offset rules; test symlinks, rotation, partial writes, and crash re-import. |
| 48 | SFTP / SCP / FTPS upload inbox | Maintained authenticated upload endpoint plus isolated importer; prove uploader attribution and upload-versus-post status. |
| 49 | Object storage | Versioned object importer plus notification/list reconciliation; test duplicate/missed notifications and untrusted metadata. |
| 50 | Database mailboxes / change feeds / notifications | Product-specific durable rows and ingestion cursor/lease; test transaction failure, reconnect, and duplicate notifications. |
| 51 | Email / SMS | Verified payload or trusted enrolled provider mapping; test spoofed sender fields, segmentation, repeats, and no implicit reply. |
| 52 | Delay-tolerant networking / Bundle Protocol | Maintained BP implementation plus authenticated identity binding; test expiry and delayed/duplicate bundle arrival. |
| 53 | Offline exchange / removable media | Signed bounded bundle importer; test replay after reboot, invalid audience, expiration, and untrusted file paths. |
| 54 | Raw Ethernet / link-layer frames | Privileged interface-specific sidecar with configured frame format; test actual frames and authenticated source, not MAC alone. |
| 55 | Bluetooth RFCOMM / L2CAP / BLE GATT | Separate supported native profiles with app identity; real device tests for framing, pairing assumptions, MTU, and disconnect. |
| 56 | BLE advertising / extended advertising | Scanner with provisioned authenticated payload profile; prove actual payload fit, replay/reset handling, and supported OS/hardware. |
| 57 | ESP-NOW | Provisioned radio sidecar; separately test unicast protection and signed/authenticated broadcast handling on devices. |
| 58 | LoRaWAN unconfirmed uplinks | Trusted network/application-server ingestion; verify device identity, counter reset/replay, payload limits, and no downlink dependence. |
| 59 | UART / RS-232 / RS-485 / virtual serial / modem | Framed authenticated serial bridge with bounded reassembly; test partial bytes, resets, contention, and real device rates. |
| 60 | USB serial / bulk / interrupt transfers | Native device profile and permissions; test enumeration, transfer sizes, disconnect, and sender auth independently of device path. |
| 61 | CAN / CAN-FD / SPI / I²C | Per-bus hardware bridge and protected message framing; test actual size limits, fragment loss if used, and spoofed bus identifiers. |
| 62 | NFC / NDEF signed message records | Reader and trusted-signature/freshness verifier; test valid message, copied/replayed tag, and untrusted signing key. |
| 63 | IEEE 802.15.4 / Zigbee / LoRa / radio mesh | Specific stack/gateway integration with provisioned device binding; prove payload/security/duty-cycle constraints on actual hardware. |
| 64 | InfiniBand / RDMA / specialized interconnects | Native isolated producer/queue bridge; test memory access boundaries, completed operation framing, and recovery on supported hardware. |
| 65 | Optical / QR sequences / acoustic channels | Bounded decoder producing authenticated envelopes; test noisy/partial/repeated media and an actual light/image/audio transfer. |
| 66 | Unidirectional link / data-diode ingress | Existing framed carrier plus pre-provisioned message auth; physically remove return path and prove ingestion/retry still works. |
| 67 | Ethernet / Wi-Fi / cellular / satellite with IP | Carrier recipe using an existing IP adapter; test intended reachable path, MTU, reconnect, and configured delay/expiry. No new plugin required. |
| 68 | SSH forwarding / reverse tunnels | Maintained SSH tunnel carrying the chosen stream/HTTP adapter; verify host keys, bounded destination, and reconnect. No new AIRC protocol. |
| 69 | SOCKS / HTTP CONNECT | Configured proxy path for a compatible adapter; test actual proxy negotiation, destination restrictions, and end-to-end auth. |
| 70 | VPNs / private mesh overlays | Deployment recipe over an existing adapter; verify network reachability and preserve application identity/permissions. |
| 71 | Tor | Configured SOCKS/onion TCP path for a compatible adapter; test endpoint/auth and timing. Do not imply native UDP support. |
| 72 | DNS / ICMP custom tunnels | Paired, explicitly deployed carrier implementation feeding an existing adapter; prove capacity, loss behavior, and message auth. Distinguish from raw ICMP ingestion. |

For OS-specific sockets and IPC, implementation-time references include the [Linux socket interface](https://man7.org/linux/man-pages/man2/socket.2.html), [Unix sockets](https://man7.org/linux/man-pages/man7/unix.7.html), [VM sockets](https://man7.org/linux/man-pages/man7/vsock.7.html), and [Microsoft IPC documentation](https://learn.microsoft.com/en-us/windows/win32/ipc/interprocess-communications). Check the actual target OS rather than extrapolating Linux support to macOS or Windows.

## 12 Verification and acceptance evidence

Tests must cross the boundary whose behavior is claimed. A fake backend is useful for unit tests but does not demonstrate a protocol, account mapping, receipt, or recovery path. Use the isolated real AIRC fixture and a separately connected observer for integrated acceptance. Do not post into the user's active rooms.

### Required failure cases

| Scenario | Required result |
|---|---|
| AIRC unavailable before first attempt, then recovers | Admitted job remains durable and retries; observer eventually sees the post within budget. |
| Packet lost before gateway intake | Gateway has no job; sender-side repetition can deliver it. No false queued/accepted claim. |
| Gateway commits intake, caller loses response | Same-ID ingress retrieves/coalesces the original job; no extra job is created. |
| Two adapters submit same operation concurrently | Same principal/binding/ID/body produces one job. Different body produces a conflict. |
| Session caller omits default policy/expiry on repeated ingress | Stored defaults are reused; no artificial fingerprint conflict or extended lifetime. |
| Process dies before intake commit | No admitted responsibility is claimed. Sender/broker may redeliver. |
| Process dies after commit but before send | Restart submits the recoverable job. |
| Partial upstream write or receipt lost | Default policy attempts lookup and then resends the same ID if unresolved. Outcome remains uncertain until evidence resolves it. |
| Acceptance is retained by AIRC, gateway did not save receipt | Recovery gets the original message ID; observer sees only the original post. |
| Acceptance was evicted from AIRC, gateway did not save receipt | Default policy can create another post. Verify the second post and duplicate-risk reporting; **this is an expected allowed outcome**, not a test failure. |
| Same uncertain case under `receipt_only` | No automatic repost; unresolved state is visible. |
| Repeated unknown/failed receipt lookups, including across restarts | Persist and enforce the independent lookup budget, backoff, and expiry. `receipt_only` stops probing when exhausted; `retry` may still use remaining send attempts. |
| Status requests after lookup exhaustion | Read the journal without resetting counters or restarting recovery; an explicit local operator lookup is bounded and cannot send. |
| Registration stalls, a write blocks, or receipts are withheld while other traffic continues | Absolute phase/overall deadlines terminate the attempt, close the lease, release the worker, and preserve uncertainty without process restart. |
| Gateway saved acceptance, response/callback fails | Result delivery may retry; no AIRC repost. |
| Receipt observed, database result commit fails | No further send while acceptance remains known in memory; saving the receipt retries. Crash recovery reflects the durable evidence boundary. |
| AIRC reports accepted but not persisted | Report both fields accurately; do not resend simply to obtain persistence. |
| Later attempt is denied after an earlier ambiguous write | Stop that prohibited retry; preserve possible earlier acceptance instead of claiming definitely not posted. |
| Invalid auth, unauthorized target, invalid body, ID conflict | Reject before upstream send. No retry loop for permanent bad input. |
| Credential rotation or principal/binding revocation during outage | Stable principal/ID survive rotation; revocation prevents new attempt grants and preserves job state. |
| Revocation before grant, after grant before write, or during write | No grant after revocation may send. A grant committed earlier may finish only its original bounded attempt; no retry/reconnect can reuse it. Record any resulting acceptance. |
| Process restarts with a pre-revocation dispatch marker | Preserve uncertainty but require a fresh grant; the old marker cannot authorize another write after revocation. |
| Account/binding config changes with pending jobs | Existing jobs retain their original binding or become explicitly blocked; no silent author/server reassignment. |
| Expiry or send-attempt budget exhausted | Stop new sends and show expired/unresolved, including possible prior acceptance. Remaining receipt probes may run only within their own budget and before expiry; an already observed acceptance can still be recorded. |
| Disk full, failed journal commit, full quota | Backpressure/drop according to native protocol; no success ACK and no unsafe post-before-journal. |
| Broker redelivery or failed broker ACK | Coalesce stable operation ID and retain gateway retry responsibility exactly as documented. |
| Out-of-order completion of broker partition items | Offset commit does not skip an unjournaled predecessor. |
| Slow/abandoned read stream | Bounded memory, explicit disconnect/gap, prompt session release, other callers continue. |
| History cursor evicted or watch outage exceeds retention | Explicit gap/expired result, no silent empty-success claim. |
| Observe acknowledgements are delayed, missing, or rejected while events arrive | Buffer from setup start; begin catch-up only after every requested `765`. Failed setup closes the connection without exposing a partial watch. |
| Oversized/malformed frames or expensive-auth flood | Bounded parsing, verification work and memory; no privilege or operation bypass. |
| Bridge loops, forged identity, arbitrary callback address | No echo loop, no unpermitted principal assertion, no arbitrary outbound destination. |
| Graceful stop and forced process termination | Owned listeners/sessions close; durable jobs recover; live AIRC outside the fixture remains untouched. |

Where possible, implement fault injection at observable network or process boundaries: a controllable proxy can drop a receipt after the fixture accepts, and a small-retention fixture can evict that ID before retry. Inspect both the journal and the independent observer's messages. A mocked `SendWithID` returning an error cannot prove the accepted-but-unknown case.

Add focused fuzz/property tests for untrusted frame lengths, envelope parsing, duplicate JSON keys, normalization/fingerprint stability, malformed cursors, and device reassembly where implemented. Cryptographic verification uses library vectors plus application binding tests; do not attempt to validate a custom cryptographic design with round trips alone.

### Resource and lifecycle evidence

Measure a bounded load at the configured quotas, a burst above them, and recovery after it. Record admitted/rejected requests, maximum queue bytes, WAL growth, live sessions, goroutines, file descriptors, and shutdown behavior. Assert configured count/byte ceilings where deterministic. Test that another principal makes progress during a flood and that sends retain capacity while watches are full.

Do not invent a throughput SLA from the 8-worker default. Report hardware, adapter mix, payload sizes, observed throughput/latency, and any saturation boundary. Exponential backoff and eventual retries do not guarantee global message order: a later operation may post before an earlier uncertain one. If a connector promises per-principal/target FIFO, implement that explicit ordering policy and test the head-of-line behavior; it is not a baseline guarantee.

### Required project checks during implementation

Use the project toolchain and run formatting checks, `go vet ./...`, `go test ./...`, and `go test -race ./...` on a supported host, plus the real integration suite for implemented adapters. Run relevant native-platform tests for platform bridges; report unavailable race/native tools accurately. Build claimed distributions on their target platforms or cross-compile with the limitation recorded. Security, persistence, and concurrency changes require these broader checks even when the code diff is small.

Do not run the active AIRC checkout's full suite or alter it as part of this plan. The pinned fixture snapshot has its own validation and outputs. If upstream incompatibility is found, isolate and report the exact failing contract; do not patch the user's live project without a separately authorized task.

### Clean first-use acceptance

For every claimed platform and at least one client in each implemented ingress family:

1. Start from a clean temporary environment with no access to the user's existing AIRC or gateway state.
2. Follow only the published setup instructions to generate/provision test credentials and start the fixture plus gateway.
3. Discover or read the connector's declared capabilities and its limits.
4. Authenticate and send a message using the real client, tool, or device. A separate fixture observer confirms author, target, content, and receipt ID where available.
5. Disconnect AIRC, submit another operation, restart the gateway if relevant, then restore AIRC and verify the promised bounded retry behavior.
6. Exercise one unsupported operation and one invalid credential. Confirm that neither becomes an upstream post.
7. Remove only fixture-owned resources and verify no surviving child process or listener. Record the commands and outputs, with secrets removed.

## 13 Operator model and rollout

The gateway adds a durable outbox and credential boundary. Keep the operator surface local and narrow: validate config, inspect capabilities and queue summaries, view one request's state, rotate/revoke credentials, back up state, and stop/start the service. No agent-facing endpoint may grant itself a principal, register an upstream account, enable a new listener, or run a shell command.

Keep three readiness facts separate: the gateway process is alive, durable intake is available, and the AIRC backend is reachable/compatible. An offline upstream can coexist with healthy durable intake until limits or expiry make admission inappropriate. An incompatible upstream configuration must be conspicuous. Optional adapter failure need not stop unrelated configured adapters, but startup must not silently claim a requested listener is active when it failed to bind.

Deployment proceeds only after the fixture acceptance and a concrete staging configuration exist. Adding the gateway normally needs no AIRC daemon restart. Any production account provisioning, network exposure, installation, or integration with the user's live daemon must be explicitly authorized in the future implementation/deployment task. The plan itself does not authorize those changes.

Rollback consists of stopping gateway intake, preserving its database/keys/config, disabling the new listeners, and either draining admitted jobs under the compatible version or leaving them recoverable. Never restore a pre-send journal snapshot and blindly resend everything: the external AIRC side effects may already exist. Run schema-compatible binaries only, and handle older backups as potentially uncertain delivery records. Disabling the gateway leaves existing native AIRC clients unaffected unless an explicit deployment chose to change their paths.

## 14 Handoff checklist and execution brief

The implementing agent should maintain one compact status document under `docs/gateway/` in this worktree with:

- The pinned AIRC revision, gateway revision, dependency versions, and fixture identity.
- Completed steps with real acceptance evidence and exact commands.
- A per-protocol capability/status matrix: working, disabled, platform unavailable, hardware validation pending, or proposed.
- Known limits: packet/body sizes, identity/trust assumptions, expiry/retention windows, replay scope, optional return paths, and allowed duplicate behavior.
- Any exact upstream API blocker and the smallest separately proposed AIRC change, if one is actually needed.
- Publication state: local commit, no push, no installation, and whether live integration was separately authorized/performed.

Use this brief when starting the later implementation:

> Implement the standalone AIRC gateway described in `docs/gateway/IMPLEMENTATION_PLAN.md`, working in `/Users/sws/Code/airc-gateway` on `codex/connection-gateway`. Use the existing Go module and keep gateway code under `cmd/airc-gateway` and `internal/gateway`. Preserve the primary checkout `/Users/sws/Code/airc` and its active daemon, accounts, state, and other work. Record the baseline revision and use disposable fixtures. Implement all AIRC functionality each connection type can support, including history, receipt/status lookup, replies, and live updates wherever technically supported. Authenticated send alone suffices only with concrete connection limitations; adapter omissions are not limitations. Ordinary sends default to bounded automatic retry under uncertainty, using stable IDs and allowing duplicates after retention loss. Follow the shared contract and dependency steps, then the unranked adapter matrix for the requested implementation scope. Prove each claimed transport through its real client or device and an independent AIRC observer. Keep hardware/platform blockers visible. Do not push, publish, install, or connect to live AIRC without authorization for that action.

The 72-row catalogue is the requested breadth; the common authenticated-send boundary is what makes it extensible. No implementation or live deployment was performed while preparing this plan.
