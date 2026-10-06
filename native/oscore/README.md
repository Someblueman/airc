# Pinned OSCORE dependency

This is the native dependency boundary for the gateway OSCORE implementation.
It does not yet provide a gateway listener or a durable state store.
The gateway's Go build does not link or download this dependency.

`libcoap-persistence.patch` applies to upstream libcoap commit
`75a532c1f7fcb3053f91c8f30ac2f49355194028`. It makes failed sender reservations
and receiver replay-state persistence fail closed, poisons the affected context,
persists receiver updates after authenticated decryption, restores replay-window
initialization, and restores zero-valued state on rollback.
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

The initial clean build through `scripts/build-oscore.sh` completed on 2026-10-06
with ASan/UBSan enabled; CTest passed (0.50 seconds). An additional sequence-zero
restore control was subsequently verified through the same CMake/CTest build.
The upstream examples produced existing Darwin `sprintf` deprecation warnings;
the owned check compiled without warnings under its strict warning policy.
