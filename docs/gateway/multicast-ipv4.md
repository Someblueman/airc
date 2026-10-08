# IPv4 multicast

The private endpoint validator requires a canonical literal group in
`239.0.0.0/8`, a decimal port from 1 through 65535, and one explicit interface
name of at most 64 bytes without whitespace, path separators or NUL. DNS,
unicast, IPv6 and link-control multicast endpoints are rejected.

The administrative address range follows [RFC 2365](https://www.rfc-editor.org/rfc/rfc2365).
Choosing an address does not configure a network boundary or authorize a
sender. Operators must allocate a group appropriate to their network scope.

Listener configuration accepts `ipv4_multicast` options. Validation checks
configuration shape; service startup checks interface availability and joins
the group. Native AIRC send/read qualification remains on the roadmap.

An enabled listener must name ingress recipient keys and a response signing
key. Every principal must have a signing key and exactly one protected return
route for that listener. Routes require a recipient key ID, an absolute public
key path and a canonical literal IPv4 unicast address with a nonzero decimal
port. Multicast, unspecified, limited broadcast, IPv6 and DNS return endpoints
are rejected. Listener TLS and browser origins are prohibited. Reply destinations
are configuration inputs; no packet source or request field selects a route.

`TestIPv4MulticastConfigurationBoundary` exercises accepted endpoint and
interface bounds, malformed addresses and ports, prohibited address families
and groups, and missing or malformed interface names. The config package test,
vet and staticcheck gates passed for this prerequisite.
`TestIPv4MulticastProtectedRoutePrerequisite` accepts the required protected
configuration. `TestIPv4MulticastRejectsUnsafeProtectedRoutes`
checks key, signer, route, endpoint and options boundaries.

## Native packet owner prerequisite

`adapter.ListenIPv4Multicast` owns one membership on an explicitly named, up
interface. It requires destination and interface control messages and discards
packets unless both match the retained membership. The reported local endpoint
is a copy of the configured group, rather than the wildcard socket bind.
Writes require a nonzero-port IPv4 unicast destination; group and limited
broadcast writes are refused. Native broadcast permission is disabled before
the writer is exposed; [the corrective native checks](multicast-ipv4-broadcast-testing.md)
cover directed broadcast protection. Read/write deadlines and closing the native
socket remain available through `net.PacketConn`.

Four actual socket tests passed three times on Darwin ARM64 (normal 0.617s,
race 1.720s) and Linux ARM64 in the existing pinned Ubuntu fixture
`sha256:33ceb71981b602c1a7443a53469e4dba065f7503eab3078a2d7a57a2ab987517`.
The tests cover an independently joined second group on the same port, unicast
ingress refusal, matching-group payload and source delivery, a unicast reply,
unsafe reply refusal, retained-address copying, filtered-read deadlines and
close releasing a blocked reader. Invalid groups and unavailable interfaces
fail construction. Darwin and Linux vet, Darwin staticcheck and whitespace
checks passed. Linux logs and binary provenance are retained in
`/tmp/airc-ipv4-multicast-1cb3069`.

These are transport socket checks; no multicast AIRC message acceptance,
protected bounded history or durable replay is claimed here.

## Service activation acceptance

Service startup now connects the socket owner to the existing encrypted AGD1
packet dispatcher. It reports the configured group in Ready, sends responses
only through the configured protected route, and closes and joins the packet
owner during shutdown. The CLI reports enabled multicast capabilities without
starting listeners for its read-only commands.

`TestIPv4MulticastServiceStartupAndProtectedCapabilities` passed against the
actual isolated native AIRC fixture on Darwin ARM64 (normal 7.611s, race
15.798s) and Linux ARM64 (0.35s test time). It checks the Ready group, read-only
CLI commands, encrypted fixed-mailbox capabilities, source silence and no
durable send job, with joined shutdown registered by the shared fixture.
Linux startup was rerun after the broadcast fix with rebuilt gateway and test
binaries. Logs and digest provenance are in
`/tmp/airc-ipv4-multicast-1cb3069/startup-broadcast.log` and
`startup-broadcast-provenance.json`.
Full normal/race and build, formatting, vet, staticcheck and whitespace acceptance
passed on frozen source
`40d2ff20574764db50d89eea9ae3887b5fc6ca63ab4dc868c7067e38398d03af`.
The [exact acceptance record](testing.md#ipv4-multicast-service-activation-acceptance)
retains commands, counts and raw provenance. This completes service activation;
multicast message acceptance, bounded history, replay, refusal and pressure
qualification remain on the roadmap.

## Native Unicode, cursor and replay prerequisite

`TestIPv4MulticastNativeUnicodeCursorAndReplay` sends two encrypted requests
through the actual configured group. An independent native AIRC observer
requires exact author, target and whole Unicode body. The larger request spans
multiple 1000-byte AGD1 fragments, sent in reverse order. The test verifies and
discards its actual protected durable response, retries the retained request
ID and validity interval, and requires one send attempt and two total native
messages rather than a duplicate. It then collects whole one-record protected
history pages, retaining explicit cursors through the empty caught-up page,
and compares all metadata with an independent native history read.

The test passed on Darwin ARM64 (normal 4.162s, race 7.047s), with vet,
staticcheck and whitespace checks passing. Linux ARM64 cross-compilation and
vet passed; the actual test also passed in the existing isolated pinned Ubuntu
fixture. Logs and digest/command provenance are in
`/tmp/airc-ipv4-multicast-1cb3069/unicode.log` and
`unicode-provenance.json`.

This completes the send/history/replay prerequisite. Authentication refusal,
group/source response silence, resource pressure and final integrated native
qualification remain open. The service activation full acceptance above
precedes this additional test source and does not certify the final
qualification checkpoint.

## Native refusal and reply reflection prerequisite

`TestIPv4MulticastUnverifiedInputHasNoResponseOrNativeIntake` uses the actual
group and shared UDP refusal controls. Signed-only requests, tampered encrypted
proofs, another realm, denied targets, JSON destination overrides, 1001-byte
datagrams, incomplete fragment sets and an unknown signing key produce no
source or fixed-mailbox output, no durable jobs and no independent native
history. A valid encrypted capabilities request succeeds afterward through
the fixed mailbox while the source remains silent. The original UDP test uses
the same helper and retains all earlier cases, with unknown-signer and partial
fragment coverage added.

`TestIPv4MulticastRepliesNeverReturnToGroupOrSource` joins an independent
second socket to the same group. It requires an authenticated capabilities
result at the fixed unicast mailbox, observes every original request fragment
exactly once on the group socket, then requires group and source silence.
This proves the positive response route without treating incoming request
packets as reflected responses.

The multicast refusals, group reflection test and shared UDP regression passed
on Darwin ARM64 (combined normal 10.169s, race 15.840s), with Darwin vet,
staticcheck and whitespace checks passing. Linux ARM64 compilation and vet
passed, then the actual three tests passed in the existing isolated pinned
Ubuntu fixture (2.86s, 0.35s and 2.67s respectively). Raw logs and binary/command
provenance are in `/tmp/airc-ipv4-multicast-1cb3069/boundary.log` and
`boundary-provenance.json`. Resource-pressure and final integrated native
qualification remain on the roadmap.

## Native capacity and joined shutdown prerequisite

The multicast capacity test seeds nine 4000-byte messages through a native
AIRC author. An oversized protected history request returns encrypted
`capacity_exceeded`; one-record retries recover every whole native record,
with explicit status and cursor boundaries through an empty caught-up page.
All metadata must match an independent native history read, with no send jobs.
The original UDP capacity test shares these strengthened assertions.

The multicast shutdown test establishes an active native watch, submits only
the first fragment of a valid encrypted send request, then sends SIGINT. The
gateway must join within two seconds, release the native observer lease and
allow an exclusive wildcard UDP4 bind on its former port. No send job is
created. The original UDP shutdown test shares these assertions and a bounded
fragment write deadline.

All four affected tests passed on Darwin ARM64 (combined normal 4.495s, race
11.306s); vet, staticcheck, formatting and whitespace checks passed. Linux
ARM64 compilation and vet passed. The actual isolated Linux fixture then
passed all six multicast tests together with the three affected UDP
regressions, including multicast capacity (0.08s) and shutdown (0.04s). Raw
logs and binary/command provenance are in
`/tmp/airc-ipv4-multicast-1cb3069/capacity-integrated.log` and
`capacity-integrated-provenance.json`. Linux execution uses the unchanged
production gateway binary, digest
`4c21b4b2bc4bddea38215ac7af2f12d5d97b1fee8e8cde0f25e2de8505338de8`.

This completes the capacity/shutdown prerequisite. Final integrated native
qualification and full acceptance on the resulting test source remain open.
