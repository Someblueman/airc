# Matrix bridge profile

Private bounded identifier helpers retain exact user, room and event values.
User localparts follow the current grammar; server names support ASCII DNS,
IPv4 and bracketed IPv6 with optional ports 1–65535, without zones or IDNA
conversion. Opaque room/event IDs preserve case and Unicode, including legacy
and domainless forms. The selected 255-byte profile refuses controls and
whitespace; room aliases never establish room authorization. See the
[Matrix identifier specification](https://spec.matrix.org/latest/appendices/#identifier-grammar).

These are parsed data, not identity proofs. Verified homeserver provisioning,
account and membership checks, explicit author/room grants, loop prevention,
freshness, durable admission, bounded replies and real-client/native
qualification remain on the roadmap. No service consumes these helpers yet.

The private `MatrixBridge` configuration validator requires one concrete
numeric-IP dial endpoint with nonzero port, a verified TLS server name and
optional absolute CA path, an exact bridge account and absolute access-token
path. It bounds freshness provisioning to 1–300 seconds and grants to 1–128
distinct exact author/room pairs. Each grant must name an active existing
principal with the exact target and distinct explicitly authorized send,
context, history or search operations; history/search require a cursor key.
Bridge-account grants and room aliases are refused. Case-distinct opaque rooms
remain distinct grants. This configuration is not registered with the service;
file contents/permissions, actual verifier ownership, homeserver authentication
and reply disclosure are not established by configuration validation.

The private owner now clones explicit operation grants and binds them to actual
verifier-owned identities, rejecting configuration/verifier authority mismatch.
It prepares verified TLS 1.3 with an optional validated CA file and reads an
owner-only regular token file using the existing descriptor checks. Tokens are
1–1,024 bytes with Bearer header grammar and no newline trimming; Close clears
the retained bytes after users join. See [RFC 6750](https://www.rfc-editor.org/rfc/rfc6750#section-2.1).
The owner now makes bounded account-verification requests; event proofs remain open.

`VerifyAccount` calls only the fixed homeserver's `GET /_matrix/client/v3/account/whoami`
with the private token in the Authorization header. It dials the provisioned
numeric endpoint while validating the configured TLS name; environment proxies,
redirects, query credentials, compression and keepalive reuse are disabled.
One three-second context covers dialing, verified TLS 1.3, headers and body;
headers are bounded to 8 KiB and body to 16 KiB. Only HTTP 200 JSON with the
exact non-guest bridge account succeeds. Strict JSON validation refuses
duplicate/aliased/unknown/null fields and invalid Unicode. Optional device ID
and guest fields follow the [whoami API](https://spec.matrix.org/latest/client-server-api/#get_matrixclientv3accountwhoami).
This check establishes current token/account provenance only, with no room,
event or native admission authority; it has no service caller yet.

`VerifyRooms` first verifies the exact non-guest account, then queries
`GET /_matrix/client/v3/joined_rooms` through the same fixed-endpoint HTTP
boundary. One shared three-second context caps both requests. The room response
is bounded to 64 KiB and 256 distinct valid opaque IDs; every provisioned
author/room grant must reference an exact returned room. Missing rooms, aliases,
duplicate IDs and case mismatches are refused. See the
[joined rooms API](https://spec.matrix.org/latest/client-server-api/#get_matrixclientv3joined_rooms).
The check performs no join or other membership mutation and caches no event
authority. It proves membership only at this check; later event processing
still needs validated timeline membership and author provenance.

Private `ParseMessage` reads one complete JSON event, bounded to 64 KiB and
the shared eight-level nesting limit. It refuses duplicate keys, invalid
Unicode and trailing data. Exact `m.room.message`/`m.text` fields, valid sender
and event IDs, and a nonnegative integer origin timestamp within the Matrix
JSON safe-integer range are required. The selected room is explicit; an
optional `room_id` must match exactly. The whole plain body is retained,
including spaces and line breaks. Blank/NUL bodies, state/redaction events,
redacted metadata, edits and relational messages are refused by this plain
message profile. Formatted bodies and bounded ancillary extensions are never
substituted for the plain body. See [Matrix text messages](https://spec.matrix.org/latest/client-server-api/#mtext).
Returned fields are data, with no authentication, historical membership or
freshness claim. `unsigned.age` and client transaction IDs are ignored. An
authenticated reader, exact grants, loop prevention and durable admission are
still required before native intake.

Private `ReadMessage` now owns exact account verification, current provisioned
room membership and `GET /_matrix/client/v3/rooms/{roomId}/event/{eventId}`
retrieval under one shared three-second deadline. Room and event path segments
are canonically escaped; the HTTP boundary admits only those validated paths
and the account/membership endpoints. Unknown rooms are refused before network
contact. Retrieved plain events must include the exact requested room and ID,
and their author must have an explicit immutable grant for that room; unmapped
authors and the bridge account cannot mint authority. See the
[room event API](https://spec.matrix.org/latest/client-server-api/#get_matrixclientv3roomsroomideventeventid).

Only this verified reader can mint `VerifiedMatrixMessage`, retaining mapped
identity, target, cloned operations and whole body. Zero values grant nothing.
Stable request identity includes configured network and globally unique event
ID, with room/author retained as separate provenance. Cancellation or expiry
before issuance prevents proof creation. Origin timestamps remain data; neither
original-event freshness nor durable replay admission is established. The
homeserver supplies event provenance; current membership checks do not prove
historical membership at the event's origin time. No timeline poller or service
consumer exists yet.
