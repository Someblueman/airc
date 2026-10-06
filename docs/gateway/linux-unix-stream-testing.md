# Linux Unix-stream acceptance evidence

The existing filesystem Unix-stream connector was qualified on Ubuntu 24.04
ARM64, Docker 29.7.2 and kernel `7.0.12-linuxkit`. The actual local socket
uses mode 0600 inside a mode-0700 directory; kernel peer credentials are checked
with Linux `SO_PEERCRED`, and every application request still requires the
existing encrypted signed envelope.

The former Darwin-only send/cursor test is now
`TestUnixStreamAuthenticatedSendAndBoundedResume`, built for both supported
platforms. The actual framed example can use `AGW_FRAMED_CLIENT_BINARY` plus
`AGW_FRAMED_CLIENT_SHA256` through the same bounded, digest/program/platform
verified private-copy fixture path described in
[testing.md](testing.md#cross-compiled-native-fixtures). With neither variable
set, its existing source build is unchanged.

The run reused the checksum-pinned native daemon and real production gateway
from the [Linux FIFO fixture](fifo.md#linux-native-delivery-bounded-reads-and-recovery),
production source `159afc40c4e7607fe871443002cd39596f3fb41e`. The updated
integration Go/module source digest was
`b612d4afe18bb8115a6a51fc508c3effe8e9fb71a8e106389e6c20a9150e6657`
(sorted paths and contents, each NUL-separated). Host Go 1.27.1 cross-compiled
the actual framed example and tests with `CGO_ENABLED=0 GOOS=linux GOARCH=arm64`.
The test executable SHA256 was
`c87ff175f8bb9c8bd4a99cca933e9f2271fa00b61f86c8f9187ffdd08bafd5ce`;
the framed example SHA256 was
`632ae19cedbaff2af721b9b3d2a7f89e0e6311035cdda49db1cca1138176a4de`.
All artifact digests were rechecked before execution. The container had no
network, a read-only root/artifact mount and an automatically removed anonymous
local-filesystem volume for journal state.

`-test.run '^TestUnixStream' -test.v -test.count=1 -test.timeout=180s`
completed with terminal exit 0. Four real native controls passed:

- Two encrypted authenticated sends, exact independently observed ID/author/
  target/whole multiline Unicode body, stable accepted replay with one attempt,
  complete limit-one cursor pages and final empty caught-up resume (0.45s).
- Signed-only profile refusal, invalid encrypted authentication refusal, healthy
  encrypted discovery, the real framed example CLI, malformed and incomplete
  frame closure, joined shutdown and owned socket cleanup (5.10s).
- Room/context/pin/waiting/reaction operations with independently observed
  native mutations and whole snapshots (0.65s).
- The shared Unix operation conformance control (3.17s).

Actual Linux adapter controls also completed with terminal exit 0. The
root-only peer test ran here: a real UID/GID-1 child reached the socket but was
closed by the peer gate; a subsequent owner connection was accepted. It was
not skipped. Private lifecycle, existing-path/symlink/insecure-parent refusal
and replacement-inode preservation also passed. The adapter executable SHA256
was `2973c2c8ca376f52e676962ac357e8c7c8a41b64583a5bdc514c67292d1d3c56`.

Raw evidence is `/tmp/airc-linux-native-159afc4/unix-stream-native.log` and
`/tmp/airc-linux-native-159afc4/unix-stream-adapter.log`; the build manifest is
`/tmp/airc-linux-native-159afc4/unix-stream-provenance.json`. Affected Darwin
Unix-stream tests retained their default source-built examples and passed under
race detection in 21.549s; integration vet, staticcheck and diff checks passed.

This qualifies the existing Linux filesystem Unix-stream mode on this fixture.
Linux executables have no race instrumentation. It does not qualify Unix
datagrams, abstract namespaces, other platforms or power-loss durability.
The unchanged-source full Darwin normal/race checkpoint passed; see
[testing.md](testing.md#linux-portability-integration-checkpoint). Final
acceptance after future executable changes remains in the roadmap.
