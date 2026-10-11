# Typing and thinking activity

`activity.set` sends a native typing or thinking signal through the shared
authorized journal and backend path. HTTP, framed TCP/TLS, WebSocket, Unix
streams and stdio use the same implementation. It supports exact room and
direct targets and requires permission for the operation and target.

An `activity` object has `kind` (`typing` or `thinking`), explicit `ttl_seconds`
(1–15), and optional `note` (at most 240 UTF-8 bytes under native BriefText rules).
The note is preserved without trimming. A target is required; parent IDs,
message/reaction bodies, read selectors and other mutation inputs are rejected.
Invalid or unauthorized intent creates no job. Request validity and the native
activity TTL are separate clocks.

The upstream session must negotiate ephemeral mode and `CHAT=1`. The backend
sends only native action, target, seconds and note. Native room posting authority,
including mute restrictions, remains authoritative. Native typing/thinking do
not require a retained parent; reply preparation/cancellation are separate native
operations that remain to be implemented.

## Acknowledgement meaning

The collector requires one bounded whole 777 record and the 778 terminator on
its exclusive session. It validates the exact target and authenticated actor,
account-ID syntax, actual typing/thinking kind, note and nonzero expiry. Mixed
message/profile/context/settings/poll fields, parent IDs, foreign targets,
duplicate records and malformed responses are rejected. The combined canonical
native response and typed acknowledgement each fit 4096 bytes. Write and ACK
phases have five- and ten-second limits under the original attempt deadline;
cancellation closes and joins the owned session.

Native activity is keyed by target and account and throttled for two seconds.
A second request can acknowledge the prior signal, including a different kind,
note and expiry. The gateway returns those actual values, without claiming the
new requested values were applied. Native expiry can elapse while the ACK is
in transit; a saved acknowledgement is historical evidence, not a guarantee that
the signal remains active. Native clocks are not assumed synchronized with the
gateway. The upstream's activity table is bounded and held in memory; native
signals are not chat messages or retained history.

`result.activity` contains version, operation, exact target, actual kind,
authenticated `from`, account ID, actual note and expiry. Durable accepted
results contain no new message ID and no persistence or recipient-connected
claim. The journal persists acceptance evidence, not native activity state.
Room readers and direct recipients can independently receive native 780
broadcasts; the sending session is excluded. Native throttling emits no new
broadcast. Forwarding these nonmessage events through gateway `watch` remains
required work and is not claimed by this write implementation.

## Retry and durable replay

Native activity has no request-ID receipt lookup; its lookup budget is zero.
The gateway still derives a stable request ID and retains exact intent. Default
bounded retries after uncertainty may refresh a signal or receive its throttled
predecessor; they expose duplicate risk. `receipt_only` does not send another
possibly accepted mutation. Definitive native validation/mute failures remain
permanent failures. An already accepted request replays its original whole ACK
without sending again, even after the signal expires or another native write
changes activity. Reusing its ID with changed intent is a conflict.

Schema 8 adds `activity_outcome` and six-way receipt/card/topic/room/pin/activity
exclusivity. Transactional migration preserves existing accepted evidence.
Private observed acceptance is immutable across save retries and fences ordinary
completion; close/reopen and retention follow the shared journal contract.

## Verification

Dated records follow. Where they mention principals, bindings, signed or
encrypted envelopes, signing keys or return routes, they describe the
[removed identity model](removed-identity.md), which no longer exists.

Native activity tests passed under the race detector in 55.382s, covering bearer
HTTP/1.1/HTTP/2, signed HTTP/2, encrypted HTTP/TCP/WebSocket, framed TLS, Unix and
real stdio processes. Independent native 780 records are compared with whole
saved ACKs. Controls cover cross-kind throttling, unthrottled fresh writes,
direct delivery, invalid/unauthorized intent, mute/unmute, original-token replay
after expiry and a newer native write, and accepted-but-unsaved journal recovery
under retry and receipt-only. Replay is checked after the native throttle ends,
so an accidental replay write cannot be hidden by that throttle.

Final full-package core/auth/backend/store race checks passed in
1.412s/2.001s/3.482s/27.566s. They include copied private intent, immutable
failed-save evidence and completion fencing, whole accepted restart/no-resend,
retention, pairwise INSERT/UPDATE exclusivity with positive controls, and
schemas 1–7 migration preserving canonical pin evidence. A new migration helper
initially used QueryRow on the owned SQL connection; the compile error was fixed
to QueryRowContext before these passing checks. Vet/staticcheck, tidy, formatting
and actual disabled-example CLI checks passed. Independent source review found
no must-fix defect. Both full-module suites passed: normal integration/store
512.061s/6.561s and race integration/store 537.591s/29.425s. Dependencies
and native source are unchanged; disposable fixtures leave the live daemon
and primary checkout outside their boundary. The whole catalogue
requires further evidence; other native operations are optional, and
nonmessage live forwarding and DM reads remain unverified here;
no adapter is complete.
