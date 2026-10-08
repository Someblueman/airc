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
