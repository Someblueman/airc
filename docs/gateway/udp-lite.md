# UDP-Lite

## Endpoint and integrity policy prerequisite

`UDPLite` configuration options require an explicit `checksum_coverage` value
of `full`. Missing, partial, header-only and differently spelled policies
are rejected. [RFC 3828](https://www.rfc-editor.org/rfc/rfc3828.html) defines
wire checksum coverage zero as protection of the entire UDP-Lite packet.
Socket ownership must set and verify full send/receive coverage; transport
checksums do not authenticate senders or replace protected framing.

Endpoints are canonical numeric IPv4 or IPv6 addresses with canonical
decimal ports from 0 through 65535. Zero supports listener allocation, not
a return destination. Hostnames, multicast, limited broadcast, mapped IPv4
and noncanonical spellings are rejected. IPv6 link-local requires an
explicit bounded interface zone; non-link-local addresses reject zones.
IPv4 link-local addresses remain valid numeric endpoints.

The configuration test covers both address families, wildcard allocation,
upper bounds, link-local scope and unsafe endpoints/policies. Full package
tests, vet, staticcheck, formatting and whitespace checks passed. This
options validator does not itself activate a listener. The following sections
record protected route and Linux socket/checksum prerequisites. Native AIRC
service qualification remains on the roadmap.

## Protected return-route configuration prerequisite

`Listener.udp_lite` now carries the explicit coverage policy. Disabled
listeners validate endpoint and route shapes. Enabled configuration requires
ingress recipient keys, response signing credentials and signing principals,
each with exactly one protected route for the listener. Recipient key IDs
and absolute public-key paths are mandatory. TLS/origin options are rejected.
Existing global checks preserve known principal/listener identities, binding
and operation scopes and duplicate-ID refusal.

Reply addresses must be non-unspecified unicast in the listener's address
family, with nonzero ports. IPv6 link-local return zones must match the
listener's explicit zone. The same canonical endpoint and full-coverage
policy validation applies to routes; wildcard/port-zero listener allocation
cannot become a reply destination. Senders cannot supply their own route.

The full configuration package passed normally (1.643s) and under race
(1.996s), with vet, staticcheck, formatting and whitespace checks passing.
Tests cover missing/duplicate routes and credentials, unsafe addresses,
known-principal scoping, separate routes for additional principals, IPv6
zone/family boundaries and disabled configuration. At that prerequisite checkpoint, enabled UDP-Lite still failed the service
protocol gate. The following sections record socket ownership and activation;
complete native qualification remains open.

## Linux native socket ownership prerequisite

`ListenUDPLite` creates a Linux protocol-136 datagram socket, with close-on-exec
and full send/receive checksum coverage explicitly set and read back as zero.
It disables broadcast writes and pins IPv6-only sockets to their address
family. Scoped IPv6 binds resolve the named interface. The descriptor passes
to Go packet ownership through `net.FilePacketConn`; all construction errors
close ownership, and unsupported platforms/kernels return an error without
UDP fallback. Replies require validated same-family unicast destinations.
Read/write deadlines and close delegate to the owned packet connection.

The actual existing isolated Linux ARM64 fixture passed all three native
controls three times. They verify SO_PROTOCOL=136, broadcast permission zero,
IPv4/IPv6 partial-coverage packet refusal followed by whole Unicode delivery
and replies, 1001-byte bounded reads of oversized packets, joined blocked
reads on close and unsafe endpoint/reply refusal. Raw logs and command/binary
provenance are retained in
`/tmp/airc-ipv4-multicast-1cb3069/udp-lite-final.log` and
`udp-lite-final-provenance.json`. The fixture retained network-none, read-only,
no-new-privileges and zero capabilities; no dependency installation occurred.
[Linux kernel socket documentation](https://docs.kernel.org/networking/udplite.html)
specifies the send/receive coverage settings checked by these actual controls.

Darwin unsupported-platform refusal passed normally (0.520s) and under race
(1.546s); these are refusal tests, not Darwin UDP-Lite delivery. Darwin vet,
staticcheck, formatting/whitespace checks and Linux compilation/vet passed.
Linux race and other architectures have no runtime claim. This completes the
raw socket/checksum prerequisite only. The following section records service activation. Authenticated fragments and
actual native AIRC delivery/read/replay/refusal/capacity/shutdown remain open.

## Linux service activation and protected startup

Enabled Linux listeners now own UDP-Lite sockets through shared protected packet
dispatch. Configuration rejects enabled listeners on unsupported platforms. CLI
capabilities expose the protocol, and fixed protected return routes retain
principal, binding, realm and recipient scope. Independent native protocol-136
clients supply the integration source and mailbox sockets.

The existing isolated Linux ARM64 AIRC fixture passed service startup and
encrypted capabilities (0.32 seconds), with source silence and no durable send
jobs. Its gateway SHA-256 was
`13534996db3f9deeac2f67a760b2970c1501708a31daf8f8f6e6db20a4f1d549`.
Logs and exact command/binary provenance are retained under
`/tmp/airc-ipv4-multicast-1cb3069/udp-lite-startup.*`. Actual Linux configuration
controls also passed (`udp-lite-config.log` and its provenance). The fixture
retained zero capabilities and network-none; no installation occurred.

Full Darwin normal/race and static acceptance passed; see
[activation acceptance](testing.md#udp-lite-service-activation-acceptance).
Linux-only service startup is separate from Darwin unsupported-platform tests.
This closes startup activation only; whole native send/cursor/replay, protocol
refusals, capacity and joined shutdown qualification remain on the roadmap.

## Native Unicode, cursor and retained replay prerequisite

The actual isolated Linux ARM64 AIRC fixture passed
`TestUDPLiteNativeUnicodeCursorAndReplay` (0.92 seconds). Independent native
protocol-136 clients sent Unicode and a multi-fragment message in reverse
fragment order. After discarding the actual durable response, retrying retained
intent produced one accepted native message with one send attempt. The joined
independent observer verified exact bodies, authors and targets; native history
contained exactly two messages. Protected one-message cursor pages matched
complete independent native metadata and ended with an empty caught-up page.
Logs and binary/command provenance are retained in
`/tmp/airc-ipv4-multicast-1cb3069/udp-lite-native.log` and
`udp-lite-native-provenance.json`, using the unchanged activation gateway.

The shared IPv4 multicast control passed normally (4.766 seconds) and under
race (6.931 seconds). Integration vet, staticcheck, Linux compilation and diff
checks passed. Linux race is not claimed. This closes only the native
send/cursor/replay prerequisite; protocol refusals, capacity recovery, joined
shutdown and final integrated acceptance remain open.

## Native refusal and full-checksum admission prerequisite

The actual isolated Linux ARM64 AIRC fixture passed eight unverified-input
controls (3.10 seconds): signed-only, tampered encryption, wrong realm, denied
target, destination override, oversized datagram, incomplete fragments and an
unknown signing sender. Each kept source/mailbox silent and admitted no durable
jobs; independent native history remained empty. A subsequent authenticated
request succeeded through the fixed encrypted mailbox route.

A separately valid protected send used native send coverage eight, set and
read back on the client descriptor. Full-coverage ingress rejected it without
response, durable jobs or native history. Restoring native coverage zero
recovered protected capabilities, while the source stayed silent (0.51 seconds).
Logs and exact binary/command provenance are under
`/tmp/airc-ipv4-multicast-1cb3069/udp-lite-boundary.log` and
`udp-lite-boundary-provenance.json`. The existing zero-capability fixture and
activation gateway were unchanged. Linux integration compilation, vet,
staticcheck and diff checks passed; staticcheck required access to its existing
macOS cache. No Linux race instrumentation is claimed. Capacity recovery, joined
shutdown and final integrated acceptance remain open.

## Native capacity and joined shutdown prerequisite

The actual isolated Linux ARM64 AIRC fixture passed capacity recovery
(0.15 seconds) and joined shutdown (0.03 seconds). Nine native 4,000-byte
messages exceeded the aggregate response budget, producing encrypted capacity
refusal. One-message cursor pages then matched whole independent native
metadata, without gaps or partial messages, ending in a caught-up empty page.

An active watch acquired a native observer lease while an incomplete send
fragment remained pending. SIGINT joined the process within two seconds,
returned the native observer count to baseline and left no durable send jobs.
A fresh independently created protocol-136 socket bound the listener port,
verifying UDP-Lite port release rather than a different transport's port.
Logs and exact command/binary provenance are retained under
`/tmp/airc-ipv4-multicast-1cb3069/udp-lite-capacity.log` and
`udp-lite-capacity-provenance.json`, using the unchanged activation gateway and
zero-capability isolated fixture.

Shared ordinary UDP/multicast shutdown controls passed normally (4.048 seconds)
and under race (7.883 seconds); Linux compilation, vet, staticcheck and diff
checks passed. No Linux race claim is made. Final integrated Linux controls and
full normal/race/static acceptance remain required before closing the broad
UDP-Lite roadmap item.
