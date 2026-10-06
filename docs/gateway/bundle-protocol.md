# Bundle Protocol ingress

One-way BPv7 signed-send file ingestion is implemented and qualified through
independent native clients on Darwin and Linux. Full normal/race integration
acceptance and required build/static gates passed on the same frozen source.
This is a file convergence input, not a bundle router or a general bundle agent.

## Import configuration and contract

Run `airc-gateway import-bpv7 --file CONFIG --input /absolute/input.bpv7` with
exactly one enabled `bpv7` listener. `serve` refuses this mode before journal
access. The listener requires only the explicit `send` operation, no address,
TLS or origins, and a provisioned mapping such as:

```json
{
  "id": "bp", "protocol": "bpv7", "enabled": true,
  "operations": ["send"],
  "bundle_protocol": {
    "source": "dtn://producer/", "destination": "dtn://gateway/send",
    "principal": "producer", "target": {"kind": "room", "name": "#room"}
  }
}
```

The mapped principal must be active, have a signing key and permit that exact
target and send operation. Its existing binding selects the AIRC account and
backend; file metadata grants no authority. The payload is the existing compact
EdDSA signed-message envelope for the configured realm. Its principal, target,
request ID, issued-at and expires-at must exactly match the mapping and bundle.
Creation and lifetime must both be multiples of 1,000 milliseconds. Subsecond
values are refused rather than rounded into the integer-second signed contract.
Creation cannot be in the future and original expiry must still be ahead of
import time; the existing signed lifetime limit also applies.

The loader supports Darwin and Linux, reads only a bounded regular file through
the shared no-follow/inode-checked offline reader and leaves the source intact.
Parsing, mapping and signature refusal precede journal opening. A verified
intent uses the existing durable admission, replay markers, fingerprints,
delivery workers and version-1 local entry report. Re-import reports rejected
replay while returning the original result and may recover already-admitted
pending work. Conflicting content under one identity cannot admit another job.
Workers join before journal release. Delayed delivery retains original expiry;
import never resets lifetime. There is no remote receipt or conversation-read
return path, and an intake report is not by itself native acceptance.

## Wire profile

The bounded profile follows [RFC 9171](https://www.rfc-editor.org/rfc/rfc9171.html):
an indefinite outer array, optional self-described CBOR tag, one primary block
and one final payload block. Nested arrays may be definite or indefinite;
integers and lengths must use shortest encodings and byte strings must be
definite. Input is limited to 65,536 bytes and payload to 32,768 bytes.

Primary CRC-16/X25 or CRC-32C is mandatory. Payload CRC may be absent or either
supported kind. Checks cover the original encoding, including array breaks,
with only the CRC value bytes zeroed. Checksums do not authenticate a sender.

Source is a bounded lowercase `dtn://node/` node EID, destination is a bounded
singleton `dtn://node/application` EID and report-to is `dtn:none`. Creation time
is positive milliseconds from the DTN epoch; lifetime is a positive duration
in milliseconds after creation. Checked arithmetic prevents overflow.
Fragments, extension blocks, administrative payloads, status
reports, multicast, IPN and BPSec are outside this explicit profile.

## Stable identity

Request ID is `bp7-` followed by unpadded base64url SHA-256 of the concatenation
of `airc-bpv7-identity-v1` and a NUL byte, the exact source EID and another NUL,
creation milliseconds as unsigned 64-bit big-endian, and sequence as unsigned
64-bit big-endian. Destination, lifetime and payload do not change bundle
identity. The importer must bind those fields to provisioned routing and the
signed request before admission; the decoder alone makes no replay claim.

The independent fixed vector for source `dtn://sender/`, creation `1000000` and
sequence `17` yields `bp7-3ZcbrV9Lzf3YawyS4vpuxrFnY2T-xk9jaa5Wz_pCtCk`.

## Decoder verification

On 2026-10-06, affected Darwin ARM64 race tests, vet, staticcheck and diff checks
passed. A second fuzz run after the parser fix exercised 156,200 inputs with
three workers and no failure. Tests cover 24 supported encoding/CRC forms,
independent CRC reference values and a fixed wire/identity vector, field and
size refusals, every truncation prefix and checksum-covered byte corruption.

The same package ran on Linux ARM64 in the existing Ubuntu 24.04 container:
31 test/subtest passes, zero skips or failures. Test binary SHA-256:
`59c869b25953af8d2549c7c536234f747e4996280ae808683d573e2f55e33c50`.
The private terminal record is
`/tmp/airc-linux-native-159afc4/bpv7-codec-v1.log`.
This is codec evidence, not native AIRC delivery or Linux race evidence.

## Affected native ingestion evidence

Independent producer code serializes CBOR/CRC and derives identity without the
production decoder. Actual CLI controls verified exact native author, target
and Unicode body, pre-aged creation plus a physical two-second retention of
wire bytes, stable replay and changed-body conflict. This does not claim a
two-minute physical storage delay: that older creation is a validity fixture.

Sixteen preflight cases cover source/destination, sequence, creation/lifetime,
subseconds, expired/future input, signature, CRC, whole-file size, request ID,
target, audience and a valid signature from another authorized principal.
Each refuses without a journal or native message; a matched healthy control
then delivers. SIGKILL, SIGINT and SIGTERM tests interrupt admitted work at a
held backend, reopen the released journal and recover the same upstream ID,
original expiry and one exact native post. Existing offline-import controls
also pass after sharing the admission path; its JSON input/report contracts
are unchanged.

Darwin affected package race tests, native CLI race controls, vet and the
Darwin/Linux staticcheck matrix passed. The final native BPv7 race controls
took 13.172s; the shared unsafe-file control took 1.508s under race detection.
Actual Linux ARM64 BPv7 and offline controls produced 29 test/subtest passes,
zero skips or failures, terminal exit 0. Private log/provenance prefix:
`/tmp/airc-linux-native-159afc4/bpv7-ingress-v2`.

Linux gateway SHA-256:
`a14a9d9406e7d557bdc7c295855496fba856635a5c02f84f703c0ce73663b84e`;
integration test binary SHA-256:
`7f98e491c5cd31ef4b60fe9292cba97d39ae41a2d9cb45d9c26761607812b4a5`.
The pinned native AIRC binary is the existing `96f16c3…` fixture with SHA-256
`1f3aa4a566a23d4326e2a2b3ed81942b5384e17521dc1bf38572511778d7e85f`.
The isolated existing Ubuntu 24.04 ARM64 container had no network, a read-only
root/artifact mount and an auto-removed anonymous local filesystem volume.
No Linux race instrumentation, Windows runtime, remote bundle router or
removable-device qualification is claimed.

Full normal acceptance passed at frozen Go/test/module source
`f592b5875ff57a5c6a2c2192170fcd2b48fc654ff77b76ceb3e7d557b094a814`:
2,518 test/subtest passes, 53 skips, zero failures, terminal exit 0 in
617.684s. Full race acceptance also recorded 2,518 passes, 53 skips and no
failures, terminal exit 0 in 1,360.546s. Build, formatting, vet, staticcheck and
diff checks passed at that unchanged source. See the [full checkpoint](testing.md#bpv7-integration-checkpoint).
Private evidence prefix: `/tmp/airc-gateway-bpv7-v2-73aa587`.
The earlier run was intentionally stopped after detecting the duplicate-key
test-fixture weakness; its terminal exit 1 is retained under the v1 prefix
and is not acceptance evidence. The corrected test separately proves that
the original principal authenticates before the mapped-principal refusal.
