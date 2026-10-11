# MCP over Streamable HTTP

The `mcp_http` listener serves one `/mcp` endpoint with six tools: `send`,
`read`, `wait`, `status`, `identify` and `register`. Each takes an optional
`author` (`{nick, token}`) and `password` (the listener's access password).
A nickname alone is an AIRC guest; with a token it is that registered account.
`identify` makes its author the session's default until the session ends,
as IRC `NICK` and SASL do for a connection; see
[contract](contract-v1.md#authors). Catalogue completion is not claimed.

It uses the pinned Go MCP SDK and the gateway dispatcher, journal, workers and
AIRC backend. It does not invoke `airc mcp` or reuse the native CLI's
fixed-identity subprocess tools. The pinned SDK is v1.6.1. Its default
negotiated protocol version is `2025-11-25`; it also supports `2025-06-18`,
`2025-03-26` and `2024-11-05`. This records the versioned implementation
rather than a latest-version claim.

Read-only configuration checks, without starting runtime or contacting AIRC:

```sh
go run ./cmd/airc-gateway config validate --file examples/gateway/mcp-http.disabled.json
go run ./cmd/airc-gateway capabilities --file examples/gateway/mcp-http.disabled.json
```

The example is disabled. Room history, search and watch need the gateway
cursor key. Listener allowlists use gateway operation names (`send`,
`history.read`, `watch`, `receipt.get`); MCP tool names do not replace them.
TLS is optional, as on the other HTTP listeners; account tokens sent without
it can be observed on the network. Session IDs are private transport handles,
not authentication: a session belongs to the client address that created it.

| Tool | Gateway operation | Arguments | Result |
| --- | --- | --- | --- |
| `send` | `send` | Stable `request_id`, `target`, `message`; optional `issued_at`, `expires_at`, `delivery_policy` | Durable admission and delivery facts |
| `read` | `history.read` | `request_id`, room `target`, `limit`; optional `parent_id`, `thread_mode`, `cursor`, `start` | Complete bounded history page and opaque cursor |
| `wait` | `watch` | Same selectors as `read` | Bounded message page, or progress pages when the MCP progress token is present |
| `status` with `lookup_id` | `receipt.get` | `request_id`, `lookup_id` | The author's durable receipt (a registered author's needs its token) |
| `status` without `lookup_id` | `capabilities.get` | `request_id` | This listener's access policy and limits |
| `identify` | `account.identify` | `request_id` | Checks the author with AIRC and makes it the session default |
| `register` | `account.register` | `request_id` | Registers the author's nickname with its own token |

Tool names select the operation. Arguments omit `version` and `operation`; the
adapter supplies gateway version one internally. Every tool keeps a validated
`request_id`; a send ID remains the durable retry identity. Fixed argument
structs use the strict JSON boundary so duplicate, unknown, aliased, null and
oversized fields fail before dispatch. The schemas expose only the listed
fields, with the usual target, selector, time, size and ID constraints. Calls
check the listener's policy each time; AIRC decides what the author may do.

Results preserve admission uncertainty, bounded retries, progress budgets,
watch renewal, opaque cursor scope, TLS/origin/session boundaries and joined
shutdown. A cursor created for `watch` still belongs to `watch`, even though its public MCP tool is called `wait`. SDK JSON-RPC IDs
remain transport correlation IDs.

SDK SSE event replay is separate from gateway watch renewal. This increment
does not configure an event store: `Last-Event-ID` replay is rejected rather
than presenting an incomplete cache as complete history. Reconnect and renew
using the gateway cursor. No native event is declared delivered merely because
the SDK wrote a notification.

Allowed browser origins are configured explicitly, without wildcards or cookie
credentials. Preflight permits only the MCP endpoint, required methods and
protocol headers. Origin permission never supplies an author.

Logical MCP sessions are limited globally and per client address; HTTP requests also consume the shared request slots. HTTP bodies are
limited to 32 KiB and gateway arguments to 16 KiB. Encoded JSON-RPC IDs, method
names, tool names and outer progress tokens are limited to 256 bytes; watch
progress tokens additionally require a nonempty string of at most 64 bytes or
a nonnegative integral number. Tool calls expire after 20 seconds, HTTP requests
after 25 seconds and idle SDK sessions after 30 seconds. Whole tool results and
progress parameters reserve 1024 bytes within the 64 KiB frame ceiling; progress
streams have a 1 MiB and 128-frame ceiling. Oversized values fail rather than
being truncated. Shutdown stops admission, closes SDK sessions, joins tool
handlers and native leases, then closes the journal. Native DM read identity,
remaining native reads remain shared gateway work. Nonmessage live events use
[events.watch](live-events.md) through other carriers; MCP `wait` is message-only.
Catalogue row 31 uses the distinct framed stdio adapter, documented in
[stdio.md](stdio.md); this increment does not add an MCP stdio profile.

## Verification

Dated records follow. Where they mention principals, bindings, signed or
encrypted envelopes, signing keys or return routes, they describe the
[removed identity model](removed-identity.md), which no longer exists.

The repository SDK callers migrate to these four tools. Unsupported core
operations fail in the test fixture rather than entering a generic operation
field. Superseded catalogue, membership and live-signal MCP tests are replaced
with explicit rejection of all removed tool names. Other carriers and shared
backend operation coverage remain.

Public SDK tests cover strict raw arguments and narrow schema discovery,
principal-scoped tool and target discovery, denied receipt permission,
same-session discovery rejection after durable revocation, quota release,
principal session isolation and joined shutdown. Real SDK/native acceptance
covers observed author/target/body and accepted receipts, bounded history pages
and cursor boundary, HTTP/1.1/2, message progress, cancellation, lease renewal,
cursor resumption, reconnect replay without duplicate native submission, and
SIGINT shutdown with an active watch.

Before source: `d543449`. Complete adapter normal/race checks passed in
6.061s/9.994s. Shared core/auth/dispatch/store race checks passed in
1.374s/4.108s/1.535s/32.496s. Real SDK/native integration (all `TestMCPHTTP`
checks plus retained gRPC AGENTS coverage) passed normal/race in
27.201s/39.399s. Added SDK oversized-send/direct-read failure checks passed race
in 1.781s. Checks overlapped; these are validation durations, not speed claims.
Module build, vet, staticcheck, formatting and diff checks passed. The disabled
example passed actual CLI validation and configuration capability discovery
without starting runtime or listeners.

Initial attempts retained in the raw evidence include denied sandbox sockets,
removed imports still needed by session tests, a fixture with no configured
operations, a missing room-only decoder check caught by the complete adapter
race suite, and the obsolete snapshot helper caught by staticcheck. All were
corrected and their affected checks rerun. The earlier narrow `^TestMCP`
selection omitted `TestDecodeMCPRequestUsesStrictToolJSON`; complete adapter
checks cover it. Raw evidence: `/tmp/airc-roadmap-baseline/mcp-four-*`.
Earlier all-operation MCP acceptance applies to the superseded interface and
does not establish final acceptance for this replacement. Full final module
acceptance remains on the roadmap. Catalogue completion is not claimed.
