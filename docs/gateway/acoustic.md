# Acoustic signed-bundle ingress

The selected acoustic bridge uses Linux, Python 3.10+ and an operator-provisioned
minimodem 0.24 build with ALSA support. It sends one small signed bundle as
audible Bell 202 FSK through an explicitly selected speaker/microphone pair.
`scripts/acoustic-bundle.py` supervises the modem and reconstructs a private
bundle file; the existing `airc-gateway import-bundle` command performs all
sender authentication and durable admission. No audio library is linked into
the gateway and no package, device or mixer is configured by the helper.

Run the helper from the repository with `acoustic_transport.py` and
`gateway_device_io.py` beside it. Provision the native modem executable and its
shared libraries through the operator's trusted package/build process. Supply
the absolute canonical executable path and its expected SHA-256 digest from
that provisioning. The helper checks the opened regular ELF file, size,
owner/permissions, identity and digest before executing the same descriptor.
The executable must belong to root or the current UID, have executable bits,
have no set-ID or group/other write bits, and be at most 16 MiB. Keep it immutable
while a transfer runs. The hash pins the selected executable, not its dynamic
libraries, ALSA configuration or an AIRC sender. Version/build and dependency
provenance remain provisioning responsibilities.

Select the current numeric ALSA card and device as `plughw:N,M`, with canonical
decimal numbers from 0 through 255. There is no `default` device fallback.
ALSA's `plughw` conversion layer is used for the fixed mono sample format/rate;
the operator chooses physical capture/playback routing and grants device access.
Card numbers may change after reconnection. Retain a trusted ALSA configuration
and ensure the selected input is the intended microphone. A software loopback
or audio cable does not establish acoustic propagation through air.

Start the receiver before the sender. Substitute the provisioned modem's exact
digest and each endpoint's actual ALSA device in these commands:

```sh
python3 scripts/acoustic-bundle.py read \
  --modem /usr/bin/minimodem --sha256 EXPECTED_MODEM_SHA256 \
  --device plughw:0,0 --output /absolute/private/received-bundle.json
```

On the sender:

```sh
python3 scripts/acoustic-bundle.py emit \
  --modem /usr/bin/minimodem --sha256 EXPECTED_MODEM_SHA256 \
  --device plughw:0,0 --input /absolute/path/signed-bundle.json
```

Then authenticate and import the received file:

```sh
airc-gateway import-bundle --file /absolute/path/gateway.json \
  --input /absolute/private/received-bundle.json
```

Use the existing sole enabled `offline_bundle` listener, sender signing keys,
native bindings and Send target grants from [offline-bundle.md](offline-bundle.md).
Input is a bounded regular file; output must be a fresh mode-0600 file in a
current-user-owned mode-0700 directory. Existing files and final-component
symlinks are refused. The opaque input is not authenticated by the emitter.

## Carrier and framing

The fixed modem settings are 1200 baud, eight data bits, one start bit, one stop
bit, 1200 Hz mark, 2200 Hz space and 48 kHz mono audio. Transmission amplitude
is 0.5; actual speaker volume is operator-controlled. No inversion or sync-byte
protocol is added. The receiver uses `--rx-one` and accepts the decoded output
only after the modem exits successfully. That flag ends the first carrier/loss
event; it does not assert that an application message was complete.

Decoded bytes must contain exactly this ASCII line, including one final LF:

```text
AIRCA1:<length>:<sha256>:<base64>\n
```

The payload is 1–2048 opaque bytes of the existing signed-bundle JSON. Length
is canonical unsigned decimal, digest is 64 lowercase SHA-256 hexadecimal
characters, and base64 uses the canonical standard alphabet and padding.
Prefix/suffix noise, extra lines, malformed encoding, a wrong length or digest,
and truncated carrier output all fail without publishing a bundle. The receiver
caps stdout at 3000 bytes; a valid maximum-size line is 2810 bytes. That line
takes about 23.4 seconds at 1200 baud/8N1 before carrier and device overhead,
so use smaller bundles when more acquisition time is needed.

Each modem operation is supervised for 30 seconds, including executable checks
and audio setup. The helper replaces its child with minimodem using descriptor
execution; it does not leave a nested modem process. Timeout, interruption or
oversized output terminates, then kills if necessary, and reaps that child.
The transmitter's bounded frame is provided through an anonymous memory file.
No microphone recording is stored. Noise or carrier loss can reject an attempt;
there is no automatic resynchronization, retry or partial admission.

This profile is Send-only and provides no acoustic response channel. The line
digest detects corruption but does not authenticate a sender or hide content.
The importer verifies every signature, current grant and original freshness
before journal admission; it preserves stable IDs and durable replay handling.
Retain the original signed intent on retries. A successful audio write or decode
does not prove AIRC delivery: inspect the local import report and native receipt.

## Deferred verification

Python syntax compilation and source review cover the helper. Gateway production
builds remain applicable because its Go source is unchanged. No minimodem binary,
ALSA backend, microphone, speaker, refusal/replay path or native AIRC delivery
was executed. Later acceptance needs a real acoustic link and independent AIRC
observer, plus exact device selection, binary pin refusals, loss/noise,
frame/freshness bounds, replay and child shutdown. Range, throughput and error
rate are unqualified. See [ROADMAP.md](ROADMAP.md).

The chosen CLI is documented in the [minimodem 0.24 manual](https://manpages.debian.org/testing/minimodem/minimodem.1.en.html).
The [upstream modem](https://github.com/kamalmostafa/minimodem/blob/master/src/minimodem.c)
and [ALSA backend](https://github.com/kamalmostafa/minimodem/blob/master/src/simpleaudio-alsa.c)
define the acquisition and device behavior; the [release list](https://github.com/kamalmostafa/minimodem/tags)
identifies the `minimodem-0.24-1` source tag. The helper installs none of them.
