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
standalone options validator does not activate a listener. Protected route
configuration, Linux socket ownership, actual checksum boundaries and native
AIRC service qualification remain on the roadmap.

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
zone/family boundaries and disabled configuration. Enabled UDP-Lite still
fails the service protocol gate. Socket/checksum behavior, activation and
actual native qualification remain unfinished.
