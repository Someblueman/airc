# Offline signed-message bundle format

The bounded format and read-only file loader are implemented. The authenticated
CLI, durable replay rejection and independently observed native delivery remain
outstanding in [ROADMAP.md](ROADMAP.md#add-request-and-mailbox-connections).
This checkpoint does not qualify the offline connector or removable media.

A version-1 JSON object contains only these fields:

```json
{"version":1,"envelopes":["COMPACT_SIGNED_ENVELOPE"]}
```

There must be 1–32 envelopes. Each is an opaque string of at most the existing
32 KiB signed-envelope limit. The entire UTF-8 document is at most 1 MiB,
including JSON encoding and whitespace. These limits apply independently:
32 envelopes at their individual maximum exceed the whole-file limit.
Duplicate/unknown/case-aliased/null fields, non-string entries, invalid UTF-8,
trailing documents, unsupported versions and empty lists/tokens are refused.
There are no attachment, executable or archive fields.

The container is unsigned and grants no authority. Each compact signed envelope
must separately establish the existing configured identity, audience, target,
operation and freshness before journal admission. Format parsing does not
verify signatures, reject durable replays or establish AIRC acceptance.

`ReadOfflineBundle` currently supports Darwin and Linux. It requires an absolute
normalized path, checks a regular file before opening, opens read-only with
final-component no-follow/nonblocking/close-on-exec flags, then verifies the
opened file still has the same identity and is regular and bounded. Reads are
limited to 1 MiB + 1, so growth beyond the limit is refused. Parent-path aliases
are allowed; this is not a private-directory ownership gate. Input ownership
and executable mode do not authenticate a sender. Loading never renames,
deletes, acknowledges or executes the source; the byte buffer is cleared after
parsing. Other platforms have an explicit unsupported file-loader result.

## Format and file-boundary evidence

Focused Darwin tests passed under race detection in 1.675s. They exercise
independent entry/token/whole-file limits, exact whole-file limit and limit + 1,
strict malformed-input refusals, executable-mode opaque shell text, unchanged
source content, and symlink/FIFO/directory/oversize/path refusals.
Adapter vet and staticcheck and current command builds passed. The adapter
tests also cross-compiled for Windows, retaining the unsupported file-loader
result; no Windows runtime qualification is claimed.

The same three controls passed on actual Ubuntu 24.04 ARM64, Docker 29.7.2 and
kernel `7.0.12-linuxkit`, terminal exit 0. The existing container used no network,
a read-only root/artifact mount and an automatically removed anonymous local
filesystem volume. Go 1.27.1 cross-compiled the adapter tests with CGO disabled;
these Linux controls have no race instrumentation. Their executable SHA256 was
`910f9570c914fadc21323ec2d651ee4ddef8ff6cf96b7cf944b7088f4eb33ba5`.
The Go/module source digest (sorted tracked/untracked paths and content, each
NUL-separated) was
`2250a1f285c9cc5f2004cb3af05ce4e8de51dfe90d713db50606faee47878268`.
Raw evidence and manifest are
`/tmp/airc-linux-native-159afc4/offline-bundle-format.log` and
`/tmp/airc-linux-native-159afc4/offline-bundle-format-provenance.json`.

These controls establish the format and file boundary only. No real removable
medium or native AIRC importer was exercised at this checkpoint. The preceding
full normal/race acceptance in [testing.md](testing.md#linux-portability-integration-checkpoint)
applies to its recorded source; the new importer will require fresh integration
acceptance after implementation.
