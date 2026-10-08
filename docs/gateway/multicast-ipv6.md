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

This is an endpoint prerequisite only: `Listener` has no IPv6 options field
yet, and the service does not activate IPv6 multicast. Protected route
configuration, actual socket membership/filtering, service activation and
real native AIRC qualification remain outstanding.
