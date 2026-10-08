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

## Native packet owner prerequisite

`adapter.ListenIPv4Multicast` owns one membership on an explicitly named, up
interface. It requires destination and interface control messages and discards
packets unless both match the retained membership. The reported local endpoint
is a copy of the configured group, rather than the wildcard socket bind.
Writes require a nonzero-port IPv4 unicast destination; group and limited
broadcast writes are refused. Read/write deadlines and closing the native
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

These are transport socket checks. The owner is not yet connected to gateway
service startup; no multicast AIRC acceptance, protected bounded reading,
durable replay or independent native AIRC observation is claimed here.
