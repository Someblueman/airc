# Optical QR sequence capture

The selected physical optical receiver uses a built-in or external camera on
Darwin ARM64 with macOS 14 or later. `qr-tool capture` reads a sequence of
displayed or printed `AIRCQR1` symbols through AVFoundation and Vision, then
publishes the reconstructed signed bundle for `airc-gateway import-bundle`.
This extends the existing [QR image sequence](qr-sequence.md); it adds no
listener, signing format or identity store.

Build the native tool using the installed Apple SDK:

```sh
./scripts/build-qr-tool.sh --output /absolute/path/qr-bin
/absolute/path/qr-bin/qr-tool cameras
/absolute/path/qr-bin/qr-tool capture --device EXACT_CAMERA_ID \
  --output /absolute/private/received-bundle.json
airc-gateway import-bundle --file /absolute/path/gateway.json \
  --input /absolute/private/received-bundle.json
```

The camera list returns JSON with `uniqueID` and `localizedName` fields. Select
the exact identifier of the intended camera. A missing camera fails; there is
no automatic replacement. An identifier selects hardware and grants no AIRC
identity. The receiver uses only video and does not open a microphone or save
camera frames. Capture requests the operating system's camera authorization;
denied or restricted access fails. The build embeds `NSCameraUsageDescription`
and a bundle identifier in the executable using Apple's documented
[command-line Info.plist section](https://developer.apple.com/documentation/xcode/build-settings-reference#Create-Info.plist-Section-in-Binary).
Keep a stable build/launch location
for operator provisioning. Camera permission behavior for that exact launch
path remains part of deferred native acceptance; no prompt was exercised.

Use `qr-tool encode` to create the sender's PNG sequence from an existing
signed bundle. Display the symbols clearly, one at a time, including their
quiet borders. Holding or redisplaying a symbol is permitted. The capture
window is bounded, so choose a small bundle and preserve the original sequence
if another attempt is needed. No minimum optical rate or maximum range is
claimed. Lighting, focus, display resolution, movement and printed-symbol
quality determine whether Vision can decode a frame.

The receiver selects a bounded video format, drops late frames, and processes
one frame at a time on a serial callback queue. Frames exceeding 1280×720 or
the 4 MiB pixel-buffer bound fail. A frame without a QR symbol is skipped;
multiple QR symbols, malformed protocol text, mixed sequences and conflicting
copies of an index fail. Byte-identical repeated fragments are allowed so a
stationary symbol does not create an error. These camera repeats do not relax
the image-file decoder's duplicate-input refusal.

The existing format bounds remain: at most 128 fragments, 512 raw bytes per
fragment and 65,536 reconstructed bytes. Every part must agree on count, length
and digest. The complete sequence is checked again before private, nonoverwriting
file publication. Missing parts or a wrong digest produce no final bundle.
Camera authorization, startup and sequence waits share a 25-second deadline;
the existing 30-second process alarm bounds synchronous framework setup/stop,
stalls, publication and diagnostics.
Normal completion stops capture and drains callbacks before returning. Process
termination releases capture resources; interrupted publication has the same
temporary-file boundary as the image-file decoder.

This is a one-way, Send-only application profile. There is no optical receipt
or conversation-read channel. The digest detects damage and mixing, but is not
authentication. The [offline importer](offline-bundle.md) verifies each sender
signature, current grants, target and original freshness before opening the
journal, then applies durable request IDs and replay rules. Optical encoding
or a new capture file cannot renew an expired intent. Signed contents remain
visible to an observer; this profile adds no confidentiality.

Implementation is source-reviewed and compiled with the native Swift build.
Runtime testing is deferred. The earlier image-file qualification does not
cover camera access, permissions, frame sizing, physical optical reception,
loss, deadline/shutdown behavior or native AIRC delivery through this path.
Later acceptance must use a real camera and independent native AIRC observer,
including complete sequences, malformed/conflicting fragments, expired or
tampered envelopes, replay and interrupted capture/publication.

Primary API references: [camera authorization](https://developer.apple.com/documentation/avfoundation/requesting-authorization-to-capture-and-save-media),
[camera usage description](https://developer.apple.com/documentation/bundleresources/information-property-list/nscamerausagedescription),
[device identity](https://developer.apple.com/documentation/avfoundation/avcapturedevice/uniqueid)
and [late-frame handling](https://developer.apple.com/documentation/avfoundation/avcapturevideodataoutput/alwaysdiscardslatevideoframes).
