# LoRa signed-bundle ingress

The chosen point-to-point LoRa bridge uses Linux, Python 3.10+ and REYAX RYLR998
radios with command-mode firmware. `scripts/lora-bundle.py` transfers one
1–2048-byte signed bundle in bounded ASCII fragments. The existing
`airc-gateway import-bundle` command verifies the reconstructed bundle and
performs native admission. This direct LoRa path is separate from the
[ChirpStack LoRaWAN integration](chirpstack.md).

Run the helper with `gateway_serial.py` and `gateway_device_io.py` beside it.
It uses the standard library and shares the bounded UART owner with the Zigbee
helper. No radio SDK, firmware installation or persistent configuration write
is performed. Use the public `read`/`emit` commands; underscore-prefixed worker
entrypoints are internal subprocess plumbing and must not be invoked directly.

## Provisioning and use

Provision paired RYLR998 command-mode radios and a compatible 3.3 V UART
connection. The selected UART is 115200 8N1 without flow control. The tty must
be an absolute canonical path without symlinks, current-user-owned and mode
0600, with no competing console or serial owner. Actual electrical power,
antenna, frequency, RF power and permitted transmission schedule belong to
device provisioning; there is no automatic frequency/power selection.

Record the actual 12-byte radio UID and exact `RYLR998_V...` firmware string
from trusted provisioning. The helper reads and compares them at startup.
It also queries UART baud, mode zero, the configured local address, network,
frequency and transmit power, and requires RF parameters `8,7,1,12` (SF8,
125 kHz, coding rate 4/5, preamble 12). The supplied band and power must match
the device; hardware-range validation is not a regional authorization check.
The selected profile uses distinct addresses 1–65535, avoiding broadcast zero,
and network 3–15 or 18. These are routing parameters, never sender credentials.
UART activity can wake a sleeping module; provision normal transceiver mode
and quiet radios before invocation. Query replies, firmware and UID are not
cryptographic hardware attestation.

Start the receiver first and allow its provisioning queries to finish. Replace
all capitalized placeholders with the actual provisioned values (numeric
fields require decimal integers):

```sh
python3 scripts/lora-bundle.py read \
  --device /dev/ttyUSB0 --uid RECEIVER_UID --firmware RYLR998_VEXACT_VERSION \
  --local RECEIVER_ADDRESS --peer SENDER_ADDRESS --network NETWORK \
  --band FREQUENCY_HZ --power POWER_DBM \
  --output /absolute/private/received-bundle.json
```

On the sender, use that radio's UID, firmware and power:

```sh
python3 scripts/lora-bundle.py emit \
  --device /dev/ttyUSB0 --uid SENDER_UID --firmware RYLR998_VEXACT_VERSION \
  --local SENDER_ADDRESS --peer RECEIVER_ADDRESS --network NETWORK \
  --band FREQUENCY_HZ --power POWER_DBM \
  --input /absolute/path/signed-bundle.json
```

Import on the receiving host:

```sh
airc-gateway import-bundle --file /absolute/path/gateway.json \
  --input /absolute/private/received-bundle.json
```

Use the sole enabled `offline_bundle` ingress, sender keys, native bindings and
explicit Send grants in [offline-bundle.md](offline-bundle.md). The input is a
bounded regular file. The fresh output file has mode 0600 and requires a
current-user-owned mode-0700 directory. Existing output files are refused.
The emitter does not authenticate input; only the importer grants native access.

## Application framing and bounds

Each `AT+SEND` payload is this ASCII record, without an embedded line ending:

```text
ALR1:LLLL:II:sha256:base64
```

`LLLL` is the total decoded bundle size in four uppercase hexadecimal digits;
`II` is the zero-based index in two uppercase hexadecimal digits. The digest
is the complete bundle's 64 lowercase SHA-256 hexadecimal characters. Each
chunk carries 96 raw bytes, except the final shorter chunk, using canonical
standard base64. The maximum record is 206 bytes; a maximum bundle takes 22
records. All records must agree on size/digest and arrive in strict order.
Missing, duplicate, mixed, reordered or corrupt records fail the attempt.
The last chunk must have exactly the remaining length.

The sender issues one unicast send command at a time and waits for `+OK`.
Receive lines must have the `+RCV` source, declared byte count, record and
numeric RSSI/SNR fields. The parser accepts bounded separator spaces, verifies
the exact configured peer and payload size, and ignores signal measurements
for authentication. Commands end in CRLF; input lines must also end in CRLF,
contain printable ASCII and stay below 320 content bytes. At most two empty
separator lines are skipped. Unexpected commands, resets, errors or packets
fail; no general AT console, retry or resynchronization is exposed.

One 30-second child deadline covers device setup and the entire transfer.
Serial buffers and application assembly are bounded. The UART owner flushes
previous buffered input, takes an advisory lock and kernel exclusivity, checks
device identity during I/O and attempts to restore original termios on close.
Timeout/interruption terminates, then kills if necessary, and reaps the child.
Forced kill cannot guarantee termios restoration. Use ready, quiet radios
after an interrupted command; AT responses carry no transaction ID. The
receiver publishes only after complete assembly, a matching digest and
successful child exit/normal UART restoration.

This profile provides Send-only application ingress. `+OK`, a UART write and
radio reception do not establish AIRC delivery. The digest detects corruption;
source addresses, network numbers and the optional radio domain password do
not authenticate an AIRC sender. The importer verifies every signature,
current grant and original freshness and retains durable replay handling.
Signed contents are not end-to-end confidential. Retain original signed intents
on retry and inspect the local import report/native receipt for the outcome.

## Deferred verification

Python syntax compilation and source review are complete. No radio, UART,
firmware or frequency was exercised or changed during implementation. Later
acceptance must cover the exact provisioned firmware's AT replies, packet
timing and physical RF path, corruption/loss/peer refusals, process and tty
cleanup, original signatures/grants/freshness/replay and native AIRC delivery.
The fixed deadline is a resource bound, not a measured maximum-size throughput
claim. No native or radio acceptance is inferred from compilation.

Primary references: REYAX [RYLR998/RYLR498 AT command guide](https://reyax.com/upload/products_download/download_file/LoRa_AT_Command_RYLR998_RYLR498_EN.pdf)
and [RYLR998 datasheet](https://reyax.com/upload/products_download/download_file/RYLR998_EN.pdf).
