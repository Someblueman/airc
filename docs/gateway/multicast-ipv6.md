# IPv6 multicast

Enable a listener with `protocol: "ipv6_multicast"`, a canonical group address
such as `[ff12::1234]:9001`, and `ipv6_multicast: {"interface": "en0"}` using
an interface and group allocated for your network. Run it through the ordinary
`airc-gateway serve --file CONFIG` command. The listener carries plain author
requests in [datagram fragments](author-network-carriers.md#fragments), as in
the [UDP packet profile](udp.md); a client sends to the group with the
explicit interface zone and reads the reply sent by unicast to its source,
within the [amplification bound](author-network-carriers.md). The ready record
includes that interface zone. Darwin ARM64 native send/read behavior was
qualified before the [identity removal](removed-identity.md); Linux runtime
qualification remains outstanding.

## Endpoint configuration prerequisite

The private configuration package now defines `IPv6Multicast` options with an
explicit interface and endpoint validator. The group must be a canonical
bracketed IPv6 literal with a nonzero group identifier, a plain transient
flag and one of the defined scopes 1, 2, 3, 4, 5, 8 or 14.
[RFC 4291](https://www.rfc-editor.org/rfc/rfc4291.html) defines multicast
flags; [RFC 7346](https://www.rfc-editor.org/rfc/rfc7346.html) defines scopes.
Permanent/control groups and embedded-prefix/rendezvous formats are outside
this initial endpoint contract. A canonical decimal port from 1 through
65535 is required. The explicit interface supplies the membership zone; an
address zone cannot override it. Names must be nonempty, at most 64 bytes
and contain no whitespace, path separators or NUL. Group scope and interface
selection never select an author.

The boundary test accepts every defined scope and configured upper bounds,
and rejects unicast, IPv4/mapped IPv4, permanent/unsupported flag formats,
reserved scopes, zero group identifiers, zones, noncanonical spelling,
invalid ports and missing/unsafe interfaces. The affected IPv4/IPv6 tests
passed (0.313s), with vet, staticcheck, formatting and whitespace checks
passing.

This endpoint prerequisite does not establish socket membership or native
AIRC acceptance. The protected configuration prerequisite below follows it.

## Protected return-route configuration prerequisite

Dated records follow. Where they mention principals, bindings, signed or
encrypted envelopes, signing keys or return routes, they describe the
[removed identity model](removed-identity.md), which no longer exists.

`Listener.ipv6_multicast` now carries the explicit interface options. Disabled
listeners validate their endpoint and protected route shapes. Enabled
configuration requires ingress recipient keys, a response signing key and
signing principals, each with exactly one protected route for this listener.
The route must carry a recipient key ID and absolute public-key file. The
existing global validation retains known-principal/listener, duplicate-ID,
binding and operation checks. TLS and browser origin options are rejected.

Return destinations must be canonical numeric IPv6 unicast endpoints with a
nonzero canonical port. IPv4/mapped addresses, unspecified/multicast hosts
and hostname destinations are rejected. Link-local unicast requires exactly
the configured listener interface as its zone; non-link-local addresses
cannot carry a zone. A sender cannot select a response destination.

The full configuration package passed normally (1.082s) and under race
(4.248s); vet, staticcheck, formatting and whitespace checks passed. Tests
cover disabled configuration, missing credentials/routes, duplicate routes,
unknown principals/listeners, separately routed principals and safe/unsafe
link-local zones. At this prerequisite stage, those checks did not establish
socket ownership, service activation or actual native qualification.

## Socket prerequisite under verification

The IPv6 owner joins one explicit group/interface and requires
ancillary destination/interface matches before exposing a packet. It rejects
multicast, unspecified, IPv4/mapped and invalid-port reply destinations;
link-local replies require the retained interface zone, while other replies
reject zones. Membership addresses are copied, reads preserve deadlines and
close joins blocked reads. The service exposes the configured group with its
explicit interface zone.

Repeated actual Darwin ARM64 socket controls passed normally (0.596s) and
under race (1.725s), together with the existing IPv4 controls. Vet, staticcheck,
formatting, whitespace checks and Linux ARM64 compilation/vet passed.
The existing isolated Linux fixture passed membership refusal, filtered-read
deadline and blocked-read close controls, but failed group delivery with
`network is unreachable`. Its only IPv6 address is `::1`, IPv6 is enabled,
and its route table lacks a multicast route. The failed run is retained in
`/tmp/airc-ipv4-multicast-1cb3069/ipv6-adapter.log` and
`ipv6-adapter-provenance.json`; it does not qualify Linux delivery.

A temporary Linux fixture route helper is prepared under
`/tmp/airc-ipv6-route-fixture`. It would install `ff00::/8` on loopback in
the isolated network namespace, clear effective/permitted/inheritable
capabilities and verify zero ambient capabilities before executing tests.
It fails closed under the existing zero-capability fixture policy. Running
route setup with NET_ADMIN awaits explicit owner authorization; no expanded
fixture capability or host route has been applied. Linux socket delivery and
Linux native AIRC qualification remain unfinished.

## Native gateway qualification on Darwin

The enabled IPv6 multicast service passed its real gateway integration suite
on Darwin ARM64. An independent native AIRC observer confirmed exact author,
room, and body for Unicode and fragmented sends. The tests replayed a request
after discarding its first durable result, compared complete cursor history
with the native observer, exercised bounded-history capacity and resume,
rejected invalid and sender-selected response destinations without native
intake or reflection, and confirmed active-watch plus incomplete-request
workers join on SIGINT. A second UDP socket joined the configured multicast
group during a valid request and observed only the original request fragments;
the protected result arrived through the configured unicast return mailbox.

`go test ./integration/gateway -run 'IPv6Multicast' -count=1` passed (8.959s)
and the same command with `-race` passed (25.074s). The adapter socket controls
passed three normal runs (0.583s) and two race runs (1.676s); adapter vet and
formatting checks passed. Linux ARM64 test binaries compile for both the
integration and adapter packages. Linux runtime delivery remains unqualified
because the isolated fixture has no IPv6 multicast route; no route or
capability was changed for this acceptance.
