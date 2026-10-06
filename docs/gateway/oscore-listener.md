# Native OSCORE gateway boundary

The native network listener is still unfinished. Fresh owner provisioning,
durable native callbacks, Echo bootstrap, allocation caps, and the Go private
RPC endpoint are implemented prerequisites. They do not establish AIRC delivery
or bounded cursor reads through a native OSCORE client. Listener configuration,
the child lifecycle, outgoing transfer limits, and full gateway restart/replay
acceptance remain on the roadmap.

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
