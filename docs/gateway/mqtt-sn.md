# MQTT-SN ingress

The bounded datagram codec is implemented. Configuration, service ingestion
and independent native delivery qualification remain on the roadmap.

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

Integration must verify the signed publisher and exact configured topic/target
authority before existing durable admission. UDP can lose a publication before
durable intake. This QoS mode supplies no successful delivery acknowledgement;
socket write completion cannot establish intake or native AIRC acceptance.
Application retries must preserve the signed request ID and intent. These
boundaries remain unimplemented and unqualified at this codec checkpoint.
Unknown-topic error response policy must be settled during integration against
the specification's section 6.7 recommendation; no response behavior is claimed
by this decoder.

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
