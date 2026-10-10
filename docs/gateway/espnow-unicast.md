# ESP-NOW unicast signed-bundle ingress

The chosen bridge uses a dedicated classic ESP32, MicroPython 1.26.0 and a
Linux Python 3.10+ host. `native/espnow/bridge.py` runs on the board;
`scripts/espnow-bundle.py` transfers a 1–2048-byte signed bundle over its
separate UART. The receiver uses the existing `airc-gateway import-bundle`
command for native admission. This mode is Send-only.

## Provisioning and use

Provision each ESP32 with the pinned MicroPython version, the bridge as
`main.py`, and a private `espnow_config.py` containing these constants:

| Constant | Provisioned value |
|---|---|
| `LOCAL_MAC` | Board's actual station MAC, six bytes |
| `PEER_MAC` | Other board's station MAC, six bytes |
| `CHANNEL` | Same permitted 2.4 GHz channel, integer 1–11 |
| `ROLE` | `"read"` on the receiver, `"emit"` on the sender |
| `PMK` | Independently provisioned 16-byte ESP-NOW primary key |
| `LMK` | Pair-specific 16-byte ESP-NOW local key |
| `AUTH_KEY` | Separate random 32-byte application authentication key |

The pair shares the three keys. Generate independent keys; do not reuse a
native AIRC signing key, use example passwords, or commit device secrets.
Configuration and firmware provenance, board flash access, antenna and legal
radio settings are provisioning responsibilities. Provisioning/flashing is
separate from invoking the helper. No SDK or firmware is downloaded by it.

Use UART1 on GPIO33 TX and GPIO32 RX, 115200 8N1 with no flow control, and a
compatible 3.3 V USB-to-TTL adapter with common ground. UART0 is the board
console, not this protocol. Dedicate the board to the bridge: its station and
access-point interfaces are disabled at startup, and it does not retain a
normal Wi-Fi connection. Do not connect the adapter's reset/boot control lines.

The Linux tty must be an absolute canonical nonsymlink path, owned by the
current UID with mode 0600 and no other console/device owner. Keep
`gateway_serial.py` and `gateway_device_io.py` beside the helper. Start on
freshly booted, provisioned boards. Start the receiver first and allow it to
configure before starting the sender. Replace the placeholders with actual
uppercase colon-separated MACs and the decimal channel:

```sh
python3 scripts/espnow-bundle.py read \
  --device /dev/ttyUSB0 --local RECEIVER_MAC --peer SENDER_MAC \
  --channel CHANNEL --output /absolute/private/received-bundle.json
```

On the sending host:

```sh
python3 scripts/espnow-bundle.py emit \
  --device /dev/ttyUSB0 --local SENDER_MAC --peer RECEIVER_MAC \
  --channel CHANNEL --input /absolute/path/signed-bundle.json
```

On successful reception:

```sh
airc-gateway import-bundle --file /absolute/path/gateway.json \
  --input /absolute/private/received-bundle.json
```

Configure the sole enabled `offline_bundle` ingress, sender keys, native
bindings and explicit Send grants described in [offline-bundle.md](offline-bundle.md).
The emitter reads a bounded regular file; it does not authenticate it. The
receiver creates a fresh mode-0600 file in a current-user-owned mode-0700
directory and refuses overwriting. Only the importer authorizes native access.
Underscore-prefixed helper commands are internal subprocess entrypoints.

## Fixed UART and radio protocols

All multibyte integers below are big-endian. One UART request contains `AEU1`,
a fresh 16-byte host nonce, local MAC, peer MAC, one-byte channel, one-byte
operation (`R` receive or `T` transmit), two-byte bundle length and 32-byte
SHA-256 digest: 68 bytes total. Receive requests use zero length/digest. The
board checks the request against its provisioned role and peer configuration.
It verifies the station MAC, selected channel, encrypted peer settings and
single registered peer before returning readiness.

