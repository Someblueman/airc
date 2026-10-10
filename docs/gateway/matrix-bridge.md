# Matrix bridge profile

Private bounded identifier helpers retain exact user, room and event values.
User localparts follow the current grammar; server names support ASCII DNS,
IPv4 and bracketed IPv6 with optional ports 1–65535, without zones or IDNA
conversion. Opaque room/event IDs preserve case and Unicode, including legacy
and domainless forms. The selected 255-byte profile refuses controls and
whitespace; room aliases never establish room authorization. See the
[Matrix identifier specification](https://spec.matrix.org/latest/appendices/#identifier-grammar).

These identifier helpers return parsed data. Private verified readers now own
homeserver provisioning, account and membership checks, exact author/room grants,
loop prevention and originating-event freshness. Durable Send admission and complete visible-history recovery are implemented.
Service ownership, bounded replies and real-client/native qualification remain
on the roadmap; testing of recovery is deferred.

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
before issuance prevents proof creation. The reader refuses future events and
those at or beyond the configured freshness window. Its replay deadline is fixed
at `origin_server_ts` plus that window; retrieval never renews it. The originating
Matrix event timestamp, as reported by the configured TLS homeserver, is the
trust boundary. This does not independently verify federation signatures or the
age of content imported by an application service. Current membership checks do
not prove historical membership at the event's origin time.

The content fingerprint binds network, global event ID, exact author, room,
origin timestamp, whole body and mapped native identity/binding/target. Display
names, unsigned age and client transaction IDs cannot change it. Durable replay admission is implemented below; service integration remains open.

Private `ParseTimeline` now reads a complete sync page as bounded data: 64 KiB,
16 nesting levels, 256 combined join/leave rooms and 256 joined-timeline events.
The shared validator exposes an explicit depth limit while ordinary gateway
envelopes remain at eight. Opaque cursors preserve exact case/Unicode and are
limited to 1–1,024 bytes without controls or whitespace. Joined-room message
references retain exact room/event IDs and each room's event order, with no
inferred cross-room chronology. Duplicate global event IDs, room mismatches,
malformed arrays/objects and ambiguous join/leave membership are refused.

Limited timelines retain explicit room/previous-batch gap markers, and room
departures are exposed separately. A limited timeline without a valid previous
batch token is refused. Ancillary state is bounded but supplies no event
authority. See [Matrix sync](https://spec.matrix.org/latest/client-server-api/#get_matrixclientv3sync).
`next_batch` is parsed continuation data, not acknowledgement or durable intake
progress. The authenticated owner must handle gaps/departures and admit whole
events before advancing; the recovery owner below now does so.

Private `ReadTimeline` now owns account and current joined-room verification
followed by a filtered sync request under one shared three-second deadline.
The inline filter names sorted distinct provisioned rooms and requests up to
128 room-message events per room. Canonical URL encoding preserves opaque
`since` values as a single parameter; only this filter profile, timeout zero
and offline presence are admitted by the HTTP path boundary. Duplicate or
additional query parameters, including access tokens and user impersonation,
are refused. The page remains bounded to the parser's 64 KiB/256-event limits.

Only a verified response can mint immutable `VerifiedMatrixTimeline`
with copied event references and source/continuation cursors. Unknown referenced
rooms and nonprogressing nonempty pages are refused. Limited history is exposed as cloned gap markers on that proof and must be
recovered before progression. Room departures return an explicit loss error
with copied diagnostic positions and no usable page or next cursor. Initial pages are marked explicitly; empty idle
pages may retain the same cursor. Cancellation/expiry before issuance refuses
the page. References still require authenticated `ReadMessage` retrieval before
author/native authority exists. Cold-start recovery and durable progression
are implemented below; service integration remains open.

Private durable send admission now uses journal schema 17, adding bounded
`matrix_events` state without changing existing jobs, IRC replay marks or broker
positions. Admission checks current authority and the fixed original deadline,
then atomically records content/identity binding and the native job. Identical
replays retain the same job; changed content, authority or deadline conflicts.
A retained mark cannot recreate a pruned job. Expired marks are removed only
after their native jobs disappear; the 16,384-mark limit refuses new intake
without evicting live bindings. Dispatcher intake requires proof belonging to
its own verifier and enabled send operation, and performs no native contact.
This prerequisite does not advance timeline cursors or enable a service.

Private `ParseHistory` bounds room-history responses to 64 KiB, depth 16 and
256 chunk events. It preserves server order and opaque start/end cursors,
requires every chunk event to name the exact room, and rejects duplicate event
IDs, malformed cursors and non-progressing end tokens. Non-message events are
validated then ignored; ancillary state is bounded data without authority. An
empty chunk does not imply exhaustion: only an absent `end` token does. This
means exhaustion of history visible to the requesting account, not proof that
all room history exists or is accessible. See the
[room history API](https://spec.matrix.org/latest/client-server-api/#get_matrixclientv3roomsroomidmessages).
These parsed references cannot authorize native intake. The recovery owner
below performs exact authenticated retrieval before admitting or refusing them.

Private `ReadHistory` now verifies the account and all provisioned room
memberships, then retrieves one exact room-history page under the same absolute
three-second deadline. It accepts only a provisioned room, forward/backward
direction, bounded opaque optional from/to cursors and fixed request limit 128.
Canonical reconstruction refuses extra, duplicate or changed query parameters,
including credentials and user impersonation. Explicit `from` must exactly
match the response `start`; unknown rooms are refused before network contact.
Only this reader mints immutable `VerifiedMatrixHistory` source pages, retaining
the request, response cursors and cloned event references. Cancellation, closed
owners and expired deadlines cannot yield a page. Each reference still requires
verified event retrieval and durable admission. This reader itself does not
advance a cursor; the recovery owner below persists progression. Visible-history
exhaustion does not prove complete federated room history.


## Complete visible-history recovery

The selected startup policy is complete recovery of history visible to the
configured account, with no automatic new-cursor baseline or age-based shortcut.
`AdvanceMatrix` implements one bounded page or position transition at a time.
The upcoming service owner drives it serially; this checkpoint alone does not
register a service or send Matrix replies.

First, a verified `/sync` supplies a pending `next_batch` anchor. For every
provisioned room, in stable lexical order, recovery requests `/messages` with
`dir=f`, fixed limit 128, `from` equal to the last fully committed sync cursor
and `to` equal to the pending anchor. Cold start omits `from`, which requires
Matrix Client-Server API v1.3 or later and starts at the first visible event.
Both `from` and `to` accept sync tokens. These are the chosen concrete API
semantics, documented by the
[Matrix v1.3 room history API](https://spec.matrix.org/v1.3/client-server-api/#get_matrixclientv3roomsroomidmessages).
Each response remains bounded to 64 KiB and 256 events by the existing parser.

The same forward-history path runs for every new sync interval, including
limited/gapped timelines. Sync references are never taken as a complete history
baseline. An empty history chunk with an `end` cursor continues; only absent
`end` or reaching the exact pending `to` anchor completes a room. Repeated
nonprogressing page tokens are refused. The global sync cursor moves only after
all provisioned rooms reach their interval boundary. Room departure, unknown
room, invalid page metadata or a nonprogressing sync with events/gaps cannot
establish progress. Provider-side retention or membership visibility still
limits what history is accessible; this is not proof of complete federation
history or recovery of server-deleted events.

Every message reference is fetched again through the fixed authenticated HTTPS
source and current account/membership checks. `ReadMessage` now returns either
an immutable eligible proof, an explicit terminal source disposition, or an
error. Before any disposition it validates bounded JSON and the exact requested
room/event ID, `m.room.message` type, canonical sender and nonnegative safe-integer
original timestamp. Eligible whole plain text must fit the existing Send
request contract without normalization changing its body or target.

The explicit dispositions are `unmapped` (including bridge echoes), `unsupported`
(content or operation outside this profile), `expired` and `future`. Recovery
never makes old or future events fresh. Authenticated source metadata failures,
HTTP failures, missing membership, cancellation and deadline expiry are errors,
not terminal dispositions. These errors stop the page without advancing its
position. Fresh authorized events enter the existing Matrix durable journal
before page completion. A conflict, admission-capacity refusal or uncertain
admission write also leaves the page pending. Repeating partially completed pages
uses the same network/event identity and existing immutable fingerprint checks;
it cannot create a second native Send.

Schema version 20 adds only `matrix_progress`, retaining a committed sync cursor,
pending target cursor, current room index/history cursor, monotone revision and
cumulative count of explicitly refused references. Cursors are bounded to 1,024
UTF-8 bytes and the table to 128 owner identities. No event body or access token
is persisted there. Whole-page completion and its refusal count are saved in
one checkpointed compare-and-save transaction. A concurrent/stale owner cannot
overwrite another revision. A crash after admission but before that transaction
repeats the same page. The refusal count records the completed recovery pass;
an event admitted during an earlier interrupted pass can be expired on retry,
so the count is not a count of uniquely undelivered events.

The position identity binds network ID, numeric endpoint, TLS server name,
bridge account, realm, freshness duration and all sorted author/room mappings,
principal/binding/target/operation grants. Changing those inputs selects a fresh
history recovery instead of silently reusing another mapping's cursor. Existing
Send replay marks remain global to their original network/event identity.
Positions are not evicted to admit a new owner; reaching the table limit refuses
new recovery state.

Each source fetch and event admission has a three-second budget and holds shared
request capacity only for that operation. Capacity is released between events;
one history page cannot monopolize a slot for its full traversal. Parent
cancellation stops further work, and no later cursor transition is committed
from a failed operation.

Production builds passed for Darwin ARM64 and Linux AMD64/ARM64 with CGO
disabled. The migration and recovery boundaries have not been runtime tested. Crash/CAS
behavior, cold-start and limited-sync recovery, explicit refusals, original-time
bounds, native delivery, cancellation and real Matrix interoperability remain
verification work in the roadmap. Service ownership and protected bounded reads
are the next separate implementation item.
