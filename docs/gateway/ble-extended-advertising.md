# BLE extended-advertising signed-bundle ingress

The selected extended-advertising profile uses Linux's exclusive HCI user
channel and Bluetooth 5 controllers with extended advertising support.
`scripts/ble-advertising-bundle.py --mode extended` uses the same bounded
signed-bundle importer as [legacy advertising](ble-advertising.md), with a
distinct wire UUID, HCI commands and receive-report parser. It requires actual
extended data; no fallback to legacy advertising is attempted.

## Provisioning and commands

Use dedicated, initially powered-down controllers with stable public addresses
and provisioned HCI permissions. The [legacy controller ownership requirements](ble-advertising.md#controller-provisioning-and-use)
apply: no competing BlueZ use, no automatic controller selection and no changes
to system services or permissions. The helper checks the local public address
before reset, then requires LE extended-advertising feature bit 12, reported
advertising-data capacity of at least 202 bytes and at least one advertising
set. Unsupported capabilities or commands fail before transfer.

Start the receiver first, using its actual index/address and the actual sender
public address:

```sh
python3 scripts/ble-advertising-bundle.py read --mode extended \
  --controller 0 --local RECEIVER_PUBLIC_MAC --peer SENDER_PUBLIC_MAC \
  --output /absolute/private/received-bundle.json
```

After receiver setup, emit from the other host:

```sh
python3 scripts/ble-advertising-bundle.py emit --mode extended \
  --controller 0 --local SENDER_PUBLIC_MAC \
  --input /absolute/path/signed-bundle.json
```

Then authenticate and import on the receiver:

```sh
airc-gateway import-bundle --file /absolute/path/gateway.json \
  --input /absolute/private/received-bundle.json
```

Use the existing sole enabled `offline_bundle` ingress, signing keys, native
bindings and explicit Send grants. Input is a regular file containing 1–2048
opaque bytes; output must be fresh, mode 0600, inside a current-user-owned
mode-0700 directory. Only public `read`/`emit` commands supply the supervised
operation and private publication contract.

## Extended radio and wire format

The helper uses advertising handle zero, SID zero, LE 1M primary and secondary
PHYs, all primary channels, 100 ms intervals and a public address. Event
properties select nonconnectable, nonscannable, undirected extended advertising.
Data is supplied as one complete HCI update with fragmentation discouraged;
controller-selected transmit power is used. Scan setup is passive LE 1M with
equal 10 ms interval/window and duplicate filtering disabled.

Each advertisement has exactly 202 data bytes:

| Bytes | Value |
|---|---|
| 0 | AD length 201 |
| 1 | Service Data–128-bit UUID type `0x21` |
| 2–17 | Private UUID `95a73d2b-a997-451a-9378-2a79c08b1b02`, least-significant byte first |
| 18–19 | Complete stream length, unsigned big-endian |
| 20–21 | Zero-based fragment index, unsigned big-endian |
| 22–201 | Next 180 stream bytes; final unused bytes are zero |

The stream is SHA-256 of the bundle followed by the original bundle, at most
2080 bytes in 12 fragments. The sender makes two sweeps, holding each fragment
for 220 ms: maximum-size dwell time is 5.28 seconds before command overhead.
The complete operation has a 30-second supervisor/controller deadline.

Reception requires HCI LE Extended Advertising Report subevent `0x0D`, the
exact configured public peer, event properties zero, primary/secondary PHY 1,
SID zero and periodic interval zero. Event properties zero explicitly require
complete extended data, with no legacy, connectable, scannable, directed or
scan-response flags. Direct-address fields are ignored for this undirected
profile, as required by HCI. The parser validates every length and count in
coalesced reports before yielding records. One 202-byte report needs 228 bytes
of LE meta-event payload and fits the 255-byte event limit.

The selected profile does not join HCI-level incomplete/truncated report
fragments. Recognized private data with those status flags fails; it cannot
be accepted as a complete application record. Application fragments may arrive
out of order; only identical duplicates are accepted. Bounded slots, exact
last-fragment padding, total length and the complete SHA-256 check are shared
with legacy mode. Missing, conflicting or mixed data produces no bundle file.

Controller commands have three-second waits and one outstanding request with
explicit completion/credit checks. Events stay within the shared 258-byte and
16,384-event caps. Cleanup uses the extended disable commands with a one-second
bound and releases the exclusive socket; Linux closes the user-owned controller.
The parent terminates, then kills if needed, and reaps timed-out children.
Publication requires successful child exit, including normal cleanup. No prior
Bluetooth session is restored.

Broadcast addresses, UUIDs and digests are not authentication, and contents
are observable. Every original signature, grant and signed validity interval
is checked by the existing importer before durable admission; transport does
not refresh an intent. Retain original request IDs on retries. There is no
application return channel or radio receipt. Inspect the import report/native
receipt to distinguish durable intake and native AIRC delivery from reception.

## Deferred verification

Source review and Python syntax compilation do not establish hardware support
or interoperability. No HCI command or device was executed during development.
Real extended-capability refusals, over-air primary/secondary packets, complete
report behavior, loss/repetition timing, removal/cleanup, native sender/target
delivery and signature/freshness/replay acceptance remain unexecuted. Legacy
advertising, GATT and loopback evidence cannot qualify this distinct mode.

Primary references: Linux [HCI command and report structures](https://github.com/torvalds/linux/blob/master/include/net/bluetooth/hci.h),
Nordic [extended-report fields](https://nrfconnectdocs.nordicsemi.com/ncs/latest/nrfxlib/doxygen/html/structsdc__hci__subevent__le__ext__adv__report__t.html),
and Bluetooth SIG [service-data definitions](https://www.bluetooth.com/wp-content/uploads/Files/Specification/HTML/CSS_v12/out/en/supplement-to-the-bluetooth-core-specification/data-types-specification.html).
