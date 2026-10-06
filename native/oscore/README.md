# Pinned OSCORE dependency

This is the native dependency boundary for the gateway OSCORE implementation.
The native worker and owned child lifecycle are described in
[oscore-listener.md](../../docs/gateway/oscore-listener.md). Service activation
and AIRC send/read acceptance remain unfinished. The Go [durable state store](../../docs/gateway/oscore-state.md)
and [fresh owner provisioning](../../docs/gateway/oscore-provisioning.md) are
implemented. Native callbacks now use the private owner socket described below.
The gateway's Go build does not link or download this dependency.

`libcoap-persistence.patch` applies to upstream libcoap commit
`75a532c1f7fcb3053f91c8f30ac2f49355194028`. It makes failed sender reservations
and receiver replay-state persistence fail closed, poisons the affected context,
persists receiver updates after authenticated decryption, restores replay-window
initialization, and restores zero-valued state on rollback.
The owned patch also adds opt-in hard UDP endpoint session and per-session
OSCORE token-association caps. These internal fields belong to this pinned build;
zero preserves upstream allocation behavior. The owned context setup sets caps
before endpoints open: 16 sessions including active transfers, 256 associations
per session, eight-byte tokens, 64 KiB individual bodies, and 256 KiB aggregate
received blockwise body RAM. At the session cap, an idle session can be evicted; if all
sessions are active, new peers are refused. Existing peers remain usable.
The patch is inherited from the [isolated prototype](../../docs/gateway/coap-oscore-prototype.md).
Upstream licensing is retained in `LICENSE.libcoap`.

## Build and verify

Prerequisites: a C compiler, Git, CMake 3.20 or newer, pkg-config, and GnuTLS
development headers/libraries. The initial owned build used Clang on Darwin
arm64, CMake 4.4.4, and GnuTLS 3.8.13. No global installation is performed.

From the repository root:

```sh
git clone https://github.com/obgm/libcoap.git /tmp/libcoap-source
scripts/build-oscore.sh /tmp/libcoap-source /tmp/airc-oscore-build
```

The output directory must not already exist. The script archives the exact
commit from the supplied Git repository, so its branch and local changes do not
affect the build. It applies the checked-in patch, builds the static library and
upstream examples, then runs the owned CTest boundary check. It keeps the source
and build artifacts for inspection and removes its temporary archive on exit.
ASan and UBSan are enabled by default. Owned C checks compile with C11,
`-Wall -Wextra -Werror -pedantic`.

