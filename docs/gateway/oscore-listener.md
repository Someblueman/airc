# Native OSCORE gateway boundary

The owned native worker, fixed-owner service configuration, and joined process
lifecycle are implemented. Darwin acceptance covers native AIRC send, bounded
history reads, whole-gateway restart/replay, and outgoing response-transfer caps
and expiration. Incoming limits and partial-transfer expiration remain on the
roadmap. Build the worker separately and select its executable explicitly in
listener configuration.

Configure the listener after fresh provisioning with the existing `oscore init`
command. Provision using a configuration without the new listener reference,
then add the listener: configuration loading checks every referenced context
file, including disabled listeners, and never creates missing material.

```json
{
  "id": "oscore-device",
  "protocol": "coap_oscore",
  "address": "127.0.0.1:5683",
  "enabled": true,
  "operations": ["send", "receipt.get", "history.read"],
  "oscore": {
    "binary": "/absolute/owned-build/build/oscore-gateway",
    "context_file": "/absolute/private/device-oscore.json",
    "principal": "device"
  }
}
```

The referenced principal must be active and have a configured signing key. The
context file's realm, principal, and binding must exactly match configuration.
The address must be a numeric unicast or wildcard IP with a valid port; port zero
selects an ephemeral endpoint reported in the service ready record. This mode
uses OSCORE rather than TLS certificates or browser origins. Existing operation,
cursor-key, request admission, and journal limits apply. Responses return through
the requesting protected CoAP exchange, so no datagram return route is required.
Native service activation is supported on Darwin and Linux; runtime evidence here
is Darwin only. Capability discovery checks references and reports the partial
mode without starting a worker, decoding keys, or creating journal state.

The service owns the child until shutdown completes. A failure while starting a
later listener closes and joins the already started child before releasing the
journal. Missing provisioning and mismatched owners never emit a ready record.

`oscore.Start` validates an owner-selected absolute executable and literal IPv4
or IPv6 listen address, loads existing journal state, and claims an exclusive
process-local lease for the canonical cryptographic context. It refuses missing
state and simultaneous owners. Executables and their resolved parent directories
must be owned by the current user or root and protected from other writers;
sticky temporary parents are permitted. Keys enter inherited private fd 3,
application RPC uses fd 4, and argv contains only the listen IP and port. The
child receives no inherited environment credentials. Startup requires a bounded
ready response with the actual bound port. Failure joins the child and persistence
worker before releasing its lease.

The owner closes both sockets on cancellation, requests SIGTERM, and kills a
child that has not exited within three seconds. Unexpected child, persistence,
or RPC termination fails the owner and joins all workers. `Close` and `Wait`
return only after joining and releasing the context lease. This lease prevents
concurrent restored replay windows in one process; it is not backup rollback
protection.

The worker exposes OSCORE-only JSON POST at `/v1/coap/rpc`, with no Observe.
Incoming bodies are capped at 16 KiB. At most four outgoing response bodies of
64 KiB are retained. Incoming and outgoing blockwise transfers have a fixed
three-second age checked before each native event-loop iteration; continuations
cannot renew it. A synchronous private RPC callback can delay cleanup until its
ten-second absolute I/O budget finishes. Session, association, and aggregate
incoming body caps apply separately. Native allocation refusal precedes dispatch.

The owner provisions one fixed cryptographic context for one realm, principal,
and binding. Startup must verify the realm and load its existing journal state;
missing state cannot be replaced. Key configuration travels through the private
persistence socket, separately from application RPC. The native child must
authenticate and commit receiver replay state before passing plaintext to Go,
and commit sender reservations before encrypting a response. Both native context
and crypto work stay on the loading thread. See [oscore-state.md](oscore-state.md)
for persistence, Echo, and native allocation controls.

