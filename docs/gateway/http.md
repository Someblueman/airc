# HTTP ingress primitive and shared dispatcher

The shared dispatcher authenticates bearer sessions, Ed25519 signed envelopes
or nested encrypted envelopes before request normalization and journal admission.
Its mutation/status profile implements send, reply, room reaction.add and local receipt/status lookup.
A separate bearer read profile implements bounded room history, native search,
long-poll watch and SSE with opaque cursors, bounded room conversation context,
and public directory snapshots.
Status reads use principal-scoped journal keys;
mutations coalesce through the same durable intent and replay transaction.
Durable revocation is rechecked by the journal across all three profiles.
Adapters do not contact AIRC or cancel admitted jobs when a caller disconnects.

`adapter.HTTPServer` returns a standard-library server with an explicit protocol
set (HTTP/1.1 and TLS HTTP/2), TLS 1.3 minimum, header/read/write/idle deadlines,
bounded headers/body and a nonblocking ingress-slot limit. It does not bind an
address or enable configuration. The owner must supply the listener, provisioned
TLS certificates, service context and shutdown/join lifecycle. Plaintext is allowed
only for a loopback peer; remote plaintext requests fail. Unencrypted HTTP/2 is
not enabled. Configured owner-verified TLS loading and CLI lifecycle are in
[service.md](service.md).

Implemented routes:

| Route | Behavior |
|---|---|
| `POST /v1/rpc` | JSON-RPC 2.0 calls, notifications and bounded batches over the full shared operation set; see [jsonrpc.md](jsonrpc.md). |
| `POST /v1/rpc/events` | JSON-RPC page notifications and a terminal correlated response over bounded SSE. |
| `POST /v1/messages` | Bearer-authenticated strict JSON send/reply/room reaction.add; `202` only after journal commit, with `Location: /v1/requests/{id}`. |
| `PUT /v1/requests/{id}` | The same durable mutation admission at a stable request URI; body `request_id` and optional Idempotency-Key must match that URI. |
| `POST /v1/envelopes/signed`, `/v1/envelopes/encrypted` | Detached JWS/JWE authority for delivery, current read operations and caller capability discovery; see [signed-message-v1.md](signed-message-v1.md). |
| `GET /v1/capabilities` | Authenticated gateway intake operations, caller targets, configured limits and new-job retry/retention defaults; durable revocation checked. |
| `GET /v1/requests/{id}` | Bearer-authenticated local status; no network probe, send or budget replenishment. |
| `GET /v1/search` | Bearer-authenticated native room substring/sender search, scoped opaque pagination and explicit expiry; see [search.md](search.md). |
| `GET /v1/watch` | Bearer-authenticated native live room updates with bounded long polling and opaque snapshot/resume; see [watch.md](watch.md). |
| `GET /v1/events` | Bearer-authenticated single-session SSE room messages, opaque event IDs, retained-prefix resume and bounded leases; see [events.md](events.md). |
| `POST /v1/envelopes/signed/events`, `/v1/envelopes/encrypted/events` | Signed/encrypted watch envelopes return the same bounded SSE frames, with signed selectors/cursors and validity-bound leases. |
| `GET /v1/directory` | Bearer-authenticated complete public-card snapshot, explicit global discovery permission and bounded card/byte limits; see [directory.md](directory.md). |
| `GET /v1/history` | Bearer-authenticated bounded room snapshot; typed selector, retained/recent initial mode, opaque cursor, explicit status/gap and current authorization. |
| `GET /v1/context` | Bearer-authenticated bounded room conversation context, exact retained-trigger ownership proof and whole native records; see [context.md](context.md). |
| `GET /v1/pins` | Bearer-authenticated whole room pin snapshot; see [pins.md](pins.md). |
| `GET /v1/room` | Bearer-authenticated native stored room settings; see [room-waiting.md](room-waiting.md). |
| `GET /v1/waiting` | Bearer-authenticated reply-coming signals with retained-question ownership proof; see [room-waiting.md](room-waiting.md). |

