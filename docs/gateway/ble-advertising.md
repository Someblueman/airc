# Legacy BLE advertising signed-bundle ingress

The selected implementation uses Linux's HCI user channel on a dedicated LE
controller, Python 3.10+ and private 128-bit service data in legacy advertising.
`scripts/ble-advertising-bundle.py` emits or reconstructs one signed bundle;
the existing `airc-gateway import-bundle` command authenticates it and performs
durable native admission. No BLE connection, pairing, GATT or return channel
is used. Extended advertising is a separate roadmap mode.

## Controller provisioning and use

Provision two compatible controllers with stable public Bluetooth addresses.
The helper names one exact numeric HCI index and verifies its public address.
Index reuse or an unexpected address fails. Random/private addresses and address
rotation are outside this profile. The selected controller must be dedicated,
powered down and available for exclusive HCI user-channel ownership before the
helper starts. Provision permission for the Linux raw HCI/user-channel operations
through the service's existing capability policy. Do not grant capabilities to
a general-purpose Python installation or disturb a controller serving other
Bluetooth applications.

The helper does not stop BlueZ, search for another adapter or alter system
service configuration. Binding reserves the selected controller from kernel
protocol processing. After address verification, it resets and configures that
controller for one bounded operation. This is deliberate controller ownership,
not concurrent use of a BlueZ-managed adapter. The public-address check selects
hardware; it does not attest firmware or authenticate an AIRC principal.

Keep `gateway_hci.py` and `gateway_device_io.py` beside the script. Only the
public `read` and `emit` commands are supported; underscore-prefixed commands
are internal subprocess workers. Use actual provisioned public addresses:

```sh
python3 scripts/ble-advertising-bundle.py read \
  --controller 0 --local RECEIVER_PUBLIC_MAC --peer SENDER_PUBLIC_MAC \
  --output /absolute/private/received-bundle.json
```

Start the receiver before the sender, allowing controller setup to complete.
On the other host:

```sh
python3 scripts/ble-advertising-bundle.py emit \
  --controller 0 --local SENDER_PUBLIC_MAC \
  --input /absolute/path/signed-bundle.json
```

MAC arguments contain six uppercase hexadecimal octets separated by colons.
The sender broadcasts; the receiver's exact peer filter is not a secrecy or
authentication boundary. After complete reception:

```sh
airc-gateway import-bundle --file /absolute/path/gateway.json \
  --input /absolute/private/received-bundle.json
```

Use the sole enabled `offline_bundle` ingress with configured sender keys,
native bindings and explicit Send grants from [offline-bundle.md](offline-bundle.md).
Input is a bounded regular file. Output is a fresh mode-0600 file inside a
current-user-owned mode-0700 directory; existing output files are refused.

## Advertising and assembly

The advertiser uses legacy nonconnectable, nonscannable undirected advertising
with a 100 ms interval on all three advertising channels and a public address.
The passive scanner uses equal 10 ms interval/window and disables duplicate
filtering. Only legacy `ADV_NONCONN_IND` reports from the configured public
peer are considered. RSSI does not grant authority. There is no scan response,
local name or Flags element in this nonconnectable profile.

Each advertisement contains exactly 31 data bytes:

| Bytes | Value |
|---|---|
| 0 | AD length 30 |
| 1 | Service Data–128-bit UUID type `0x21` |
| 2–17 | UUID `95a73d2b-a997-451a-9378-2a79c08b1b01`, least-significant byte first |
| 18–19 | Complete stream length, unsigned big-endian |
| 20–21 | Zero-based fragment index, unsigned big-endian |
| 22–30 | Next nine stream bytes, final fragment zero-padded |

This UUID is a private application namespace, not a Bluetooth SIG-assigned
16-bit service or a claim of native Bluetooth profile support. Its exact
value selects this wire format. The complete stream consists of SHA-256 of
the bundle (32 bytes) followed by 1–2048 original signed-bundle bytes. The
maximum stream is 2080 bytes in 232 fragments.

The sender holds each fragment for 220 ms and makes two complete sweeps.
Maximum-size dwell time alone is 102.08 seconds; controller command overhead
also consumes the 120-second operation deadline. This is an implementation
budget, not a measured delivery guarantee. Smaller bundles leave more setup
margin. Repeated broadcasts provide another reception opportunity without
receipts or automatic extension of the deadline.

The receiver can collect fragments out of order and accepts identical repeats.
It keeps only one bounded sequence, rejects mixed lengths, conflicting repeated
fragments, invalid indices and final padding, then checks complete length and
digest. A missing fragment never yields a file. HCI event lengths, per-report
lengths and report counts are checked before accepting service data. Other
peer/service data is ignored within the global event/deadline bounds; malformed
controller reports fail. No unbounded discovery cache or per-device assembly
is created.

HCI commands are serialized and matched to successful command completion.
Each completion must return command credit; unsupported controller scheduling
fails rather than sending beyond its allowance. Individual command waits have
a three-second bound, events a 258-byte bound and each operation a 16,384-event
limit. Cleanup has at most one extra second for disabling the active mode.
The raw controller uses the exclusive user channel, never a shared raw-channel
fallback. A small libc `bind` call supplies Linux's explicit channel field
because Python 3.10's HCI address tuple cannot select it. Events and commands
are bounded and one supervisor deadline covers startup and transfer. Normal
cleanup disables the active radio operation and releases the socket; timeout
or interruption terminates and, if needed, kills and reaps the child. No prior
Bluetooth session is restored. Forced termination and controller loss require
hardware cleanup verification. Output is published only after successful child
exit, so a reported cleanup failure prevents publication.

The stream digest detects corruption but supplies no authentication. All
advertising content and addresses are observable and forgeable. The importer
verifies every original signature, current grant and signed freshness before
admission, with existing stable IDs and durable replay handling. Transport
time does not extend signed validity. Keep the original signed intent on retry.
A successful emit or reassembly is not native AIRC delivery; inspect the local
import report and native receipt. This profile supports Send only.

## Deferred verification

Source review and Python syntax compilation are implementation checks. No
controller, adapter state, radio or AIRC fixture was exercised. Actual HCI
user-channel ownership/permissions, controller command behavior, over-air legacy
frames, packet loss and repetition timing, shutdown and device removal,
signature/grant/freshness/replay refusals and native AIRC delivery remain
unexecuted. Existing GATT evidence cannot qualify advertising.

Primary references: BlueZ [HCI user channel](https://github.com/bluez/bluez/wiki/HCI)
and [HCI command/report definitions](https://github.com/bluez/bluez/blob/master/lib/bluetooth/hci.h),
Bluetooth SIG [advertising data definitions](https://www.bluetooth.com/wp-content/uploads/Files/Specification/HTML/CSS_v12/out/en/supplement-to-the-bluetooth-core-specification/data-types-specification.html),
and [CPython 3.10 HCI binding](https://github.com/python/cpython/blob/3.10/Modules/socketmodule.c).