`adapter.ServeOSCORERPC` owns one private connected socket from that configured
child. The caller owns and joins the child. There is no public RPC listener or
RPC-only configuration. Requests run sequentially, with shared gateway request
admission. Each input is a four-byte big-endian length followed by the existing
JSON `FrameRequest`; zero lengths and lengths above 16 KiB close the boundary
before allocating the body. After the first header byte, one three-second
absolute deadline covers the remaining header and body. Idle reads are
cancellable through socket closure.

An input must be version 1 with a valid correlation ID, a nonempty signed or
encrypted JOSE envelope, and no streaming flag. The existing dispatcher verifies
its signature, audience, freshness, target, and operation authority. The verified
principal and binding must exactly match the fixed OSCORE context owner before
durable intake or reading. A packet cannot select an owner or another journal
row. OSCORE protects the signed profile in transit; an encrypted envelope is
also supported. Bearer authentication alone cannot pass this boundary.

The existing correlated dispatcher preserves stable durable request IDs and
bounded cursor semantics. A single request has its existing four-second
execution budget; streaming/Observe is refused. The response is one CoAP code
byte, four-byte big-endian JSON length, and one existing `FrameResponse`, bounded
to 64 KiB with a three-second write deadline. Authentication failure uses 4.01,
malformed input uses 4.00, full admission uses 5.03, and a successful result uses
2.05. Logical dispatch failures remain explicit JSON failures. Native transport
receipt alone does not establish upstream AIRC acceptance.

The Go tests use real Ed25519 signed envelopes and a private SQLite journal to
verify that wrong context owners cannot reach intake, a correct owner admits
the exact normalized body, and replay retains the same upstream ID. No backend
worker runs in this fixture, so it proves durable intake rather than native AIRC
delivery. Additional private-socket tests exercise zero/oversized lengths,
truncation, malformed JSON, joined cancellation, and stalled-peer write limits.
The partial-read control verifies that a slow header consumes the same absolute
deadline as its body; neither partial input nor output can renew its budget.

Verification on Darwin, 2026-10-06: the complete adapter race suite passed
(9.762 seconds), followed by the final OSCORE RPC controls twice under race
detection (2.170 seconds). Adapter vet/staticcheck, command builds, formatting,
and diff checks passed. Final full-module acceptance remains open until the
remaining executable integration work.

The native integration controls start the actual sanitizer-enabled worker on
IPv4 and IPv6, refuse duplicate ownership, missing state, and occupied ports,
and verify port/lease reuse after joined shutdown. They kill a running child,
cancel its parent, and close the journal during an actual encrypted Echo callback
to verify failure propagation. A real upstream native client completes fresh Echo,
Block1 request and Block2 response transfers, and repeats one signed request;
the journal retains its exact normalized body and stable upstream ID. This fixture
runs no AIRC backend worker or independent observer, so it establishes durable
intake, not AIRC delivery or whole-gateway restart acceptance. Transfer allocation
caps and expiration still require dedicated adversarial network controls before
listener qualification.

Lifecycle checkpoint verification on Darwin, 2026-10-06: the checked-in patch
applied to a clean pinned archive; strict ASan/UBSan build and CTest passed
(0.33 seconds). The gateway packages and command passed race testing, including
all explicit native controls (OSCORE package 11.012 seconds). Command builds,
repository vet/staticcheck, formatting, and diff checks passed. Full-module final
acceptance remains on the roadmap.

Service configuration checkpoint on Darwin, 2026-10-06: gateway package and
command race tests passed against the clean sanitizer-enabled native build
(service 6.468 seconds). Controls verified real service ready/shutdown/restart,
wrong owner and missing-state refusal, cleanup after a later bind failure, and
read-only capability discovery. Command builds, repository vet/staticcheck,
formatting, and diff checks passed. These controls do not replace independent
AIRC observer, bounded history, adversarial network, or restart/replay evidence.

