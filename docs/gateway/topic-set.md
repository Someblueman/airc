# Room topic mutations

`topic.set` changes the topic of one authorized room, or clears it with an
explicit empty string. HTTP, framed TCP/TLS, WebSocket, Unix stream and stdio
use the shared mutation dispatcher and journal.

```json
{"version":1,"request_id":"topic-set-1","operation":"topic.set","target":{"kind":"room","name":"#room"},"topic":"Current discussion"}
```

The `topic` field is required, fits 400 UTF-8 bytes without controls, and is
trimmed like the native command. An omitted field is invalid; `"topic":""`
clears the header. No message, parent, profile, presence or read selectors
apply. Mutation validity and `delivery_policy` follow the existing contract.
The listener must permit this operation and the room, and AIRC must permit
the author to post in that room;
setting a topic does not require channel-operator privilege.

One exclusive native session sends `TOPIC` with an explicit trailing field.
A populated acknowledgement requires the matching topic and native numeric 333
setter/time metadata. Its target and normalized text must match the request,
and the setter must match the author. A clear requires
the native no-topic response and has no invented setter or timestamp. The
whole typed acknowledgement is bounded to 4 KiB. Native `TOPIC` is a baseline
command and does not require message-receipt or CHAT feature negotiation.

The accepted result carries a `topic` acknowledgement containing `version:1`,
`operation:"topic.set"` and the whole native `TopicResult`. It reports
`accepted:true`, while `persisted` and `recipient_connected` remain null and
`message_id` is absent. Native topic persistence failures are logged without a
failure acknowledgement, so the gateway cannot infer disk persistence from
success. Topic responses also contain no setter account ID.

The acknowledgement is stored independently of message receipts and card
outcomes. Schema 5 migrates older journals transactionally. Repeated admission
with the same normalized intent returns the original result and setter/time;
changed intent under the same ID conflicts. A failed journal save retains a
private copy for a later save without repeating the command.

Native topics have no stable request-ID lookup. A write followed by disconnect
can leave acceptance uncertain. Default retries repeat the same normalized
intent within the stored send budget and expose duplicate risk; rewriting the
same topic can refresh its setter/time. Lookup attempts remain zero.
`receipt_only` does not resend after possible acceptance, and a later topic
query is not evidence that this particular mutation succeeded.

The pinned SDK collapses raw numeric 331/332 into topic events and discards
recipient/parameter structure. Validation covers the exposed fields; raw 333
metadata remains available and is validated directly. Native topic broadcasts
are distinct from the setter acknowledgement and do not prove acceptance.
Nonmessage live updates remain unverified here. The full connection catalogue
remains outstanding; other native operations are optional.

## Verification

Dated records follow. Where they mention principals, bindings, signed or
encrypted envelopes, signing keys or return routes, they describe the
[removed identity model](removed-identity.md), which no longer exists.

Isolated pinned native fixtures exercise HTTP/1 bearer, signed HTTP/2 with an
actual protocol assertion, encrypted HTTP/framed TCP/WebSocket, framed TLS,
Unix streams and real stdio subprocesses. Whole acknowledgements are compared
with independent native queries. Coverage includes set/clear, trim normalization,
the valid 400-byte escaped-text boundary, rejected oversized/control/foreign
fields and unauthorized rooms without admission, and stable-ID conflicts.
Stdio restart replays the original authenticated token after an intervening
native update: the stored whole setter/time remains unchanged, send attempts
stay one, and the newer native topic is preserved.

Collector controls cover broadcasts before the private acknowledgement,
foreign/duplicate/missing metadata, malformed timestamps, typed permanent
numeric errors and cancellation under continuous unrelated traffic. Signed and
encrypted validity controls reject missing or mismatched inner timestamps.
Journal tests cover private pointer isolation, immutable failed-save retry,
restart uncertainty under retry and receipt-only policies, zero lookup budget,
retention and pairwise evidence exclusivity. Version 1–4 migration controls
preserve old canonical message receipts and whole version-4 card bytes/facts.

Final focused HTTP/encrypted transport race checks passed in 11.314s; stdio,
Unix and framed-TLS race checks passed in 18.071s. Collector race checks passed
in 1.610s. The store/delivery race command passed, with store in 21.256s.
Initial IPC tests lacked the fixture's new signed-mutation validity gate and
were rejected; the shared gate was corrected before passing checks. Those
failed runs are not acceptance evidence. Runtime evidence is from Darwin;
other platforms and full catalogue acceptance remain pending.

The serial full-module command `go test -race -p 1 -timeout 20m ./... -count=1`
returned failure because the shared framed/WebSocket/Unix/stdio conformance
assertion still expected sixteen operations. Gateway integration ran for
567.198s; its other tests passed, as did all remaining packages, including the
native CLI in 54.173s and store in 20.701s. The assertion now expects seventeen.
That broad command compiled before a duplicate acceptance guard was removed;
final topic/migration/exclusivity store race checks passed afterward in 3.419s.
The broad command is not recorded as green.

After correcting the assertion, all affected framed TCP/TLS, WebSocket, Unix
and stdio real conformance race tests passed in 58.243s. The broad command was
not repeated because the only subsequent change was that shared test expectation.
