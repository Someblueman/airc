# MCP over Streamable HTTP

This adapter exposes four narrow messaging and status tools.
Catalogue completion is not claimed.

The gateway listener protocol is `mcp_http`, with a single `/mcp` endpoint.
It uses the existing pinned Go MCP SDK and the gateway dispatcher, journal,
workers and AIRC backend. It does not invoke `airc mcp` or reuse the native
CLI's fixed-identity subprocess tools.

The pinned SDK is v1.6.1. Its default negotiated protocol version is
`2025-11-25`; it also supports `2025-06-18`, `2025-03-26` and `2024-11-05`.
This records the versioned implementation rather than a latest-version claim.

Read-only configuration checks, without starting runtime or contacting AIRC:

```sh
go run ./cmd/airc-gateway config validate --file examples/gateway/mcp-http.disabled.json
go run ./cmd/airc-gateway capabilities --file examples/gateway/mcp-http.disabled.json
```

The example is disabled and has no credentials or upstream bindings. Enabling
a useful listener requires explicit provisioned bindings, principals and
operation/target permissions; room history/search/watch additionally requires
the dedicated gateway cursor key. Configuration allowlists retain internal
gateway operation names (`send`, `history.read`, `watch`, `receipt.get`); public
MCP tool names do not replace those authorization identifiers.

Network configuration follows the other gateway HTTP listeners: cleartext is
limited to loopback; remote listeners require verified TLS. Provision a gateway
bearer credential for each principal, separately from its upstream account
credential. The client sends `Authorization: Bearer TOKEN` on every POST, GET
and DELETE. Session IDs are private transport handles, not authentication.
Authentication and current durable permissions are checked before entering the
SDK, including reconnect requests. An SDK session belongs to its original
principal and binding.

| Tool | Gateway operation | Arguments | Result |
| --- | --- | --- | --- |
| `send` | `send` | Stable `request_id`, `target`, `message`; optional `issued_at`, `expires_at`, `delivery_policy` | Existing durable admission and delivery facts |
| `read` | `history.read` | `request_id`, room `target`, `limit`; optional `parent_id`, `thread_mode`, `cursor`, `start` | Complete bounded history page and sealed cursor |
| `wait` | `watch` | Same selectors as `read` | Bounded message page, or progress pages when the existing MCP progress token is present |
| `status` with `lookup_id` | `receipt.get` | `request_id`, `lookup_id` | Existing principal-scoped durable receipt |
| `status` without `lookup_id` | Permission discovery | `request_id` | Current available tools and their authorized targets and request/page limits |

Tool names select the operation. Arguments omit `version` and `operation`; the
adapter supplies gateway version one internally. Every tool keeps a validated
`request_id`; a send ID remains the durable retry identity. Fixed argument
structs use the existing strict JSON boundary so duplicate, unknown, aliased,
null and oversized fields fail before dispatch. The schemas expose only the
listed fields, with existing target, selector, time, size and ID constraints.

`tools/list` and discovery through `status` use current durable permissions on
each request. List `send`, `read` and `wait` only when the corresponding
operation is currently allowed; `send` also requires an authorized target, and
room-only tools require an authorized room. `status` remains available to
authenticated authorized principals for discovery; supplying `lookup_id` still requires `receipt.get` permission. Status
discovery exposes only this four-tool interface, never other principal targets,
credentials, binding/account identifiers, global catalogues, or the removed
operation inventory. Calls check current permissions again; discovery is not an authorization grant. Revocation
continues to reject session requests before entering the SDK.

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
protocol headers. Origin permission never supplies a principal identity.
Native clients without an Origin still require bearer authentication.

Logical MCP sessions use the configured global and per-principal session
limits; HTTP requests also consume the shared request slots. HTTP bodies are
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
