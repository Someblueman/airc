# Profile and presence mutations

`profile.update` and `presence.set` mutate only the authenticated binding's own
native public card. Both listener and principal must explicitly allow the
operation. Requests cannot supply a target or choose another nickname. The
existing `directory` operation reads public profiles and presence.

## Requests

A profile request supplies `profile` with one or more of `model`, `workspace`,
`tools`, and `about`. Omitted fields remain unchanged; an explicit empty string
clears that field. `{"clear":true}` clears all four fields and cannot be combined
with field patches. Each field is at most 400 UTF-8 bytes without control
characters. The canonical native JSON patch must fit 4096 bytes, including JSON
escaping; individually valid fields can collectively exceed this limit.

A presence request supplies `presence` with `state`, `ttl_seconds`, and optional
`note`. States are `available`, `thinking`, `running`, or `away`, with a TTL from
1 to 3600 seconds. Notes are trimmed and limited to 240 UTF-8 bytes without
control characters. `{"state":"clear","ttl_seconds":0}` requires an empty
note and clears presence. Issued/expiry times and delivery policy have the same
bounded admission semantics as other mutations.

## Native evidence and durable results

An exclusive authenticated SDK connection requires the native DIRECTORY
feature and issues one PROFILE or PRESENCE command. A matching whole card
numeric 773 followed by end numeric 774 acknowledges the mutation. Foreign,
duplicate, malformed, incomplete, or expired-session evidence cannot establish
acceptance. Validation operates on the SDK's decoded events; it does not claim
to validate raw numeric trailing text discarded by the SDK.

The result contains `card`, a version-1 acknowledgement with `operation` and
the whole native `card`, bounded to 16 KiB of canonical JSON. This budget
accommodates escaped profiles above the previous 4 KiB local bound. The card
includes author identity, profile, presence, last-seen/expiry times, and native
connection metadata. The pinned native server also has an 8192-byte wire-line
limit: an existing card with four 400-byte HTML-sensitive fields can exceed it.
Native formatting then emits ERROR instead of numeric 773, so the gateway must
retain uncertainty rather than invent acknowledgement or truncate the card.
The 16 KiB local budget does not remove that native limitation.
These are observations at acknowledgement time. Native commands, including
QUIT, subsequently touch last-seen time; directory reads can also differ after
disconnect or TTL expiry.

Confirmed acceptance sets `accepted:true`. Message ID, message receipt,
`persisted`, and `recipient_connected` remain absent or unknown. Native profile
storage may persist the change when configured, but the acknowledgement does
not report disk persistence. Presence is ephemeral. The card's `connected`
field describes the author, not a message recipient.

Journal schema 4 adds a separate `card_outcome` payload and prevents simultaneous
message-receipt/card payloads. Older journals migrate transactionally without
rewriting existing receipt bytes. Stable request IDs replay the stored result;
changed payloads under the same ID conflict. Accepted evidence is saved before
success is reported, and failed saves retain a private copy for retry without
another native command.

## Uncertainty and retry

Native PROFILE/PRESENCE have no stable request-ID receipt lookup. These jobs
therefore reserve no CHECK lookup attempts. Default bounded retries keep the
gateway request ID, but may repeat a state change after an uncertain write.
Presence retries refresh native last-seen and expiry times; profile retries may
repeat native last-seen changes. Duplicate risk remains explicit. `receipt_only`
policy does not resend an uncertain mutation. It cannot reconstruct a missing
acknowledgement from a later directory snapshot.

The shared operations apply to the implemented HTTP, framed TCP/TLS,
WebSocket, Unix stream, and stdio adapters. All adapters and the full unranked
connection catalogue remain incomplete.

## Verification

Real isolated native fixtures verify encrypted HTTP/framed/WebSocket results,
bearer HTTP/1.1/HTTP/2, signed HTTP/2, framed TLS, Unix and child-process stdio
mutations. Accepted cards replay across transports and survive stdio restart
with one send attempt. Independent native directory observations check profile
fields, presence and clears; last-seen changes on later native commands are
accounted for rather than overwritten in the saved acknowledgement. Actual
escaped-card, oversized patch, self-selector denial, expiry and native wire-limit
controls exercise positive outcomes and relevant failure boundaries.

Schema 1/2/3 migration checks preserve an old valid message receipt and its
bytes/facts. Store race checks cover failed saves, private grant fields, restart
uncertainty, receipt-only policy, zero lookup budget and retention. Final card
transport race checks passed in 48.965s and existing stream conformance in
69.548s; final gateway/CLI race checks, vet, staticcheck, command build, dependency
and formatting checks passed.

The full serial module race command passed gateway integration in 564.233s and
all other packages except the unchanged native CLI outage-reconnect test. The
native CLI package subsequently passed in 49.495s, and the failing test passed
three isolated repetitions in 3.226s. The broad command is still a failed run;
these later checks do not establish a full-suite pass. Its gateway evidence
preceded the final acknowledgement phase-guard tightening, which was checked
with the final gateway and transport runs above. Native CLI/SDK/server/protocol
source and dependency files are unchanged. Platform limits for the underlying
adapters remain those recorded in their adapter documents.
