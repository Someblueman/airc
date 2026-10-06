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

`watch` and `events.watch` return a bounded page without an MCP progress token.
With a progress token, each progress notification contains a
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
remaining native reads remain shared gateway work. Nonmessage live events use
[events.watch](live-events.md).
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

## Required four-tool replacement

The roadmap requires replacing the operation-per-tool interface above. This
section defines that replacement; it is not yet an implemented-interface claim.
The native `airc mcp` subprocess is a separate published contract and is not
changed by the gateway replacement. No compatibility aliases remain.

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
room-only tools require an authorized room. `status` remains available to authenticated authorized principals for
discovery; supplying `lookup_id` still requires `receipt.get` permission. Status
discovery exposes only this four-tool interface, never other principal targets,
credentials, binding/account identifiers, global catalogues, or the removed
operation inventory. Calls check
current permissions again; discovery is not an authorization grant. Revocation
continues to reject session requests before entering the SDK.

The result values, admission uncertainty, bounded retries, progress budgets,
watch renewal, opaque cursor scope, TLS/origin/session boundaries and joined
shutdown described above remain. A cursor created for `watch` still belongs to
`watch`, even though its public MCP tool is called `wait`. SDK JSON-RPC IDs
remain transport correlation IDs.

The gateway caller inventory is repository-owned tests; the disabled example
contains listener configuration, not tool calls. Migrate these together:

- `integration/gateway/mcp_http_fixture_test.go`: map request builders to the
  four explicit tools and omit wire version/operation. Receipt polling uses
  `status`; unsupported request types must fail in the fixture, not be routed
  through a generic operation field.
- `mcp_http_test.go` and `mcp_http_session_test.go`: replace 34-tool discovery
  and the all-operation matrix with send/read/wait/status acceptance, scoped
  discovery, strict arguments and rejected removed tools. Preserve native
  author/target/body observation, accepted receipts, HTTP/1.1/2, progress,
  renewal/resume, reconnect idempotence, session isolation and shutdown.
- `channels_mcp_test.go`, `members_mcp_test.go`, `live_mcp_test.go`: remove the
  superseded catalogue/membership/signal interfaces and verify they are absent
  and cannot be called. Their other-carrier and shared backend coverage stays.
- Adapter MCP schema, tool-dispatch and lifecycle tests: migrate arguments and
  tool names, exercise current permission changes in an existing SDK session,
  and preserve quota, revocation, strict JSON, result bounds and joined cleanup.

This repository caller inventory cannot establish which external gateway
clients are deployed. Gateway users must migrate the published request format
when adopting the replacement; the separate native MCP owner decisions remain
on the roadmap.
