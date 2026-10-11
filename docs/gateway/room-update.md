# Room setting updates

`room.update` changes the stored slow-mode and history settings of one exact
authorized room. Omitted fields preserve their prior values.

```json
{"version":1,"request_id":"room-update-1","operation":"room.update","target":{"kind":"room","name":"#room"},"room":{"slow_seconds":5,"history_limit":32}}
```

The room patch requires at least one explicit field. `slow_seconds` is an
integer from 0 through 3600; `history_limit` is a nonnegative integer whose
upper bound is the upstream configured global history limit. Zero is a native
stored setting, not an omitted field: slow mode zero disables throttling,
and history zero removes the per-room quota while the global limit still
applies. Gateway parser bounds do not invent a new global retention ceiling. Message, parent, reaction, topic, profile,
presence and read selectors are invalid on this mutation.

The listener must allow `room.update` and the room. The author also needs
native channel-operator or admin authority; listener permission does not
confer it. The gateway does not grant operators, create accounts or acquire
administration on behalf of an incoming request.

One exclusive session sends native CHAT with `action:"room"`; omitted patch
fields become native `-1` preserve selectors. CHAT feature negotiation is
required. A whole matching room record followed by the native terminator
confirms acceptance. The returned target and explicitly supplied values must
match; omitted values are retained in the acknowledgement as observed native
settings. Malformed, duplicate, foreign or incomplete evidence cannot produce
an accepted result. Native permission and validation failures remain typed.

The durable result includes a `room` acknowledgement containing `version:1`,
`operation:"room.update"` and the whole `RoomResult`. No message ID, recipient
connection or persistence fact is invented. Although native settings save
atomically before acknowledgement, the wire response has no persistence flag;
`persisted` and `recipient_connected` remain null. A history setting can
immediately reduce retained native messages; acknowledgement is not a backup.

Schema 6 stores room acknowledgements exclusively with respect to message,
card and topic outcomes. Older durable evidence migrates transactionally.
A failed save retains immutable private evidence for a save retry without a
new native command. Stable gateway IDs coalesce identical intent and conflict
on changed intent; accepted replay returns the saved snapshot even if native
settings subsequently change.

Native room changes have no request-ID lookup. After uncertain delivery,
default retries repeat the normalized patch within the stored send budget and
expose duplicate risk; omitted fields still preserve the then-current native
settings. Lookup attempts remain zero. `receipt_only` does not resend a
possibly accepted update. A later room read cannot prove this particular
mutation was accepted.

HTTP, framed TCP/TLS, WebSocket, Unix stream and stdio use the shared mutation
and journal path. Nonmessage live updates and DM reads remain unverified here. The complete
connection catalogue remains outstanding; other native operations are optional.

Native CHAT returns numeric 461 for failed room requests, including settings
capacity or save errors, without a distinct wire failure class. The gateway
preserves that code through the shared classifier; it cannot infer a confirmed
successful write from it. The SDK exposes decoded room fields and a terminator;
validation and byte accounting cover their canonical representation rather
than raw recipient fields or JSON extensions discarded by the SDK.

## Verification

Dated records follow. Where they mention principals, bindings, signed or
encrypted envelopes, signing keys or return routes, they describe the
[removed identity model](removed-identity.md), which no longer exists.

Isolated pinned native fixtures exercise bearer HTTP/1.1 and HTTP/2, signed
HTTP/2 with actual protocol assertions, encrypted HTTP/framed TCP/WebSocket,
framed TLS, Unix streams and real stdio subprocesses. Whole room snapshots
are compared with independent native queries. Tests cover both-field and
partial updates, zero/default-quota semantics, slow-mode 3600 and native history
limit 32, original-token accepted replay and ID conflicts. Native history 33
is admitted then rejected with code 461 without changing settings; revoked
operator authority is blocked with code 481, followed by successful recovery
under a new ID after the grant is restored. Invalid mutation/read selectors,
empty or negative patches, slow mode 3601 and unauthorized targets create no
journal job. Stdio restart preserves the original acknowledgement and single
send attempt after an intervening native update, and replay leaves that newer
native state intact.

A native retention control seeds five acknowledged messages, updates the room
quota to two, and verifies that only the latest two remain. Setting the quota
back to zero and adding three messages grows retained history to five without
discarding those two, exercising the settings' actual effect beyond their ACK.

All room transport race tests passed in 25.433s; final pre-admission controls
passed in 5.331s. Collector/authentication race checks passed in 1.759s and
1.374s, including missing/mismatched inner signed validity. Final room and
migration store controls passed in 3.219s after explicit outcome cleanup.
Journal tests exercise immutable failed-save retry, uncertain restart under
retry and receipt-only policies, both pointer fields, accepted restart,
retention and pairwise outcome exclusivity. Migration controls preserve older
canonical message/card/topic evidence. Vet/staticcheck/build/tidy/format checks
and the disabled-example CLI commands passed. The full serial race suite
(`go test -race -p 1 -timeout 20m ./... -count=1`) passed, including native
integration in 668.831s and store checks in 28.367s. After that frozen run,
test-only retention and whole typed topic migration assertions were added;
their focused race runs passed in 8.389s and 2.954s. Runtime evidence is Darwin; other platforms and the full
connection catalogue remain incomplete.

Initial test bugs included a missing result field, a missing fixture validity
gate, incorrect native operator acknowledgement assumptions and a wrong
expectation that native history 33 would be rejected before admission. They
were corrected before the final passing tests; those failed/intermediate runs
are not acceptance evidence. The initial nonoperator control expected rejected
rather than the journal's blocked state; its terminal failure was corrected and
the final transport race run passed.