Native application acceptance on Darwin, 2026-10-06:
`TestOSCOREAuthenticatedSendAndBoundedResume` passed under race detection
(12.51 seconds; package 14.289 seconds) against the clean sanitizer-enabled native
worker and actual pinned libcoap client. The fixture starts an owned current
gateway executable and isolated pinned AIRC daemon. Two blockwise Unicode sends
reached accepted receipts with one send attempt each; an independent AIRC
observer verified exact author, room, and body. Repeating each signed intent
retained the message ID and did not add native records. Protected history requests
read one record per page, matched native CHECK records exactly, resumed each
explicit cursor, and reached an empty caught-up page. The current gateway
executable SHA-256 was
`65f83f3154561f9a381db58c14d8e521e9bb6a6b8ab99ebf033f96e20f168116`.

This proves native application acceptance and bounded reading in this fixture.
It does not prove packet replay rejection after whole-gateway restart, hostile
network load/expiration controls, non-confirmable mode acceptance, Linux runtime
behavior, or production peer crash durability. Those remain separate work.

Whole-gateway restart control on Darwin, 2026-10-06:
`TestOSCOREPacketReplayAndStableSendAfterGatewayRestart` passed under race
detection (7.60 seconds; package 9.244 seconds). A bounded ciphertext relay
retained the last protected POST from a real native client. The actual native
worker refused it with CoAP 4.01 and `Replay detected` on a new UDP peer and
changed outer CoAP message ID, both before and after restarting the complete
gateway process. Loading the journal after joined shutdown verified unchanged
receiver initialization, sequence, and replay-window bits. A second restart
accepted a fresh protected retry of the original signed intent with the same
accepted message ID and one send attempt; native CHECK contained exactly one
record with the correct author and body. The gateway executable SHA-256 was
`dc41c7b08a76f76d45ea853e12f3ccea299de7e82ef2c509ac39b5e3327732af`.

This control uses joined SIGINT shutdown and the existing journal; it does not
claim backup rollback detection, power-loss evidence, or native peer crash
durability. Integration vet/staticcheck, formatting, and diff checks passed.

Outgoing response-transfer control on Darwin, 2026-10-06:
`TestOSCOREOutgoingTransferCapAndAbsoluteExpiry` holds four actual protected
multi-block history responses open through bounded ciphertext relays. A fifth
signed send receives a decrypted 5.03 in the pinned native client's protocol
trace; native CHECK retains only the seed message and a read-only journal query
finds no job for the refused request. The native client automatically delays a
5.03 retry for the default 60 seconds, so this control ends its wait after one
second; process exit alone is not refusal evidence.

The relays forward a pending continuation for every retained response before its
original expiration and observe each reply. Clients remain connected and the
responses remain incomplete. A new protected retry of the refused signed intent
then receives a protected durable admission response after the original
three-second ages expire, before the earliest continuation-plus-three-second
deadline. The control records that deadline before forwarding the first
continuation and requires admission to complete before it; a slow client call
cannot pass after a renewed age has also expired. It yields one
accepted record with the correct author and body. Disjoint fixture peer nonce
ranges stay within the fixed replay window. This is real outgoing network-cap
and nonrenewable-expiration evidence; incoming body limits and partial-transfer
expiration remain separate controls.

The integrated native OSCORE application, whole-gateway replay, and outgoing
transfer controls passed together under race detection (16.603 seconds). The
final outgoing control with the additional read-only journal assertion passed
under race detection (7.77 seconds; package 9.399 seconds). Integration
vet/staticcheck, formatting, and diff checks passed. The unchanged production
worker used the clean pinned ASan/UBSan build; its gateway executable SHA-256
was `373e59c3555e2ff60a84b23a3b4f4927199bb5bbdef9af0ef617ada5043a0706`.

The strengthened admission-completion deadline control passed under race
detection (9.82 seconds; package 11.441 seconds), followed by integration
vet/staticcheck, formatting, and diff checks. Gateway executable SHA-256:
`b7fca1bd8878e0d783b5eefb491536611226525d17dc8e8faf22682a1733f239`.
