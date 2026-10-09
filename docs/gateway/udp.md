# Authenticated UDP unicast

By default, the `udp` listener carries the shared gateway operation contract over bounded
AGD1 datagrams. It accepts only encrypted detached requests. The UDP source
address identifies a transport association; it never selects an AIRC
principal, author, or response destination.

For clients without credentials or provisioned return addresses, an explicitly
configured listener can instead use [anonymous UDP](anonymous-udp.md). That is a
separate plaintext wire profile using one shared guest account.

For a running remote deployment and repeatable send/receipt/history commands,
see the [VPS test lab](vps-test.md).

## Owner configuration

An enabled UDP listener requires an explicit numeric bind address and port. A
wildcard address and port `0` are allowed for owner-controlled ephemeral
binding. UDP listeners reject TLS certificate fields and browser origins;
authentication is provided by the signed inner request and its encrypted
envelope.

The top-level `response_signing_key` contains an owner ID and an absolute,
owner-only PKCS#8 Ed25519 private-key file:

```json
"response_signing_key": {
  "id": "gateway-response",
  "private_key_file": "/private/gateway-response-ed25519.pem"
}
```

Every configured principal needs exactly one UDP `return_routes` entry for
each enabled UDP listener. A route fixes the listener, principal, destination,
and recipient key. Requests cannot override these values:

```json
{
  "id": "agent-udp-return",
  "principal": "agent",
  "protocol": "udp",
  "listener": "udp",
  "destination": "192.0.2.20:49152",
  "recipient_key_id": "agent-response",
  "recipient_public_key_file": "/private/agent-response-p256-public.pem"
}
```

Destinations must be numeric unicast addresses with a nonzero port. Wildcard,
unspecified, multicast, broadcast, and hostname destinations are rejected.
Routes for disabled UDP listeners may remain provisioned, but they do not
enable a listener or require the response signer until that listener is
enabled.

The response signer uses Ed25519. Each route recipient is an owner-provisioned
P-256 public key used for ECDH-ES. The response envelope binds realm,
principal, binding, and route ID, and expires after 120 seconds with the
gateway's existing 60-second clock-skew policy.

For local provisioning, generate keys in an owner-controlled directory:

```sh
umask 077
openssl genpkey -algorithm ED25519 -out gateway-response-ed25519.pem
openssl pkey -in gateway-response-ed25519.pem -pubout -out gateway-response-ed25519-public.pem
openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out agent-response-p256-private.pem
openssl pkey -in agent-response-p256-private.pem -pubout -out agent-response-p256-public.pem
```

The gateway consumes the Ed25519 private key and recipient public key. A
client retains the matching P-256 private key and gateway signing public key
to decrypt and verify responses.

The native Go example consumes an already encrypted request envelope from
stdin. Its mailbox must match the provisioned route destination; the example
does not register or change routes. Run from the gateway checkout:

```sh
go run ./examples/gateway/udp-client \
  --address 192.0.2.10:9001 --bind 192.0.2.20:49152 \
  --signing-public /private/gateway-response-ed25519-public.pem \
  --recipient-private /private/agent-response-p256-private.pem \
  --realm local --principal agent --binding agent-binding \
  --route agent-udp-return --signing-id gateway-response \
  --recipient-id agent-response --correlation request-1 < encrypted-request.txt
```

The addresses above are documentation placeholders; provision actual numeric
endpoints. Generate request envelopes using [signed-message-v1.md](signed-message-v1.md).
The example creates a random correlation ID per invocation unless explicitly
supplied. Explicit IDs must be fresh for distinct requests and retries; a stable
logical request ID does not require reusing a transport correlation. This
prevents an earlier valid response on the same mailbox matching a later call.
The example prints one verified response as JSON. It has no automatic retry or
streaming mode; receipt/status and history are separate authenticated requests.
The adapter itself also supports bounded streaming watches. A general client
must demultiplex correlations, reject missing sequence positions and resume
from the last fully verified cursor.

## Wire and limits

Each AGD1 fragment is at most 1000 bytes. Requests reassemble to at most 16
KiB; encrypted response tokens are bounded to 64 KiB and the response wire
budget is 1 MiB per request, with 4 KiB reserved for error/end frames. Decrypted
response frames are limited to 32 KiB. Four incomplete assemblies are
retained per source, incomplete assemblies expire after five seconds, and the
completed-frame replay cache holds 64 entries for 30 seconds. Writes are
serialized and bounded by three seconds including queue time. Non-streaming
requests last at most four seconds; streams at most 20 seconds and cannot
outlive authenticated validity. Inactive sources expire five seconds after
creation or their last authenticated request; invalid traffic does not renew
them. Active workers retain their source slot until completion. Shutdown closes
the socket and joins active request workers.

Requests use the existing encrypted detached `FrameRequest` profile. The
signing key resolves the authenticated principal and its fixed binding, and the
outer JWE targets the configured gateway recipient. Responses are encrypted
for the fixed route recipient. Invalid input is discarded without a response.

Responses use compact EdDSA JWS inside ECDH-ES/A256GCM JWE with distinct response
types and purpose. Receivers verify both key IDs, signature, fixed four-part
scope, timestamps, frame correlation and sequence before consuming a result or
advancing a cursor. Missing fragments or sequence gaps require a bounded retry
or cursor renewal; arrival of a UDP packet is not native acceptance.

All currently implemented shared operations can use this path, including
mutations, reads, receipts, and watch modes. Operation allowlists and durable
request IDs remain authoritative. A retry preserves the original request ID
and exact intent; transport correlation and AGD1 frame IDs may be fresh after
loss or reconnect. An `accepted` receipt is evidence of native acceptance.
The ordinary delivery policy remains bounded `retry`: uncertain native delivery
can produce a duplicate after retained acceptance evidence is lost. UDP loss
does not grant new attempt budgets, extend expiry or justify a new logical ID.

## Current boundary

The bounded UDP adapter, response JOSE codec, owner route model, and native
loopback packet-loss/replay checks are implemented in the gateway worktree.
Catalogue completion still depends on the shared operation set and native
acceptance for each operation. Physically one-way deployments need separate
transport limitation evidence; this bidirectional profile does not claim
one-way delivery semantics. Remote PMTU behavior, broader platform startup
coverage, and production key distribution remain deployment work.
