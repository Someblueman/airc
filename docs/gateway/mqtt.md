# MQTT ingress

The bounded packet codec is implemented as a prerequisite. TLS configuration,
authenticated routing, durable admission and native qualification remain on
the roadmap. No MQTT listener is enabled by this checkpoint.

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

The service must require CONNECT first and once, authenticate before successful
CONNACK, own bounded client sessions and honor Keep Alive/partial-input deadlines.
It must verify the signed envelope under the same connection principal and the
configured exact topic/target mapping. Only durable intake may precede PUBACK;
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
