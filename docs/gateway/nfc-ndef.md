# NFC Type 2 signed-bundle import

The selected physical reader is Linux with Python 3.10+ and nfcpy 1.0.4,
using an explicitly selected USB NFC reader and NFC Forum Type 2 tags.
`scripts/nfc-ndef.py` reads one tag into a raw NDEF file; the Darwin/Linux
`airc-gateway import-ndef` command authenticates and imports that file through
the existing [offline signed-bundle service](offline-bundle.md). The reader,
nfcpy and its native USB dependencies are operator-provisioned. Nothing is
installed or configured by the importer.

This is send-only. The local JSON report identifies durable intake, replay
rejection and any native AIRC receipt. Reading a tag successfully does not
establish delivery, and there is no radio return channel for conversation reads.

## Record and authority

The complete raw NDEF message is at most 2040 bytes and contains exactly one
MIME record with type `application/vnd.airc.bundle+json`. Its TNF is 2, MB and
ME are set, and chunking and record IDs are prohibited. Both short-record and
four-byte big-endian payload lengths are supported. Extra records, trailing
bytes, empty payloads and truncated lengths are refused. This is raw NDEF,
without a Type 2 memory dump, capability container or surrounding TLV bytes.

The payload is the existing version-1 offline JSON bundle, including its strict
field rules and 1–32 individually signed envelopes. NDEF overhead and the
2040-byte bound further restrict how much of that format fits. Actual tag NDEF
capacity is smaller than its memory capacity because of TLV and reserved-byte
overhead; many tags cannot fit even one envelope. Size the complete signed
record against the chosen tag before provisioning it.

Neither tag UID, physical possession, record type nor filename grants identity.
Every entry must pass the existing configured sender signature, audience,
revocation, target, Send permission and original freshness checks before any
journal is opened. An altered or copied tag cannot change those grants or
renew validity. Existing stable request IDs and durable replay rejection apply.
The signed contents are readable; this format does not provide confidentiality.

## Preparing and reading

Use the existing signing workflow and offline-bundle JSON format, then encode:

```sh
python3 scripts/nfc-ndef.py encode \
  --input /absolute/path/signed-bundle.json \
  --output /absolute/private/tag.ndef
```

The encoder treats bundle bytes as opaque. It checks size, not signatures.
Provision the resulting raw NDEF message onto a sufficiently large Type 2 tag
using operator-controlled tag-writing software. The repository helper only
encodes files and reads tags; it does not write, format, protect or erase tags.

Select the reader's current USB bus and device numbers, then read and import:

```sh
python3 scripts/nfc-ndef.py read --device usb:001:004 \
  --output /absolute/private/received.ndef
airc-gateway import-ndef --file /absolute/path/gateway.json \
  --input /absolute/private/received.ndef
```

The numbers above are examples; USB device numbers can change on reconnection.
Use the sole enabled `offline_bundle` listener and the signing keys, AIRC
bindings and target grants documented in [offline-bundle.md](offline-bundle.md).
No new listener protocol or identity store is introduced. The same per-entry
report and partial-admission semantics apply: inspect each entry, and treat a
nonzero exit as an error even when a durable prefix is reported.

The reader accepts only a fully specified `usb:BBB:DDD` selector and `106A`
discovery, rejects tags outside Type 2, reads the raw `ndef.octets` property and
never uses the permissive record decoder. The read runs in a supervised child
with a 30-second deadline; interruption or timeout kills and reaps that child.
It checks the decoded length against both advertised NDEF capacity and the
2040-byte profile limit before publishing. Upstream nfcpy can process a malformed
16-bit TLV length of up to 65535 bytes before those checks; 2040 is the accepted
message bound, not a limit on all dependency parsing or USB traffic. See the
[nfcpy frontend API](https://nfcpy.readthedocs.io/en/latest/modules/clf.html),
[tag API](https://nfcpy.readthedocs.io/en/latest/modules/tag.html) and
[selected Type 2 implementation](https://github.com/nfcpy/nfcpy/blob/v1.0.4/src/nfc/tag/tt2.py).

Helper paths are absolute and normalized. Output is a new mode-0600 file in a
private current-user directory; existing files and final symlinks are refused.
The Go importer uses its existing regular-file, no-follow, identity and bounded
read checks. Input file ownership is not sender authentication. The source file
and tag remain intact after admission or replay rejection.

## Verification boundary

Implementation is source-reviewed and production builds cover Darwin ARM64 and
Linux AMD64/ARM64. The Python helper receives syntax compilation only. Testing
is deferred: no reader, physical tag, nfcpy dependency, NFC exchange, new import
command, refusal/replay case or native AIRC delivery has been exercised for this
mode. Physical tag provisioning, compatible reader permissions and actual
signed messages remain inputs to later acceptance in [ROADMAP.md](ROADMAP.md).