The test exercises actual library encryption and decryption with the
[RFC 8613 C.4 vector](https://www.rfc-editor.org/rfc/rfc8613.html#appendix-C.4),
including exact protected and unprotected bytes. It verifies:

- Sender reservation succeeds before encryption; a failed reservation prevents
  that encryption and later encryption on the same context.
- A modified authentication tag causes no receiver persistence callback and
  rolls back the replay advance; the authentic control decrypts and persists.
- Restoring the accepted replay state rejects the captured request even with
  a fresh outer CoAP message ID.
- Failed receiver persistence prevents plaintext release and poisons the context.
- Invalid restored sequence/window values are rejected, and rollback restores
  a zero-valued initial replay state. A restored sequence-zero/window-one state
  also rejects sequence zero.

These checks simulate callback success/failure and loading recorded state.
They do not prove process restart durability, power-loss durability, a full
initial-state Echo exchange, application delivery, or native network acceptance.
Those remain gateway integration requirements. Upstream CUnit tests are separate
from the owned check; this build does not require or run CUnit.

The build also produces `oscore-context-check`, used by the explicit Go/native
configuration test documented in [oscore-provisioning.md](../../docs/gateway/oscore-provisioning.md).
It checks real key derivation from freshly generated server and peer profiles.

`persistence.c` loads one fixed owner context and restored journal state through
a private connected socket. The Go `internal/gateway/oscore` owner refuses
missing state before releasing key material, commits sender reservations and
authenticated receiver updates before acknowledging them, and closes the socket
on cancellation. Native initialization and callbacks use nonblocking socket I/O
with a three-second absolute deadline; failures poison the context. The caller
must retain callback storage and join the child before releasing it.
All native crypto/context operations belong to the thread that loads the
context. Callbacks reject another thread before touching mutable state or the
socket, preventing interleaved request/acknowledgement exchanges when libcoap
temporarily releases its global lock around a callback.

The `oscore-persistence-probe` executable exercises this production callback
boundary with real native encryption/decryption and the real SQLite journal:

```sh
AIRC_OSCORE_PERSISTENCE_PROBE=/tmp/airc-oscore-build/build/oscore-persistence-probe \
  go test -race ./internal/gateway/oscore -count=2 -timeout=90s
```

These tests recreate native processes and verify reserved sender nonces, reject
previously accepted ciphertext with a changed outer message ID, reject tampered
ciphertext without changing journal state, and close the journal during a real
callback to verify that further crypto fails. Receiver tests explicitly restore
sequence-zero/window-one state; they do not qualify a fresh Echo exchange,
network listener, AIRC send/read acceptance, or whole-gateway restart.

The additional `receiver-fresh` control runs actual native UDP client/server
traffic on an ephemeral loopback port. Starting with a fresh zero-state journal,
it completes the encrypted Echo challenge/retry, delivers one exact POST body
to an OSCORE-only resource, and receives its encrypted response. The resulting
sender reservation and authenticated replay state are checked in the Go journal.
This tests fresh bootstrap without seeded receiver state; it still does not
activate or qualify a gateway listener or AIRC application delivery.

The `receiver-bounds` control exercises the actual session and association
allocation functions in the configured native context. It fills 16 referenced
sessions, verifies refusal of a seventeenth and reuse of an existing session,
then releases one to idle and verifies admission after eviction. It fills 256
token associations, verifies refusal without evicting any retained token, deletes
one, and verifies allocation resumes. Go checks that these allocation controls
leave the fresh journal's crypto state unchanged. This is an allocation-boundary
test, not a native network load test or gateway acceptance evidence.

The patch also records the initial incoming blockwise transfer time for the
owned worker's absolute expiration policy.

The extended patch applies cleanly to the pinned upstream archive. Its clean
owned build passed strict compilation and ASan/UBSan CTest on 2026-10-06
(0.33 seconds); native bridge and key-derivation tests passed twice under race
detection against that clean build. The owned worker now bounds outgoing response transfers, deadlines, IPC frames,
and process lifetime; adversarial network controls remain qualification work.
The extended patch SHA-256 is
`81601a97c5b599e9c11162171d5469c37e9043034f1befcde74373c2bf3d3962`.

The initial clean build through `scripts/build-oscore.sh` completed on 2026-10-06
with ASan/UBSan enabled; CTest passed (0.50 seconds). An additional sequence-zero
restore control was subsequently verified through the same CMake/CTest build.
The upstream examples produced existing Darwin `sprintf` deprecation warnings;
the owned check compiled without warnings under its strict warning policy.

To exercise the owned child and real native client with private fixture keys:

```sh
AIRC_OSCORE_GATEWAY=/tmp/airc-oscore-build/build/oscore-gateway \
AIRC_OSCORE_PERSISTENCE_PROBE=/tmp/airc-oscore-build/build/oscore-persistence-probe \
AIRC_OSCORE_CONTEXT_CHECK=/tmp/airc-oscore-build/build/oscore-context-check \
  go test -race ./internal/gateway/oscore -count=1 -timeout=90s
```

The native client defaults to `build/libcoap/coap-client` beside that worker;
`AIRC_OSCORE_COAP_CLIENT` can select the same pinned client's absolute path.
The client fixture's example sequence-file persistence is not production peer
restart durability evidence.
