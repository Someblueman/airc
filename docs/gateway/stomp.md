# STOMP 1.2 ingress

Implementation is in progress; service activation and native qualification remain
in [ROADMAP.md](ROADMAP.md). The selected profile follows the
[STOMP 1.2 specification](https://stomp.github.io/stomp-specification-1.2.html).

The intended endpoint uses TLS 1.3. CONNECT/STOMP login identifies a provisioned
AIRC principal and passcode verifies its bearer credential. Fixed request and
response destinations grant an exact principal, target and operation set.
Each SEND carries signed JWS intent from that same principal and binding.
Wire receipt IDs and connection-local acknowledgement IDs never replace the
signed request ID used for durable replay.

A SEND receipt establishes processing and durable intake for a mutation, not
native AIRC acceptance. Signed response MESSAGE frames expose delivery state
or a bounded History/Search/Context result through an explicitly subscribed
response destination. The selected acknowledgement mode is client-individual;
one pending response bounds storage and request admission. Lost receipts or
responses recover by resubmitting the identical signed request on a fresh
connection. Mutation validity times remain part of that stable intent.

Arbitrary broker forwarding, dynamic grants, transactions, cumulative
acknowledgements and general queue semantics are outside this profile.
No native, platform or full-service qualification is claimed yet.

## Frame bounds

Frames use UTF-8 commands and headers, LF or CRLF line endings, and a terminating
NUL. Header escaping follows STOMP 1.2, with CONNECT/CONNECTED exceptions and
first-value semantics for repeated headers. Unknown escapes are fatal.
Content lengths count bytes and permit embedded NUL only within the declared
body. SEND, MESSAGE and ERROR alone may carry bodies.

The codec limits each encoded frame to 65,536 bytes, each body to 32,768 bytes,
each line to 1,024 bytes and each frame to 32 header entries. Empty EOL frames
represent heartbeats; their timing and connection deadlines belong to the
connection owner. Bounds apply before peer-controlled allocation or iteration.

The frame codec is implemented independently of service activation. On
2026-10-07 (Europe/London), its affected race suite passed in 1.293s; vet,
staticcheck, formatting and diff checks passed. Independent wire examples cover
LF/CRLF heartbeats, pipelined frame boundaries, embedded-NUL content lengths,
first repeated-header values, CONNECT/CONNECTED escaping exceptions and exact
encoded bytes. Refusal cases cover invalid UTF-8/escapes, malformed or excessive
lengths, forbidden bodies, truncation and line/header/body/total-frame bounds.
This checkpoint supplies framing evidence only; no enabled service or native
STOMP qualification is claimed.
