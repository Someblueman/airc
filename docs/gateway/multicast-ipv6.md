# IPv6 multicast

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
selection never authenticate a sender.

The boundary test accepts every defined scope and configured upper bounds,
and rejects unicast, IPv4/mapped IPv4, permanent/unsupported flag formats,
reserved scopes, zero group identifiers, zones, noncanonical spelling,
invalid ports and missing/unsafe interfaces. The affected IPv4/IPv6 tests
passed (0.313s), with vet, staticcheck, formatting and whitespace checks
passing.

This endpoint prerequisite does not establish socket membership or native
AIRC acceptance. The protected configuration prerequisite below follows it.

## Protected return-route configuration prerequisite

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
link-local zones. Enabled IPv6 multicast still fails the service protocol
gate: socket ownership, activation and actual native qualification remain
on the roadmap.
