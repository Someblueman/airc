# Zigbee signed-bundle ingress

The chosen bridge is Linux with Python 3.10+ and provisioned Digi XBee 3 Zigbee
radios connected through UART. `scripts/zigbee-bundle.py` carries one bounded
signed bundle using escaped API mode 2. The existing `airc-gateway import-bundle`
command authenticates the reconstructed file and performs durable admission.
The helper uses the Python standard library, `xbee_api.py` and
`gateway_device_io.py`; no radio SDK or serial package is required.

## Provisioning and use

Provision actual XBee 3 Zigbee firmware, a coordinator/network, compatible radio
settings and network keys through the operator's trusted process. Each host
UART uses 115200 baud, eight data bits, no parity, one stop bit and no hardware
or software flow control. Select an absolute canonical device path without
symlinks. The character device must belong to the current UID with mode 0600.
Do not share it with another serial application.

Each invocation checks the local EUI64 through `SH`/`SL`, then reads these local
AT values: `AP=2`, `AO=1`, `EE=1`, `AI=0`, `D6=0`, `D7=0` and `SM=0`. These select
escaped API, explicit receive frames, network encryption, joined association,
disabled RTS/CTS and disabled sleep. `D7` normally defaults to enabled CTS and
must be provisioned explicitly. `NP` must report capacity for at least 64 bytes.
Queries have no parameters: the helper does not configure, apply, save, flash
or join a network. Firmware/model provenance and correct UART baud remain
provisioning responsibilities. An unexpected startup or modem-status frame
fails the attempt; begin with ready, quiet radios.

Use the exact 16 uppercase hexadecimal digits of each radio's EUI64. Zero and
broadcast addresses are refused, and local and peer must differ. Start the
receiver first, allowing its local configuration queries to complete before
transmitting. Substitute the actual device and EUI64 values:

```sh
python3 scripts/zigbee-bundle.py read \
  --device /dev/ttyUSB0 --local RECEIVER_EUI64 --peer SENDER_EUI64 \
  --output /absolute/private/received-bundle.json
```

On the other host:

```sh
python3 scripts/zigbee-bundle.py emit \
  --device /dev/ttyUSB0 --local SENDER_EUI64 --peer RECEIVER_EUI64 \
  --input /absolute/path/signed-bundle.json
```

Then authenticate and import on the receiver:

```sh
airc-gateway import-bundle --file /absolute/path/gateway.json \
  --input /absolute/private/received-bundle.json
```

Use the sole enabled `offline_bundle` ingress, sender keys, native bindings and
explicit Send grants in [offline-bundle.md](offline-bundle.md). Input is a
bounded regular file. Output is a fresh mode-0600 file inside a current-user-owned
mode-0700 directory; existing output files are refused. The emitter treats the
input as opaque bytes and does not authenticate it.

## Radio and application framing

Explicit unicast API frame `0x11` uses source/destination endpoint `0xE8`, cluster
`0x0011` and Digi profile `0xC105`, the ordinary serial-data application address.
It uses the exact peer EUI64, unknown short address `0xFFFE`, default radius and
options zero. Each packet waits for a successful `0x8B` status before the next
packet, matching a nonzero frame ID never reused within the invocation. Initial
buffered serial input is discarded; the initial ID is randomized. The 8-bit API
does not supply a cross-process epoch: after an interrupted transaction, ensure
the radio is quiet/reset before starting again. Late status from an older run
cannot establish native acceptance. The receiver requires `0x91`, the exact source EUI64 and application
address, and exactly 64 payload bytes. It accepts either value of receive-option
bit 0, without inferring authority or delivery from it, and rejects all other
bits (broadcast, APS/session options and reserved flags). Dynamic 16-bit source
addresses are not used for identity. Other radio frames fail the attempt.

Each fixed 64-byte application record contains:

| Bytes | Value |
|---|---|
| 0–3 | ASCII `AZB1` |
| 4–5 | Complete bundle length, unsigned big-endian, 1–2048 |
| 6–7 | Zero-based record index, unsigned big-endian |
| 8–39 | SHA-256 of the complete bundle |
| 40–63 | Next 24 bundle bytes; zero padding only at the end |

At most 86 records are accepted, starting at index zero in strict order. Mixed,
missing, duplicated, reordered, corrupt or nonzero-padded records fail without
publishing a bundle. API2 escaping covers every byte after the delimiter;
length and checksum use unescaped data. The parser bounds each API frame at
256 bytes and pre-delimiter noise at 4096 bytes. `NP` is an API payload bound,
not proof that 64 bytes fit one RF frame: firmware fragmentation and routing
overhead remain part of radio acceptance.

The child owns an exclusive nonblocking tty, checks its identity around frame
operations and attempts to restore original termios and release exclusivity
on exit. One 30-second supervisor deadline covers setup and the whole sequence.
Timeout or interruption terminates, then kills if necessary, and reaps the
child. Forced kill cannot guarantee restoration of tty settings; re-provision
the device after a failed cleanup. Output is published only after successful
child exit, including normal UART restoration. There is no automatic retry or
resynchronization.

This is Send-only application ingress. Radio acknowledgements establish no
AIRC acceptance and there is no AIRC response channel. EUI filtering, network
encryption and the record digest do not replace sender signatures. The importer
checks every signature, current grant and original freshness before admission
and preserves stable IDs and durable replay handling. Signed bundle contents
are not end-to-end confidential. Keep original signed intents on retries and
inspect the local import report and native receipt for delivery outcomes.

## Deferred verification

Source review and Python syntax compilation are implementation checks only.
No UART, radio, keys or network were opened or configured during implementation.
Real XBee firmware/API interoperability, encryption and peer refusals, loss and
fragmentation, interruption/restoration, signature/grant/freshness/replay bounds
and native AIRC delivery remain unexecuted.

Primary protocol references: Digi [explicit transmit frame](https://docs.digi.com/resources/documentation/digidocs/90001539/reference/r_frame_0x11.htm),
[explicit receive frame](https://docs.digi.com/resources/documentation/digidocs/90001539/reference/r_frame_0x91.htm),
[transmit status](https://docs.digi.com/resources/documentation/digidocs/90001539/reference/r_frame_0x8b.htm),
[API2 escaping](https://docs.digi.com/resources/documentation/digidocs/90001539/reference/r_ap_2.htm),
[sleep mode](https://docs.digi.com/resources/documentation/digidocs/90001539/reference/r_cmd_sm.htm),
and [RF payload limits](https://docs.digi.com/resources/documentation/digidocs/90001539/reference/r_zb_max_rf_payload_size.htm).