A UART reply has `AEU1`, the echoed nonce, one-byte status, two-byte length and
32-byte digest: 55 bytes. Ready `R`, error `E` and successful transmit `O`
have zero length/digest. After readiness, the transmitter receives the exact
requested bytes from the host and checks the digest. Successful receive `O`
carries the assembled bundle after the header, with its length/digest. Unknown
status, stale nonce, malformed length or digest fails. Nonces correlate this
local transaction; they are not firmware or hardware attestation.

Each unicast radio payload is exactly 248 bytes: `AEU1`, two-byte total bundle
length, two-byte zero-based index, 32-byte bundle SHA-256, a 176-byte chunk
(last chunk zero-padded), then a 32-byte HMAC-SHA256 tag. The HMAC input is
`airc-espnow-unicast-v1` followed by a zero byte, sender MAC, receiver MAC,
one-byte channel and the preceding 216 record bytes. It uses `AUTH_KEY`.
At most 12 records carry one bundle. The receiver checks the configured source
and HMAC before assembling strict consecutive indices with identical totals
and digest. Missing, duplicate, mixed, reordered, bad-padding or corrupt
records fail the attempt. There is no automatic replay or reassembly cache.

The board sets the provisioned PMK and registers only its peer with LMK
encryption enabled. MicroPython receive metadata does not prove a packet was
encrypted; the separate keyed record tag establishes provisioned peer trust
even if the radio accepts a plaintext packet from a spoofed MAC. Neither that
key nor the radio MAC grants an AIRC identity. The importer still checks every
original signature, current grant and freshness bound and applies durable
replay handling. The bundle is signed, not end-to-end encrypted; provisioned
bridge endpoints can read it.

## Ownership and bounds

The host gives the child 30 seconds for the whole operation. Its shared UART
owner flushes stale input, takes advisory/kernel exclusivity, checks tty
identity during I/O and attempts to restore original termios. Interruptions
terminate, then kill if needed, and reap the child. Forced kill cannot promise
termios restoration. Received output is published only after the final reply,
matching digest and successful child exit with normal UART cleanup.

The board leaves its radio off while waiting for the first request byte. That
byte starts a 25-second transaction deadline and a non-fed 30-second watchdog.
One operation runs per boot; the watchdog resets and rearms the board after
the operation. Wait for that reset or reset the board before another attempt.
The UART has bounded buffers; the ESP-NOW receive ring is 4096 bytes, the
receive buffer 250 bytes, and at most 256 receive calls are made, including
timeouts and zero-length packets. The
MicroPython RSSI peer dictionary is read-only: startup requires an empty table,
and the receive limit bounds it to 256 entries before the watchdog's hard
reset clears the heap. A soft restart with retained peer entries is refused.
The board disables ESP-NOW and its station interface before a successful
final reply, then flushes/deinitializes UART. The watchdog also bounds a stuck
native driver call. Host interruption cannot instantly stop a remote board;
it may finish its current radio operation before its own deadline/reset.

Synchronous ESP-NOW send completion is only a MAC acknowledgement, and can
precede an application read or accompany a dropped receive-buffer packet.
It is never an importer receipt. Retain the original signed intent on retry
and inspect the local import report/native receipt to resolve delivery.

## Deferred verification

Host and firmware source received syntax compilation and review only. CPython
syntax compilation does not qualify MicroPython execution. No UART, ESP32,
firmware, watchdog, key provisioning or RF path was exercised or installed.
Later acceptance must cover actual paired devices, maximum-size timing,
wrong peer/key/configuration, malformed/lost/replayed records, RX pressure,
interruption/reset/cleanup and independent observation of authorized native
AIRC delivery. The deadlines are bounds, not throughput measurements.

Primary references: MicroPython 1.26.0 [ESP-NOW](https://docs.micropython.org/en/v1.26.0/library/espnow.html),
[UART](https://docs.micropython.org/en/v1.26.0/library/machine.UART.html),
[ESP32 pin and UART configuration](https://docs.micropython.org/en/v1.26.0/esp32/quickref.html)
and [watchdog](https://docs.micropython.org/en/v1.26.0/library/machine.WDT.html).
The [pinned ESP-NOW implementation](https://github.com/micropython/micropython/blob/v1.26.0/ports/esp32/modespnow.c)
defines the read-only RSSI table and its update on receive.