The body supplies the required stable request ID. An optional `Idempotency-Key`
header must occur once and match it. Duplicate intent coalesces; a different body
under the same ID conflicts. PUT creates the immutable delivery request resource
at its stable URI; it cannot replace admitted intent or edit an AIRC message.
POST and PUT share principal/binding-scoped identity, so cross-method repeats
coalesce without resetting budgets or expiry. Successful PUT also returns `202`
and the same scoped Location, including a replay of an already accepted request.
Request IDs must not be reused beyond the published retention window.
POST/PUT cannot select read operations. GET cannot submit
chat mutations. Responses disable caching and contain normalized result/error
fields rather than internal errors or credential values. Invalid credentials,
permissions, conflicts, oversized bodies, quotas and unavailable intake have
stable HTTP classifications. Missing scoped status returns `404`.

The regular routes use explicit bearer headers; the separate envelope routes
use signed sender identity without bearer credentials. Multiple
Authorization headers fail. Browser origins are denied by default; per-listener
`allowed_origins` grants exact-origin Fetch visibility, with bounded preflight
methods/headers and no cookie credentials. Actual requests still authenticate
and authorize normally. See [browser.md](browser.md). Query parameters are allowed
only by the strict history/search/watch/events/directory/context read routes. Bounded delivery-result
waits, DM reads, directory pagination, profile/presence writes and nonmessage updates remain required pending work;
these omissions are implementation gaps, not HTTP limitations. Signed and
encrypted profiles have separate bounded envelope and SSE endpoints; see
[signed-message-v1.md](signed-message-v1.md). There is no claim of a complete HTTP adapter or gateway.

Capability discovery describes available gateway intake, including durable send
while AIRC is offline. `upstream_compatible` is currently `null`; this response
never claims that an upstream feature negotiation succeeded. Backend feature
checks still precede any write. `retry_defaults` applies to newly admitted jobs;
existing jobs retain their original stored policy, budgets, expiry and retention.
Configured resource limits do not advertise an unimplemented operation. Discovery
also returns operation-specific history/search/watch targets and byte bounds only
for the caller's implemented room selectors, plus the explicit long-poll watch mode;
a direct-only permission does not advertise those reads. General targets remain the caller's permission set.
Directory permission separately advertises `public_global` scope and snapshot
limits; chat target allowlists do not restrict public discovery. Discovery
performs no backend dial/probe, job mutation, send or budget replenishment.

## Evidence

Real TLS clients negotiate HTTP/1.1 and HTTP/2 against an owned loopback listener,
submit durable posts and replies, poll a scoped result and repeat/conflict IDs.
The pinned independent AIRC observer verifies author, room, body, message ID,
parent and thread root. Additional real HTTP/2 requests verify invalid credentials,
forbidden targets, cross-principal status isolation, bounded body size, unknown
JSON fields, read-in-POST rejection, safe GET, URL-query rejection, Origin denial,
duplicate Authorization and conflicting header/body IDs. Real HTTP/2 capability
reads show distinct principal permissions/targets, reject invalid credentials,
reflect durable revocation and preserve access for another principal. Returned
permission snapshots cannot mutate journal or credential authority.

Dispatcher tests verify signed/encrypted/bearer coalescing into one job with its
retained replay marker, durable revocation across profiles and a configured
operation fence that admits no job. Codec negative vectors remain in auth tests.

```sh
go test -race -count=1 ./integration/gateway -run 'TestHTTP|TestDispatch'
```

Catalogue rows 13, 15 and 17 have these partial primitives/evidence; their remaining
operations and complete acceptance are pending. Isolated CLI submission/status
acceptance is in [service.md](service.md). Other rows remain
proposed. No live daemon, account, global install, published listener or deployment
is involved in these isolated tests.
