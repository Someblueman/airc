# HTTP ingress primitive and shared dispatcher

The shared dispatcher authenticates bearer sessions, Ed25519 signed envelopes
or nested encrypted envelopes before request normalization and journal admission.
Its implemented/configured operation intersection currently contains send,
reply and local receipt/status lookup. Reads use principal-scoped journal keys;
mutations coalesce through the same durable intent and replay transaction.
Durable revocation is rechecked by the journal across all three profiles.
Adapters do not contact AIRC or cancel admitted jobs when a caller disconnects.

`adapter.HTTPServer` returns a standard-library server with an explicit protocol
set (HTTP/1.1 and TLS HTTP/2), TLS 1.3 minimum, header/read/write/idle deadlines,
bounded headers/body and a nonblocking ingress-slot limit. It does not bind an
address or enable configuration. The owner must supply the listener, provisioned
TLS certificates, service context and shutdown/join lifecycle. Plaintext is allowed
only for a loopback peer; remote plaintext requests fail. Unencrypted HTTP/2 is
not enabled. Runtime TLS provisioning and CLI integration remain pending.

Implemented routes:

| Route | Behavior |
|---|---|
| `POST /v1/messages` | Bearer-authenticated strict JSON send/reply; `202` only after journal commit, with `Location: /v1/requests/{id}`. |
| `GET /v1/capabilities` | Authenticated gateway intake operations, caller targets, configured limits and new-job retry/retention defaults; durable revocation checked. |
| `GET /v1/requests/{id}` | Bearer-authenticated local status; no network probe, send or budget replenishment. |

The body supplies the required stable request ID. An optional `Idempotency-Key`
header must occur once and match it. Duplicate intent coalesces; a different body
under the same ID conflicts. POST cannot select read operations. GET cannot submit
chat mutations. Responses disable caching and contain normalized result/error
fields rather than internal errors or credential values. Invalid credentials,
permissions, conflicts, oversized bodies, quotas and unavailable intake have
stable HTTP classifications. Missing scoped status returns `404`.

Only explicit bearer headers are supported by this HTTP primitive. Multiple
Authorization headers, query strings and Origin-bearing requests fail. There are
no cookies or CORS grants. Browser-origin support, bounded
waits/long polling, history, search, directory and SSE remain required pending
work; these omissions are implementation gaps, not HTTP limitations. Signed and
encrypted profiles are verified at the shared dispatcher boundary but do not yet
have HTTP endpoints. There is no claim of a complete HTTP adapter or gateway.

Capability discovery describes available gateway intake, including durable send
while AIRC is offline. `upstream_compatible` is currently `null`; this response
never claims that an upstream feature negotiation succeeded. Backend feature
checks still precede any write. `retry_defaults` applies to newly admitted jobs;
existing jobs retain their original stored policy, budgets, expiry and retention.
Configured resource limits do not advertise an unimplemented operation. Discovery
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
operations and clean CLI first-use acceptance are pending. Other rows remain
proposed. No live daemon, account, global install, published listener or deployment
is involved in these isolated tests.
