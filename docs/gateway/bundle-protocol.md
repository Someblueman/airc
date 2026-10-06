# Bundle Protocol ingress

The BPv7 decoder is implemented as a prerequisite for one-way signed-send file
ingestion. Configuration, signature/identity binding, durable admission and
native delivery qualification remain on the roadmap. No BPv7 listener or import
command is enabled by this checkpoint.

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
and lifetime are positive milliseconds from the DTN epoch; checked arithmetic
prevents overflow. Fragments, extension blocks, administrative payloads, status
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
