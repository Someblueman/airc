# IPv4 broadcast

Plain author requests need no principal, bearer or key on this carrier; see
[author network carriers](author-network-carriers.md). The rest of this page
describes the legacy signed/encrypted profile where it differs.

The Darwin/Linux `ipv4_broadcast` service accepts encrypted, signed AGD1
packets from separately provisioned senders. Replies use each principal's
fixed encrypted unicast mailbox. Service implementation is enabled;
runtime and native AIRC testing are deferred by owner direction.

## Service configuration

Use `airc-gateway serve --file CONFIG` with a listener such as:

```json
{
  "id": "broadcast",
  "protocol": "ipv4_broadcast",
  "address": "192.0.2.255:9001",
  "enabled": true,
  "ipv4_broadcast": {"interface": "eth0", "subnet": "192.0.2.0/24"},
  "operations": ["send", "receipt.get", "history.read", "watch"]
}
```

Replace the example interface and subnet with an owned broadcast-capable
interface. Configure ingress recipient keys, signing principals, a response
signing key and exactly one protected `ipv4_broadcast` return route for every
principal. The route's `listener` must be `broadcast`, its `destination` a
fixed canonical IPv4 unicast endpoint such as `192.0.2.10:9002`, and its
`recipient_key_id` and `recipient_public_key_file` must identify the authorized
reader. Native identity and target access come from existing bindings and
grants. History/watch use the ordinary cursor key. Anonymous access is refused.

The existing [AGD1 protocol](udp.md) supplies encrypted authentication,
original freshness, durable replay, shared admission limits, complete bounded
history/cursor pages and watch replies. No success response is sent for an
unauthenticated broadcast. Replies never use the observed source or a
broadcast destination. Configure one gateway receiver for a given broadcast
grant; a broadcast can reach several independently authorized gateways, so
deployment topology still matters.

Socket reads use a 1001-byte buffer and discard empty or over-1000-byte
datagrams before exposing any payload. Intake requires the configured
destination and interface metadata plus a usable unicast source. The socket
requests a 128 KiB receive buffer; Linux accounts twice that requested size,
and kernel limits can clamp it. Unicast replies pin the assigned source IPv4
and selected interface. Unknown remote subnet masks cannot be inferred from
the destination; provision authorized unicast mailboxes only.

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

These recorded results covered the endpoint prerequisite. Service activation
is now implemented, while real native AIRC verification remains on the roadmap;
IPv4 multicast or UDP unicast evidence does not qualify broadcast ingress.

## Protected route configuration prerequisite

`Listener.ipv4_broadcast` now carries interface/subnet options. Disabled
listeners validate endpoint and route shapes. Enabled configurations require
ingress recipient keys, response signing credentials and signing principals,
each with exactly one provisioned protected unicast route. Recipient key IDs
and absolute public-key paths are mandatory. Existing global validation
preserves known-principal/listener, duplicate-ID, operation and binding checks.
TLS and origin options are rejected.

Return destinations must be canonical IPv4 numeric unicast endpoints. The
selected subnet's network and directed broadcast addresses are rejected,
along with limited broadcast, multicast, unspecified addresses, IPv6, names
and invalid ports. Runtime socket ownership must also prevent broadcast
writes; configuration cannot establish the subnet masks of remote hosts.

The full configuration package passed normally (0.692s) and under race
(2.399s), with vet, staticcheck, formatting and whitespace checks passing.
Tests cover credentials, missing/duplicate routes, unsafe destinations,
unknown principals/listeners, independent routes for multiple principals
and disabled validation. The former activation gate is now removed; those
earlier test results do not verify the newly enabled service path.

## Socket prerequisite under verification

The socket owner requires an up, broadcast-capable interface
whose assigned IPv4 prefix exactly matches the configured subnet. Its
receive loop requires destination/interface ancillary metadata to match the
selected endpoint and interface. Reply writes disable SO_BROADCAST and also
explicitly reject configured subnet broadcast destinations. Service
activation uses the ordinary shared packet owner and joined shutdown.

An attempted Darwin loopback fixture failed delivery, and loopback writes
showed that SO_BROADCAST alone does not enforce the configured endpoint
policy. Loopback lacks the broadcast interface flag. Those findings led to
explicit interface capability and reply-destination checks, not a delivery
qualification claim. Ordinary refusal checks pass; three runtime controls
require `AGW_BROADCAST_INTERFACE` and `AGW_BROADCAST_SUBNET` supplied for an
explicitly authorized broadcast fixture. Missing fixture selection skips
those controls and leaves native qualification open.

A Linux setup helper is prepared and cross-compiled under
`/tmp/airc-broadcast-route-fixture`. It would create agw0/agw1 veth interfaces
and assign 10.200.0.1/24 and 10.200.0.2/24 inside the isolated network-none
container. It bounds netlink acknowledgements, then drops and verifies zero
effective/permitted/inheritable/ambient capabilities before tests. Under
the existing zero-capability policy, setup fails closed with operation not
permitted. Expanded fixture permissions remain an owner approval question;
no privileged setup run occurred as part of that earlier investigation.

The adapter independently rejects replies to the selected subnet's network
address, matching the protected return-route configuration boundary. The
focused refusal test passed under the race detector, and adapter vet plus
Darwin/ARM64 cross-compilation passed. Native send, bounded cursor read,
replay, authenticated refusal, capacity recovery and joined-shutdown tests
were prepared for an explicit interface/subnet fixture but were not run
because fixture authorization is unavailable; they remain outside the
checkout in `/tmp/airc-broadcast-route-fixture/pending-integration/`. The current
owner direction is to defer testing and complete implementation; the available
VPS may provide a later UNIX fixture without changing its live services.

## Implementation checkpoint

Production builds pass on Darwin ARM64 and Linux AMD64/ARM64, along with
formatting and whitespace checks. The enabled-protocol assertion and the
existing subnet fixture were migrated with the implementation. No tests,
socket probes, native AIRC acceptance or full normal/race gate were run for
this activation. The earlier prerequisite results above remain historical
evidence for their original source, not verification of the new service path.
