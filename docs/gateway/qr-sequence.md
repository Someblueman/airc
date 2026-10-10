# QR image-sequence import

The Darwin ARM64 QR tool carries a bounded signed-message bundle in a sequence
of PNG images. Native [CoreImage](https://developer.apple.com/documentation/coreimage/ciqrcodegenerator)
generates the QR symbols and native [Vision](https://developer.apple.com/documentation/vision/vndetectbarcodesrequest)
decodes them. The reconstructed bytes feed the existing
[offline signed-bundle importer](offline-bundle.md), which owns authentication,
freshness, durable admission, replay rejection and AIRC delivery.

This is a send-only image-file profile. It has no remote receipt channel or
conversation reads. A local importer report distinguishes durable intake from
native AIRC acceptance. The separate [camera optical receiver](optical-camera.md)
uses this sequence format and importer; its physical input and permissions
remain untested. Printed-image quality and acoustic channels require their own
evidence.

## Build and use

The builder uses the installed Apple SDK and Swift toolchain with CoreImage,
Vision, ImageIO, CryptoKit and AVFoundation. It embeds camera permission metadata
for the optional capture command and installs no dependency or service.

```sh
./scripts/build-qr-tool.sh --output /absolute/path/qr-bin
/absolute/path/qr-bin/qr-tool encode \
  --input /absolute/path/signed-bundle.json \
  --output /absolute/path/fresh-qr-images
```

The input uses the existing version-1 offline bundle format. The QR encoder
treats its bytes as opaque; it does not authenticate or renew signed envelopes.
The QR profile limits the complete bundle to 65,536 bytes, even though the
offline importer supports larger ordinary files.

Supply all PNG images to the decoder in any order, then import its output:

```sh
mkdir -m 700 /absolute/path/private-qr-import
/absolute/path/qr-bin/qr-tool decode \
  --output /absolute/path/private-qr-import/bundle.json \
  /absolute/path/received-images/*.png
airc-gateway import-bundle --file /absolute/path/gateway.json \
  --input /absolute/path/private-qr-import/bundle.json
```

Provision the existing `offline_bundle` listener, principal signing keys,
bindings, target permissions and journal as described in
[offline-bundle.md](offline-bundle.md#import-command-and-durable-outcomes).
QR files and their names grant no authority. Use the importer's exit status and
per-entry JSON report; successful image decoding alone establishes no admission
or AIRC delivery.

Existing output directories, files and symlinks are refused. Encoded images
are private files in a fresh private directory. Decoded output is published as
a new private regular file only after every fragment and the complete digest
have been checked. Its parent directory must be owned by the current UID and
have no group or other write bits. Input and output paths must be absolute and
normalized. Final-component input symlinks and nonregular inputs are refused;
the opened file's identity is checked against its pre-open metadata.

## Sequence framing and bounds

Each image contains one QR symbol with correction level M and this ASCII text:

```text
AIRCQR1:<sha256>:<count>:<index>:<totalbytes>:<base64>
```

The SHA-256 field is the lowercase 64-character hexadecimal digest of the
complete original bundle. Count, index and total bytes use canonical unsigned
decimal without leading zeroes. Index starts at zero. Base64 uses the canonical
standard alphabet and padding. Filenames do not participate in reassembly.

| Boundary | Limit |
| --- | --- |
| Complete reconstructed bundle | 1–65,536 bytes |
| Raw fragment | 512 bytes, except the exact final remainder |
| Images | 1–128, exactly the declared count |
| Input image | One static PNG, at most 4 MiB |
| Image dimensions | Each side 1–2,048 pixels |
| QR symbols per image | Exactly one |
| Codec process lifetime | 30 seconds, including file I/O and diagnostics |

Count must equal the total byte length rounded up to 512-byte fragments. Every
fragment must declare the same digest, count and total. Missing indices,
duplicate indices, mixed sequences, invalid field encodings, wrong fragment
lengths and a mismatched complete digest are refused. Decoding emits no prefix
of an incomplete sequence.

ImageIO inspects format, image count and dimensions before pixel decoding.
Native image processing still allocates SDK buffers within those input bounds;
the image-size limit is not a separate operating-system memory quota. Images
are processed one at a time. No network or camera access is needed.

The digest detects sequence damage and mixing. It is unsigned and does not
authenticate a sender: an attacker can create a different internally consistent
QR sequence. The importer must verify every signed envelope's identity,
audience, target, operation and original validity before opening the journal.
An expired envelope remains expired after encoding, decoding or copying images.

## Loss, retry and interruption

Retain the original images until the local importer reports the outcome. A
missing or damaged image requires replacement; there is no automatic resend or
partial message admission. Successful decoding does not consume or modify the
source images. Retry with the exact retained signed intent and request ID.
To repeat decoding, choose a fresh output filename. To retry admission, reuse
the existing decoded bundle with `airc-gateway import-bundle`.

Reimporting an accepted bundle rejects replay while reporting the original
receipt and send-attempt count. A pending durable request may resume its original
delivery, as defined by the offline importer; a new QR filename or digest cannot
create a new authenticated request identity.

SIGINT, SIGTERM and the process alarm stop the codec. An interrupted encoding
may leave an incomplete output directory, which the decoder will refuse. An
interrupted decode can leave a private temporary file; it never publishes a
partially reconstructed final file. If interruption follows complete publication,
the valid output may already exist. Do not infer importer acceptance from file
existence. Journal and delivery-worker recovery remain the existing importer's
responsibility. No power-loss durability is claimed for the image files.

## Native qualification

The evidence below predates the camera extension and shared assembly refactor.
Current source is compiled; runtime regression and camera acceptance are deferred
by owner direction. It does not establish that the changed binary passes these
earlier checks.

The Darwin ARM64 fixture uses Apple Swift 6.4, macOS 26.6.2 and the macOS 27 SDK.
The actual native codec is built with warnings as errors and AddressSanitizer.
The focused Go race run passed all seven test/subtest cases without skips or
diagnostics in 76.062 seconds. Output is retained in
`/tmp/airc-qr-focused-final-race.jsonl`.

The [full acceptance checkpoint](testing.md#qr-image-sequence-acceptance-checkpoint)
also passed normal/race tests, command builds, formatting, vet and staticcheck
on the unchanged source tree. All seven QR cases passed in each full suite.

The real encoder and decoder preserve arbitrary binary bytes, reconstruct
reordered sequences and round-trip the complete 65,536-byte/128-image limit.
Native image and file controls reject missing, duplicate and mixed fragments,
wrong complete digests, malformed PNGs, a one-frame animated PNG, excessive
dimensions/file size/image count, symlinks and FIFOs. Existing outputs and
source bytes remain intact; decoded publication refuses a group-writable parent.
An independent native QR renderer exercises the digest-refusal boundary.

The original image-count-only check accepted a valid one-frame APNG because
ImageIO reports one image for it. The corrected decoder checks animation keys
in the container's PNG dictionary. The same actual APNG now exits with a static
image refusal and no output. Before/after executable hashes, commands and results
are retained in `/tmp/airc-qr-review.z6XeTm/apng-regression.json`.

Native acceptance encodes two signed messages into PNG sequences, decodes their
reversed image order and runs the real `airc-gateway import-bundle` command.
An independent AIRC observer verifies the exact author, room and Unicode/large
bodies. Original signed expiry is preserved. Fresh-process decoding and replay
return the original receipts with one send attempt and no duplicate native
messages. Expired and signature-tampered envelopes survive QR decoding unchanged
but fail importer authentication before journal creation; a fresh signed control
then succeeds. The importer releases journal ownership after joining delivery.

A separate native process check fills the diagnostic pipe, keeps it unread and
invokes an invalid command. The codec terminates on its own thirty-second
`SIGALRM`, before the test supervisor's deadline. This checks the error-reporting
path as well as successful codec completion. These results cover local image
files and native software clients, not camera or physical optical delivery.
