# MQTT-SN ingress

The bounded datagram codec, configuration contract and UDP adapter are
implemented. Opt-in service wiring and Darwin/Linux native checks pass;
full integration acceptance passed.

## Selected profile

The connectionless publishing mode uses MQTT-SN 1.2 section 6.8, QoS -1,
non-retained PUBLISH and predefined topic IDs. See the
[official specification index](https://mqtt.org/mqtt-specification/) and the
[original IBM-authored specification mirror](https://image.mqtt.cn/wp-content/uploads/2024/03/2024032209060883.pdf).
This mode requires no CONNECT, registration or subscription. It fits the
catalogue's explicitly permitted publish-only connector.

`mqttsnwire.Decode` accepts exactly one datagram. Both one-byte and extended
three-byte total-length encodings are valid, including extended framing for
small messages. Declared length must equal the complete datagram length;
truncation, trailing bytes and concatenated messages are refused. Payload is
opaque, copied and bounded to 1–32,768 bytes. Total packet size is at most
32,777 bytes. Reserved topic IDs 0 and 65,535, other QoS/topic modes, RETAIN
and nonzero unused Will/CleanSession flags are outside this selected profile.
Wire message ID must be zero for QoS -1. DUP provides no application replay
identity; integration must use the signed request ID.

The adapter verifies the signed publisher and exact configured topic/target
authority before existing durable admission. UDP can lose a publication before
durable intake. This QoS mode supplies no successful delivery acknowledgement;
socket write completion cannot establish intake or native AIRC acceptance.
Application retries must preserve the signed request ID and intent. Targeted
Darwin/Linux service/native checks pass; full repository acceptance passed.
The decoder alone makes no authentication or response claim.

## UDP adapter boundary

`ServeMQTTSN` owns and closes a validated UDP socket. Payload must be the existing
encrypted detached envelope with a signed inner request; plaintext signed tokens
are refused. Service integration must provision an existing gateway recipient
key. There is no source-address identity or application fragmentation.

The receive buffer contains at most 32,778 bytes, one greater than the packet
bound, so oversized/truncated input cannot become a valid prefix. One receive
and admission runs at a time, sharing the service request semaphore. There is
no per-source lease, pending user-space queue or per-packet goroutine. Admission
has a three-second context bound. Context cancellation closes the socket and
joins its close callback before returning.

Successful intake sends no packet. Section 6.7's recommended invalid-topic
PUBACK is emitted only for fresh, authenticated, encrypted send intent with
an unknown predefined topic ID: seven bytes, return code 2, message ID zero.
It is an error, not intake ownership or native acceptance. Malformed,
unauthenticated, expired, wrong-principal, wrong-target, capacity and journal
refusals are silent. The source must be a valid unicast UDP destination; error
writes have a three-second deadline. A lost error response remains possible.

Component tests use the actual UDP socket and existing signature/encryption
verification with a real journal. They cover durable replay despite changed
DUP, validly signed conflicting intent preserving the original, exact routing
refusals with authenticated witnesses, plaintext/signature/freshness/operation
refusal, request-slot ownership, no success acknowledgement, exact authenticated
error bytes, oversize datagrams and closed/joined cancellation. The targeted
Darwin ARM64 race suite passed in 4.015s, and the full affected adapter race
suite passed in 30.241s; vet, staticcheck and diff checks passed. Subagent review
found no actionable issues.
These component results do not qualify service/native delivery or any physical
sensor network.

## Listener configuration contract

`protocol: "mqtt_sn"` selects options under `mqtt_sn.routes`. Each of 1–128
routes grants a unique predefined `topic_id` from 1 through 65,534 to a
`principal` and exact `target`. The existing principal must be active, have
signing credentials and be authorized for send to that target. A bearer
credential is unnecessary for this signed publication profile. Listener
operations must be exactly `["send"]`; TLS and origins are refused. The listen
address uses the existing UDP endpoint policy and defaults to `127.0.0.1:0`.
No source-address identity, new secrets or durable schema is introduced.
An enabled listener requires a provisioned gateway recipient key for encrypted
publications. It defaults disabled. The service owns the UDP socket, shares
existing request limits and delivery workers, and joins before journal close.
CLI discovery advertises only send and does not start runtime state.

The configuration checkpoint passed affected Darwin ARM64 race tests in
1.360s; the unchanged codec race result was reused from cache. Vet, staticcheck
and diff checks passed. Tests cover signing-only principals, revoked/unknown or
unsigned publishers, target/operation refusal, topic ID bounds/duplicates,
the 128-route limit, options on the wrong protocol, endpoint/TLS/origin refusal
and refusal to enable the then-unwired mode. The activation checks separately
cover recipient-key requirements and send-only CLI discovery.

## Service checks awaiting integration acceptance

The final targeted Darwin ARM64 native race suite passed in 17.248s. Configuration
and CLI race checks passed in 1.290s and 2.132s. The independent raw UDP client
uses the actual gateway CLI; an independent native AIRC observer verifies exact
author, target and Unicode body. A real relay receives and deliberately discards
the first packet before ingress, leaving no journal row or native message;
retry with the same signed intent delivers once. Restart and a new UDP source
preserve the accepted application ID despite changed DUP.

An existing trusted HTTPS receipt API in the isolated fixture witnesses committed
acceptance before deliberate shutdown. This is a separate test control; MQTT-SN
supplies no receipt or read path. Native observation alone did not establish
receipt persistence. A draft external live SQLite polling fence produced fatal
busy/snapshot errors and was replaced with the owner's existing receipt API;
no journal durability setting or production deadline was changed.

Fourteen refusal cases cover unknown topic, validly authenticated mismatched
target/publisher/non-send intent, plaintext, signature, expired/future intent,
nonzero message ID, QoS 0, RETAIN, CONNECT, size and length mismatch. Authority
cases first authenticate their envelope. Refusals leave no job across any
principal and no native message; a subsequent legitimate publication proves
healthy delivery. A best-effort conflicting UDP submission remains silent and
preserves the original native intent, with a subsequent independently accepted
publication and exact history. Direct adapter tests establish conflict refusal
deterministically; UDP silence alone cannot prove a packet was processed.

With an independent SQLite writer lock held, a publication produces no intake;
SIGTERM joins, and releasing the lock/restarting still leaves no job or native
message. Producer retry then delivers exactly once with one native send attempt.
This establishes interruption with the lock held and zero intake; it does not
prove that the particular datagram entered a blocked journal call. SIGINT and
SIGTERM shutdown release the journal for owner-verified reopening. Subagent
review found no unresolved findings. Full unchanged-source integration acceptance passed; the exact gate is
recorded in [testing.md](testing.md#mqtt-sn-integration-checkpoint).

### Linux ARM64 runtime

On 2026-10-06, the same 18 native MQTT-SN test/subtest cases passed with zero
skips or failures and terminal exit 0 on Linux ARM64, kernel `7.0.12-linuxkit`,
Docker 29.7.2, in the existing Ubuntu 24.04 image
`sha256:33ceb71981b602c1a7443a53469e4dba065f7503eab3078a2d7a57a2ab987517`.
The container had no network, no image pull and a read-only root/artifact mount;
private fixture files used an auto-removed anonymous `/tmp` volume. No toolchain
was installed. This is local actual Linux runtime evidence without Linux race
instrumentation, a remote sensor or physical radio claim.

Frozen Go/test/module source SHA-256:
`45de8b5a6984a1ffee603b8a0dcd06d5ff47eb9e2ce247d354a56baf919111fc`.
Gateway SHA-256: `f798f15adace365c5cfcf7e2ef4d3abe18c98306b4ac4ff3cafe5336e27f37d4`.
Test executable SHA-256: `dee08b0ac72345186c121b9b12e711c279f88cde6d10bbc5ab49ab202c4c5cdd`.
Native daemon SHA-256: `1f3aa4a566a23d4326e2a2b3ed81942b5384e17521dc1bf38572511778d7e85f`,
from pinned AIRC `96f16c3bbb8a3faac2e69684f591af63ceb58450`, archive SHA-256
`4f25ee66941bd79c29f936b8deabc120dc0451e5d1b747afee4bbdf593b32829`.
Private log/provenance prefix: `/tmp/airc-linux-native-159afc4/mqtt-sn-ingress-v1`.
Digest and Go build-info checks verified supplied executables before execution.
Full unchanged-source acceptance passed under
`/tmp/airc-gateway-mqtt-sn-v1-4ef42e2`: normal 630.075s, race 1,397.911s, each
with 2,655 test/subtest passes, 53 skips, zero failures and 35 packages
(27 passing, eight without tests). Build, formatting, vet, staticcheck and diff
checks also exited 0. See the [exact checkpoint](testing.md#mqtt-sn-integration-checkpoint).

## Codec verification

On 2026-10-06, the final affected Darwin ARM64 race suite passed in 1.295s.
Two-second fuzzing with three workers exercised 353,775 inputs without failure;
vet, staticcheck and diff checks passed. Independent packet encoding covers
both length forms, the 255-byte transition, maximum payload, copied ownership,
all truncation prefixes, reserved IDs, unsupported flags and message types,
length mismatch, concatenation, nonzero message IDs and payload bounds. Review
caught and resolved the QoS -1 zero-message-ID requirement before this
checkpoint. This evidence covers the
codec only; it does not qualify MQTT-SN service ingestion or a physical radio.
