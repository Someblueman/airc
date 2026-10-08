# IPv4 broadcast

## Endpoint configuration prerequisite

The configuration package defines `IPv4Broadcast` options with an explicit
interface and canonical IPv4 subnet. The endpoint must be either limited
broadcast (`255.255.255.255`) or that subnet's directed broadcast address,
computed by setting its host bits to one. The subnet must have 1 through
30 prefix bits, no host bits and a non-unspecified/non-multicast network
address. Canonical decimal ports range from 1 through 65535. Interface names
are nonempty, bounded to 64 bytes and exclude whitespace, path separators
and NUL. Runtime ownership must independently verify that the selected
interface actually owns the subnet. Neither configuration nor source
addresses establish sender authority.

[RFC 919](https://www.rfc-editor.org/rfc/rfc919.html) describes broadcast
addressing; [RFC 3021](https://www.rfc-editor.org/rfc/rfc3021.html) removes
directed broadcast for /31 point-to-point links. Tests cover limited and
directed broadcast, non-byte-aligned subnets, /30 and upper bounds, plus
wrong-subnet/unicast/multicast destinations, /31-/32, noncanonical subnet
and endpoint spelling, invalid ports and unsafe interfaces. The full
configuration package passed normally (0.887s) and under race (2.026s),
with vet, staticcheck, formatting and whitespace checks passing.

This is an endpoint prerequisite only. Listener/route configuration, socket
ownership, service activation and real native AIRC verification remain on
the roadmap; IPv4 multicast or UDP unicast evidence does not qualify
broadcast ingress.
