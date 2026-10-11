# Message actions, corrections and retractions

`message.action`, `message.correct` and `message.retract` use the existing
journal-backed mutation intake over HTTP, framed TCP/TLS, WebSocket, Unix
streams and stdio. This shared increment does not complete a catalogue adapter.

All require the operation and the room or direct target to be allowed by the
listener; AIRC decides whether the author may act on the message. Action takes a nonblank `message` without a parent.
Correction takes canonical lowercase `parent_id` and a nonblank replacement `message`. Retraction
takes canonical lowercase `parent_id` and an optional reason in `message`; only an empty string
selects native AIRC's default body `Retracted`. Whitespace-only text is invalid.
Bodies normalize newlines and fit 4096 UTF-8 bytes. The native server still
applies its configured body limit, author/admin ownership, mute and slow mode.

Correction/retraction cannot use the permitted target to redirect a foreign
parent. On the same authenticated native session, room proof first checks
bounded pins, supporting originals evicted from ordinary history, then retained
context. It proves the original ID, exact room and thread root before writing.
Direct proof reads the caller's current native directory card and retained
context, matching the original's stable sender account ID and exact destination.
This permits only the caller's own direct originals. It does not establish
recipient account identity or enable gateway DM history/search/watch; that
separate prerequisite remains in [dm-read-boundary.md](dm-read-boundary.md).
Proof has a five-second deadline, closes and joins cancellation, and cannot
write after the attempt expires.

The exclusive session must observe native `762` receipt, matching `777` whole
message metadata and `778` terminator, in order, within bounded collection.
Decoded known fields and terminator are accounted against a 64 KiB compound
wire budget; the SDK line ceiling also applies. Unknown extensions discarded by
the SDK are not counted as retained decoded fields. The new message must carry
the correct native kind (`action`, `correct`, `retract`), authenticated author,
canonical account ID, nonzero sequence, body, ID and timestamp. Correction and
retraction carry `supersedes` and `reply_to` equal to the original ID and the
proven thread root. The new retract message has `retracted: false`; AIRC marks
the original message or pin retracted and sets its `superseded_by` to the new ID.
Incomplete or contradictory responses never become accepted. A numeric error
after partial native evidence preserves uncertainty, possible posting and its
native numeric code.

The journal retains the complete ordinary native receipt with no new schema or
alternate outcome. Public results preserve its message ID, accepted/persisted
facts and actual direct recipient connectivity. Room recipient connectivity
remains unknown. False persistence is valid native evidence, not invented
success. Replay returns the original saved facts after later changes and process
restart; it never corrects or retracts again.

Native CHAT posts have no request-ID lookup or idempotency field. These operations
therefore use zero lookup attempts and retain the gateway's default bounded
retry policy. After uncertain delivery, retry can create another native post
under the same gateway request ID, exposing duplicate risk. Receipt-only policy
preserves uncertainty without resending.

## Verification

Dated records follow. Where they mention principals, bindings, signed or
encrypted envelopes, signing keys or return routes, they describe the
[removed identity model](removed-identity.md), which no longer exists.

Real pinned-native race controls cover bearer HTTP/1.1 and HTTP/2, signed HTTP/2,
encrypted HTTP, framed TCP, WebSocket, Unix streams, stdio and framed TLS. They
compare whole new message metadata with an independent native observer and
fresh gateway history, verify foreign-room rejection before writing, pinned
original annotations after history eviction, online direct author proof and
non-author rejection, historical replay and stdio restart. The corrected broad
native run passed room/direct/HTTP/encrypted/recovery cases; Unix/stdio replay
and framed TLS passed a focused rerun in 10.651s after the test fixed explicit
signed validity. Separate offline direct and native mute/unmute controls passed
in 8.104s. All three operations preserve possible duplicates after a lost ACK
under default retry, and do not resend under receipt-only policy.

The initial native run exposed a room-only early guard, incorrect retract
metadata validation and failure to prove an evicted pinned original. These
were fixed before acceptance. An earlier compile attempt ran during backend
implementation and failed on an unused import; it is not acceptance evidence.
Collector controls require observed native writes and cover incomplete,
reordered, duplicate, malformed, mismatched and contradictory compound ACKs.
Stable-author proof controls include old sender nicknames, account replacement
and foreign destinations. Final whole-backend race checks passed in 3.822s. Review tightened only the new
operations to canonical lowercase parent/receipt/thread IDs; final focused
core/auth/backend/store race checks passed in 1.365s/1.406s/1.573s/2.455s,
and the complete native mutation race controls passed in 36.710s. Whole
core/auth regression checks after that guard passed in 1.222s/1.742s.
Core/auth/config/store race checks passed in 1.258s/1.639s/1.305s/34.829s.
Store controls cover all three whole receipts across close/reopen, false native
persistence and private receipt recovery/completion fencing after a failed save.

Real Chrome WebCrypto/WebSocket acceptance passed in 20.951s, independently
comparing all three new native messages and original retraction annotation,
while preserving poll/reply-signal, history/search/directory/live-watch, tamper
and abort controls. Vet, staticcheck, tidy, formatting, disabled configuration
validation and the actual CLI's 29-operation capabilities passed. Initial full
module normal/race runs failed integration in 387.458s/404.894s:
the shared conformance assertion still expected 26 operations. It now expects
the actual 29. Framed TCP/TLS, WebSocket and stdio conformance passed in
33.486s, and Unix conformance passed in 9.626s. Final `go test ./... -count=1
-timeout=900s -p=1` and the corresponding `-race` run both passed, including
integration in 422.955s/439.527s and store in 6.533s/33.580s.
Linux runtime and full catalogue coverage remain
pending; no complete adapter is claimed.
