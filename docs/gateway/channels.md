# Global room discovery

`channels.read` is an implemented shared gateway operation. It does not
complete an adapter or the full catalogue.

The exact v1 request contains `version`, `request_id` and
`operation: "channels.read"`. Target, query, cursor, limit, message, parent,
delivery policy and validity fields are rejected. An explicit listener and
principal operation permission is required. Like `directory`, this is global
discovery: granting it reveals rooms outside the principal's message targets.
It does not authorize messages or room-specific reads in those rooms.

Native `CHANNELS` includes rooms known from connected membership, retained
room history or a topic. It excludes direct-message targets. Each channel has
`name`, `members`, `messages`, optional `last_activity` and optional `topic`.
The gateway response is `{"channels":[]}` for an empty snapshot; otherwise it
contains the complete list in lexical room-name order. The maximum is 256 rooms
and 48 KiB. Overflow fails the entire read rather than hiding rooms through
truncation. No receipt, durable job or room join is created.

The backend uses a private ephemeral session and requires native `CHANNELS=1`.
Its opt-in SDK raw query preserves native 768/769 envelopes for recipient,
payload and terminator validation, while ordinary SDK callers retain typed
channel events. Reads use the existing one-use grants, bounded leases, pool
reserve, cancellation and shutdown ownership.

Bearer HTTP uses `GET /v1/channels` without query arguments. Signed/encrypted
HTTP, framed TCP/TLS, WebSocket, Unix and stdio return the ordinary detached
`channels` field. MCP HTTP exposes `channels.read` with the same envelope.
Capabilities advertise `channels_scope: "public_global"`, `max_channels` and
`max_channels_bytes` only when the operation is currently permitted.

Focused native race acceptance passed in 20.368s, including encrypted HTTP,
framed/WebSocket and Unix, signed HTTP/2, bearer HTTP/2, signed TLS framed and
secure WebSocket, signed/encrypted stdio and public MCP SDK clients. The MCP
tool list has 32 tools and its full operation matrix includes channel discovery.
Independent native probes cover history-only, topic-only and live-only rooms,
case-distinct names and direct-target exclusion. Repeated HTTP/MCP snapshots
after PART reflect the native membership change and preserve full retained
message metadata. Unix exercises explicit empty and populated snapshots; a
separate real fixture denies global discovery to a room-authorized principal.

Core/config/global grant, SDK/backend and gateway race checks pass. Strict
collector tests cover malformed/foreign replies, missing completion, JSON
duplicates/nulls/case aliases, timestamps, sorting, native errors, count and
wire/encoded overflow, and cancellation. Exact-origin GET browser preflight and
denied origin/method cases pass. Read-only CLI validation/discovery reports 31
operations without starting runtime. Vet, staticcheck, module tidiness and diff
checks pass. An initial MCP refresh test retained omitted fields by decoding
into the previous result; it now decodes a fresh snapshot. Signed requests on
plaintext framed/WebSocket fixtures were correctly rejected; their
encrypted-only policy remains intact, and signed acceptance uses TLS fixtures.

Full normal/race module acceptance passed:
`go test ./... -count=1 -timeout=900s -p=1` and
`go test -race ./... -count=1 -timeout=900s -p=1` both exited successfully.
Integration passed in 460.951s/469.036s; journal checks in 5.794s/25.862s.
The full catalogue remains outstanding. DM reads remain unverified here;
nonmessage live events are optional.
