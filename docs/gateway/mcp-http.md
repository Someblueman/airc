# MCP over Streamable HTTP

This adapter increment exposes the current shared gateway operations.
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
the dedicated gateway cursor key.

Network configuration follows the other gateway HTTP listeners: cleartext is
limited to loopback; remote listeners require verified TLS. Provision a gateway
bearer credential for each principal, separately from its upstream account
credential. The client sends `Authorization: Bearer TOKEN` on every POST, GET
and DELETE. Session IDs are private transport handles, not authentication.
Authentication and current durable permissions are checked before entering the
SDK, including reconnect requests. An SDK session belongs to its original
principal and binding.

Tool names use gateway operation names. Arguments are the complete gateway v1
request object, with its explicit `version`, stable `request_id`, `operation`
and operation-specific fields. Tool name and operation must agree. The raw
arguments pass through the existing strict decoder, preserving duplicate,
unknown, case-aliased and null-field rejection. JSON-RPC IDs only correlate MCP
calls; they do not replace gateway request IDs or affect native retry identity.

Mutation tools return the ordinary durable gateway result. Use `receipt.get`
with `lookup_id` to observe later native acceptance. Repeating the same logical
request preserves its original gateway intent and receipt; conflicting content
under one request ID fails. Default bounded retries and possible duplicate
posts under uncertain delivery remain the existing delivery contract.

Read tools return their complete existing bounded snapshot or page as
`structuredContent`; results contain a short text description rather than a
second full copy of the JSON. `capabilities.get` returns the current permission
intersection. Tool discovery is not authority to access another target or to
bypass later revocation.

`watch` requires an MCP progress token. Each progress notification contains a
whole JSON history page in its `message`, with the original progress token and
an increasing progress counter. Calls are bounded by the gateway read lease,
page/stream budgets and cancellation. Preserve the returned opaque gateway
cursor and renew through another authenticated watch call or a new MCP session.
The cursor remains scoped to the principal, binding, operation and selector.

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
remaining native reads and nonmessage live events remain shared gateway work.
Catalogue row 31 uses the distinct framed stdio adapter, documented in
[stdio.md](stdio.md); this increment does not add an MCP stdio profile.

## Verification

Public SDK client race tests verify principal session isolation, global and
per-principal quota release, reopening sessions, removal of completed server
records, and shutdown joining. Active journal revocation rejects subsequent
POST tool calls, GET streams and DELETE on an existing SDK session. Public SDK
tool calls preserve strict raw argument rejection and required argument schema
discovery. The whole adapter race suite passed in 3.442s; focused service,
CLI and configuration race checks passed in 3.228s/2.306s/2.627s.

The disabled example passed actual CLI configuration validation and capability
discovery without starting a listener or creating state. Whole-module vet,
staticcheck and module tidiness checks passed after the final identifier-bound
guard. The standalone native MCP suite passed in 35.526s, with all current
operations, independent message observation, typed reads and mutation results,
HTTP/1.1 and actual HTTP/2, live progress, cancellation, lease renewal and cursor
resumption, reconnect replay without a duplicate native send, cross-principal
session rejection and clean SIGINT shutdown with an active watch. Full normal
module acceptance passed (integration 456.595s, journal 6.998s). Full race
module acceptance passed (integration 478.265s, journal 38.069s).
No complete MCP catalogue adapter is claimed.
