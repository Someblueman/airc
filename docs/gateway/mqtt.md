# MQTT ingress

The bounded packet codec, configuration contract and TLS adapter are wired into
an opt-in service listener. CLI/native checks pass on Darwin and Linux ARM64,
and the [unchanged-source full acceptance checkpoint](testing.md#mqtt-integration-checkpoint) passed.

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

The listener configuration uses `protocol: "mqtt"`, explicit
`operations: ["send"]`, a TLS certificate/key pair and `mqtt.routes`. Each of
1–128 routes contains `principal`, literal `topic` and exact `target`. Topics
are unique across the listener. The principal must be active with existing
bearer and signing credentials, send permission and the exact target grant.
Origins are refused. Set `enabled: true` to select the listener; it defaults off.
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

These component checks alone did not qualify a service listener, TLS-to-PUBACK
journal ordering, independent native client or MQTT-to-AIRC delivery. Those
boundaries have separate service evidence below.

## Service qualification

The service uses TLS 1.3 without the framed transport ALPN and existing accepted
socket limits. MQTT connections and delivery workers join before journal close.
CLI discovery advertises only send and does not start the runtime.

Targeted Darwin ARM64 CLI/native race tests passed in 26.675s. Configuration and
CLI discovery race checks passed in 1.360s and 1.581s. An independently encoded
MQTT client exercises the actual gateway CLI and native AIRC observer. Holding
the real SQLite writer prevents PUBACK until release; the committed row is
visible after PUBACK. Two exact Unicode messages reach the configured native
author/target, including a distinct first submission whose PUBACK stays unread
before disconnect/reconnect replay. Stable request IDs produce one native send
each despite changed packet IDs; a valid signed conflicting body closes without
PUBACK and preserves the accepted original.

Twelve refusal cases check unknown topic, valid but mismatched signed target or
publisher, malformed signature, expired/future intent, non-send operation,
zero packet ID, QoS 0, RETAIN, repeated CONNECT and oversized Remaining Length.
Authority witnesses first verify the signed intent so routing refusal cannot
be confused with invalid signature/configuration. Refusals leave no job or
native message; a fresh authenticated connection remains usable. Additional
tests reject untrusted CA, wrong name, TLS 1.2 and plaintext; two accepted socket
slots bound authenticated and pre-authentication connections. Shutdown closes
and joins both. Closure assertions require peer EOF/reset; timeouts fail.
Subagent review found no unresolved findings. Full and Linux gates passed;
the two MQTT service/native qualification lines were removed from the roadmap.
This is the documented publish-only profile; MQTT-SN and other broker modes
remain separate work.

### Linux ARM64 runtime

The same 20 native MQTT test/subtest cases passed with zero skips or failures
on Linux ARM64, kernel `7.0.12-linuxkit`, Docker 29.7.2, in the existing Ubuntu
24.04 image `sha256:33ceb71981b602c1a7443a53469e4dba065f7503eab3078a2d7a57a2ab987517`.
The container had no network, no image pull and a read-only root/artifact mount;
its private test files used an auto-removed anonymous `/tmp` volume. No new
toolchain was installed. These are actual Linux runtime checks, without Linux
race instrumentation or a remote-broker qualification claim.

Frozen Go/test/module source SHA-256:
`aedc753b8b5b95a86858536cd9082de2b31d72dca40e89b1b475395565c5f323`.
Gateway SHA-256: `ddec6aa7a4f75b6db0ed06e6650f9c992ee24bb852cac92caf48f91cdd511ccf`.
Test executable SHA-256: `a5c19d6c469efba3071f1736fe0c64ea817574eefbe9e19e6654bd3df011523d`.
Native daemon SHA-256: `1f3aa4a566a23d4326e2a2b3ed81942b5384e17521dc1bf38572511778d7e85f`,
from pinned AIRC `96f16c3bbb8a3faac2e69684f591af63ceb58450`, archive SHA-256
`4f25ee66941bd79c29f936b8deabc120dc0451e5d1b747afee4bbdf593b32829`.
Private log/provenance prefix: `/tmp/airc-linux-native-159afc4/mqtt-ingress-v1`.
Prebuilt fixture checks verified each supplied binary digest and Go build info
before execution. The full Darwin acceptance gate on this same source passed
with terminal exit 0 under `/tmp/airc-gateway-mqtt-v1-816acfd`: normal 625.384s,
race 1,395.204s, command builds 2.391s, formatting 0.414s (empty output), vet
1.057s, staticcheck 3.882s and diff check 0.026s. Each test phase recorded 2,599
test/subtest passes, 53 skips and zero failures across 34 packages (26 passed,
eight without tests). Gateway integration took 618.752s normal and 1,234.857s
race. The source digest was checked before every phase and again after terminal
completion. The skips do not qualify any omitted mode or physical/remote path.
