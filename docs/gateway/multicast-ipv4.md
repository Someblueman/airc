# IPv4 multicast

The private endpoint validator requires a canonical literal group in
`239.0.0.0/8`, a decimal port from 1 through 65535, and one explicit interface
name of at most 64 bytes without whitespace, path separators or NUL. DNS,
unicast, IPv6 and link-control multicast endpoints are rejected.

The administrative address range follows [RFC 2365](https://www.rfc-editor.org/rfc/rfc2365).
Choosing an address does not configure a network boundary or authorize a
sender. Operators must allocate a group appropriate to their network scope.

This is a configuration prerequisite only. Disabled listener configuration
accepts `ipv4_multicast` options; enabled listeners still fail with a protocol
implementation error. Validation does not inspect interface availability or
join a group. Socket ownership and native send/read qualification remain on
the roadmap.

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
`TestIPv4MulticastProtectedRoutePrerequisite` retains the enabled-service gate
and accepts disabled configuration. `TestIPv4MulticastRejectsUnsafeProtectedRoutes`
checks key, signer, route, endpoint and options boundaries.
