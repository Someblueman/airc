# MQTT-SN ingress

The bounded datagram codec, disabled configuration contract and UDP adapter are
implemented. Service ingestion and independent native delivery qualification
remain on the roadmap.

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
Application retries must preserve the signed request ID and intent. These
service/native boundaries remain unimplemented and unqualified at this
checkpoint. The decoder alone makes no authentication or response claim.

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

## Disabled configuration contract

`protocol: "mqtt_sn"` selects options under `mqtt_sn.routes`. Each of 1–128
routes grants a unique predefined `topic_id` from 1 through 65,534 to a
`principal` and exact `target`. The existing principal must be active, have
signing credentials and be authorized for send to that target. A bearer
credential is unnecessary for this signed publication profile. Listener
operations must be exactly `["send"]`; TLS and origins are refused. The listen
address uses the existing UDP endpoint policy and defaults to `127.0.0.1:0`.
No source-address identity, new secrets or durable schema is introduced.
Enabling the listener is refused until service integration is implemented.

The configuration checkpoint passed affected Darwin ARM64 race tests in
1.360s; the unchanged codec race result was reused from cache. Vet, staticcheck
and diff checks passed. Tests cover signing-only principals, revoked/unknown or
unsigned publishers, target/operation refusal, topic ID bounds/duplicates,
the 128-route limit, options on the wrong protocol, endpoint/TLS/origin refusal
and refusal to enable the unwired mode.

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
