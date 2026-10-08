# IPv4 multicast

The private endpoint validator requires a canonical literal group in
`239.0.0.0/8`, a decimal port from 1 through 65535, and one explicit interface
name of at most 64 bytes without whitespace, path separators or NUL. DNS,
unicast, IPv6 and link-control multicast endpoints are rejected.

The administrative address range follows [RFC 2365](https://www.rfc-editor.org/rfc/rfc2365).
Choosing an address does not configure a network boundary or authorize a
sender. Operators must allocate a group appropriate to their network scope.

This is a configuration prerequisite only. Public listener configuration still
rejects this protocol; the validator does not inspect interface availability or
join a group. Protected fixed unicast return routes, socket ownership and
native send/read qualification remain on the roadmap.

`TestIPv4MulticastConfigurationBoundary` exercises accepted endpoint and
interface bounds, malformed addresses and ports, prohibited address families
and groups, and missing or malformed interface names. The config package test,
vet and staticcheck gates passed for this prerequisite.
