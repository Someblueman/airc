# Isolated OSCORE prototype evidence

OSCORE is not implemented by the gateway. Catalogue row 25 remains partial.
The existing CoAP/DTLS and CoAP/TLS profiles do not establish OSCORE support.
OSCORE does not impose an authenticated-send-only limitation: bounded reads and Observe support need independent acceptance evidence.
Richer AIRC operations are optional under [ROADMAP.md](ROADMAP.md).

The isolated native prototype uses libcoap revision
`75a532c1f7fcb3053f91c8f30ac2f49355194028`, GnuTLS 3.8.13 and a local CMake
build. It runs on Darwin loopback, independently of AIRC. Its application
response is `Hello World!`, not a gateway operation or AIRC receipt.
No Go dependency, gateway listener, installed binary or live AIRC state was
changed by these experiments.

## Library boundary findings

The native probes found that the selected library revision ignored failed
sender/receiver persistence callbacks. A failed sender reservation could also
advance the in-memory reservation boundary, permitting later encryption without
another callback. The prototype patch checks callback results and poisons the
affected context after failure; subsequent encryption/decryption fails closed.
Multiple-context controls exercise both context-list orders.

Restoring a recipient replay window initially left its bootstrap flag enabled.
The patch carries the existing configuration initialization marker into the
recipient context. Native captured-ciphertext replays with fresh CoAP message
IDs are rejected after restoring sequence 1/window 1 and sequence 0/window 1.
New authenticated sequence-8 requests still decrypt and dispatch.

Normal receiver persistence originally occurred before AEAD authentication.
The patch moves that save after successful decryption and before application
dispatch. The separate Echo/bootstrap callback remains at its
protocol-authenticated point. The patch also fixes
rollback of zero-valued prior replay state. A tampered sequence-8 ciphertext
causes no persistence callback or handler invocation; its untouched control
persists and dispatches. The manually assembled initial-state Echo exchange did
not complete, so it is not evidence for full initial-state retransmission.

The original prototype's cumulative patch applied cleanly to an archive of the
pinned revision, and that patched archive built the native examples. Its SHA-256:
`8038ca886efca97de0c1c4f49800a1833f598e3a34f1c852286ca38a397bdb10`.
The owned patch, subsequently extended with explicit native resource caps, is at
[native/oscore/libcoap-persistence.patch](../../native/oscore/libcoap-persistence.patch).
The [owned build](../../native/oscore/README.md) archives the pinned revision and
runs sanitizer-enabled encryption/decryption persistence-boundary checks.
This establishes the dependency boundary, not gateway OSCORE support.

## State-store controls

The stock example file-write callbacks do not establish durable state storage.
The owned temporary prototype instead uses private state directories, advisory
process ownership acquired before loading state, bounded versioned records,
atomic replacement, file sync and directory sync. Receiver state-file controls
reject symlinks, malformed records, embedded NULs, oversized input and competing
owners. Native receiver SIGKILL/restart restores sequence 9; captured replay is
rejected without an application handler. Injected rename and file-sync failures
return callback failure and dispatch no application request.

The sender prototype binds its record to a SHA-256 digest of the exact OSCORE
configuration bytes. Normal startup rejects missing state and configuration
mismatch. An explicit experimental fresh-context provisioning flag permits the
matched fresh-state control; it is not permission to reuse an old key after
losing its sequence state. The prototype cannot establish that an explicitly
provisioned key has never been used; production provisioning must enforce that
boundary.

The final digest-bound sender controls reserve 8, then reserve 16 before
SIGKILL. Restart without fresh provisioning restores 16 and reserves 24.
The independent server observes request partial IVs 0/1 before the restart and
16/17 after it. Injected file-sync and rename
failures prevent encryption, including a second direct encryption attempt on
the same context. A competing sender owner is rejected before context creation.
These are process-restart and simulated-I/O-failure controls. They do not prove
power-loss durability, filesystem journal behavior, or AIRC application delivery.
OSCORE sequence reservation is separate from the gateway's stable operation IDs
and durable delivery journal.

## Evidence location and remaining work

The local artifacts are under `/tmp/airc-oscore-probe-20261005`; they are
temporary experiment files, not a portable checked-in implementation. The
directory contains `PROBE_REPORT.md`, the cumulative patch, clean-build logs,
captured datagrams, and named sender/receiver failure and restart logs.

Production work still requires an owned native integration boundary, complete
key-context provisioning and recovery policy, bounded ingress and response
correlation, and authenticated send plus complete bounded reads against the
pinned AIRC fixture. Observe is optional under the roadmap and requires its own
cancellation and output-limit evidence if added. The dependency build is now
reproducible through the checked-in build script; the durable store and listener
integration remain unfinished. The checked-in [journal state API](oscore-state.md)
now has independent SIGKILL/reopen and failure controls; it is not yet connected
to native callbacks or a gateway OSCORE listener.
Linux/remote-device runtime acceptance and production cryptographic/state-store
review remain open. No hardware limitation was established by these local
probes; missing integration remains implementation work.
