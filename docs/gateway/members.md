# Room membership snapshots

`members.read` is an implemented shared gateway operation.
No complete catalogue adapter is claimed.

The request is the gateway v1 envelope with `operation: "members.read"` and an
exact `target` of kind `room`. No cursor, query, limit, parent, message or
delivery policy is accepted. The principal and listener must both permit the
operation, and the principal must have permission for that exact room. Native
NAMES visibility is not an authorization substitute.

The response is `{"target":{"kind":"room","name":"#room"},"members":[]}`,
with each member carrying `nick` and `operator`. Nickname spelling and native
operator markers are preserved; the whole list is sorted by lowercase nickname.
Empty and unknown rooms return the same empty snapshot, matching native NAMES.
No room-existence inference, durable receipt or delivery job is fabricated.

The native collector uses an ephemeral, service-owned session and requests only
the authorized room. It validates every 353 chunk and requires the exact 366
completion. Duplicate nicknames, foreign rooms/recipients, invalid markers,
native failures and missing completion discard the snapshot. The result is
limited to 256 members and 48 KiB, with wire and encoded JSON budgets. Overflow
fails instead of returning a partial list. Existing read leases, cancellation,
pool release and shutdown ownership apply. No cursor key is needed, but reads
require the configured session reserve.

Bearer HTTP uses `GET /v1/members?kind=room&target=%23room` with strict query
validation. Signed/encrypted HTTP envelopes and framed TCP/TLS, WebSocket,
Unix and stdio carry the ordinary detached result under `members`. MCP HTTP
uses the `members.read` tool with the same request envelope. Capabilities expose
the actual permitted `members_targets`, `max_members` and `max_members_bytes`.

Hidden session membership is constrained by the pinned native protocol:
ephemeral sessions cannot join; a joined persistent session cannot become an
observer, and an observer cannot join. A real wire regression test verifies the
joined OBSERVE rejection, subsequent PART/OBSERVE, and observer JOIN rejection.
No native production code was changed for this increment. Public profile and
presence lookup remain the existing `directory` operation, not a new read alias.

Native race checks passed for encrypted HTTP/framed/WebSocket, signed and
bearer HTTP/2 with actual protocol assertions, encrypted Unix, signed/encrypted
stdio and public MCP SDK clients. Whole snapshots are compared to independent
ephemeral native NAMES queries without filtering members. Tests preserve
operator markers, verify hidden observers are absent, track membership changes
after PART, deny foreign rooms, preserve history and create no delivery jobs.
The combined carrier checks passed in 7.550s before adding the signed stdio
control; signed stdio, HTTP/MCP controls subsequently passed, and the final
encrypted Unix check passed in 4.956s. Unix's configured encrypted-only policy
is preserved. Initial test defects closed seed members too early and rejected
expected error frames; these were corrected before acceptance.

Core/config/backend/store/adapter race checks passed, including the 256-member
overflow, malformed and incomplete native responses, cancellation, cloned
one-use leases and durable revocation. Whole-module vet, staticcheck, tidiness
and diff checks passed. Actual disabled-example CLI validation and capability
discovery report thirty implemented operations without starting runtime. Full
normal/race module runs completed: all packages passed except the stale
29-operation CLI assertion. Integration passed in 458.721s/470.991s and journal
checks in 6.698s/32.388s. The full commands returned nonzero for that assertion;
after correcting it, the CLI normal/race suites passed in 0.441s/1.465s.
Final review then found the missing members CORS preflight route. It is fixed;
allowed-origin GET preflight and denied-origin/method cases pass in the whole
adapter race suite (3.126s). Fresh normal/race integration checks on this
corrected source pass in 458.351s/463.658s, with journal checks in
6.070s/29.718s. Both full commands exited successfully:
`go test ./... -count=1 -timeout=900s -p=1` and
`go test -race ./... -count=1 -timeout=900s -p=1`.

The full catalogue remains outstanding. Remaining native reads and nonmessage
live events are optional; DM reads remain unverified here.
