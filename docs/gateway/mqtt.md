# MQTT ingress

The bounded packet codec, disabled configuration contract and TLS adapter are
implemented as prerequisites. Service wiring and end-to-end native qualification
remain on the roadmap. No MQTT listener is enabled by this checkpoint.

## Selected wire profile

The publish-only profile uses [MQTT 3.1.1](https://docs.oasis-open.org/mqtt/mqtt/v3.1.1/mqtt-v3.1.1.html).
CONNECT requires the `MQTT` protocol name, level 4, clean session, username and
password, with no Will. Client IDs and usernames contain 1–64 ASCII letters,
digits, underscores or hyphens. Password is exactly 64 hexadecimal bytes for
the existing bearer-credential format. Keep Alive is 1–60 seconds. These are
explicit gateway restrictions; the codec does not authenticate credentials.

PUBLISH supports QoS 1 with a nonzero packet identifier, optional DUP and no
RETAIN. Literal application topics are 1–128 ASCII bytes from letters, digits,
underscores, hyphens, dots and slashes, without a leading slash. Wildcards and
system topics are refused. Payload is opaque and contains 1–32,768 bytes;
signature verification and topic/target authority belong to integration.
PINGREQ and DISCONNECT have empty bodies. Other packet kinds and modes are
outside this profile; this is not a general subscription broker.

`Read` consumes one packet without reading the next. Remaining Length must
use its shortest encoding and cannot exceed 32,900 bytes. Excess declarations
are refused before body allocation or reading. Raw body buffers are cleared
after fields are copied. Malformed UTF-8/NUL client IDs produce packet refusal;
valid UTF-8 IDs outside the supported policy have a distinct rejection result.
Unsupported protocol level likewise has a distinct result for CONNACK code 1.
The fixed CONNACK, PUBACK and PINGRESP writers handle partial and stalled writes.

## Integration boundary

The disabled listener configuration uses `protocol: "mqtt"`, explicit
`operations: ["send"]`, a TLS certificate/key pair and `mqtt.routes`. Each of
1–128 routes contains `principal`, literal `topic` and exact `target`. Topics
are unique across the listener. The principal must be active with existing
bearer and signing credentials, send permission and the exact target grant.
Origins are refused. Enabled MQTT remains refused until service integration.
No credential material or new durable schema is introduced by this contract.

The adapter requires CONNECT first and once and authenticates before successful
CONNACK. Username and Client ID must both equal the authenticated principal ID.
An authenticated reconnect cancels, closes and joins its predecessor before
success; unauthenticated connections cannot replace an owner. The caller must
bound accepted connections and supply TLS 1.3. Handshake and CONNECT share a
three-second absolute deadline; writes and journal admission are bounded to
three seconds. Keep Alive uses 1.5 times the declared interval from the last
complete control packet; partial input does not renew it. Cancellation closes
the underlying socket before TLS close-notify and joins every connection.

The adapter verifies the signed envelope under the same connection principal
and binding, send operation and configured exact topic/target mapping. It shares
the service request admission semaphore. Authentication, authority, capacity or
admission refusal closes without PUBACK. Only durable intake may precede PUBACK;
that acknowledgement transfers intake ownership and does not prove AIRC
acceptance. Packet IDs and DUP are transport metadata; signed request IDs and
the existing journal must provide stable replay/conflict handling.

## Codec verification

On 2026-10-06, affected Darwin ARM64 race tests passed in 1.314s; vet,
staticcheck and diff checks passed. Final malformed-input fuzzing exercised
156,175 inputs with three workers in a one-second run and no failure.
Tests independently encode input packets and verify concatenated stream
boundaries, exact topic/payload limits, unsupported modes, every truncation
prefix, oversized declarations without body consumption, client-ID rejection
distinctions and exact reply bytes under short/stalled writes.
This is codec evidence; it makes no TLS, sender authority, durable intake or
native MQTT-to-AIRC delivery claim.

The disabled configuration checkpoint passed affected configuration and codec
race tests (1.636s and 1.324s), vet, staticcheck and diff checks. Tests cover
TLS/options/operation refusal, unknown/revoked or incompletely credentialed
principals, exact target authority, duplicate/wildcard topics, the 128-route
boundary and refusal to enable the unwired listener. Subagent review found no
actionable issues.

## Adapter verification

On 2026-10-06, full affected Darwin ARM64 adapter and dispatch race suites passed
in 28.260s and 1.770s; vet, staticcheck and diff checks passed. Targeted race tests
passed in 5.865s and 1.799s. Tests use trusted TLS over a real Go byte stream for
CONNECT/PING, bad credential/username/client-ID refusal, idle/partial keep-alive
expiry and joined cancellation. Separate ownership tests prove reconnect waits
for its predecessor and old release cannot remove a successor. Admission tests
use existing signature verification and a real journal for exact target grants,
pre-journal refusal, capacity ownership and stable signed-request replay despite
changed MQTT packet IDs. Subagent review found no actionable issues.

These component checks do not qualify a service listener, TLS-to-PUBACK journal
ordering, independent native client or MQTT-to-AIRC delivery. Those remain
explicit service and native qualification work.
