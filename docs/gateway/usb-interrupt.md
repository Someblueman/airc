# USB interrupt input reports carrying signed bundles

The selected API is Linux AMD64/ARM64 `hidraw` on the host and `usb_f_hid` on a
Linux AMD64/ARM64 USB gadget. `scripts/usb-hid-bundle.py` sends and receives one bounded signed bundle
using fixed vendor HID input reports. The received file then enters the existing
[offline signed-bundle importer](offline-bundle.md). Python 3.10+ and the Linux
drivers are required; the helper has no third-party Python dependency.

This application profile is send-only, gadget to host. It has no AIRC response
reports or conversation reads. A local import report distinguishes durable
intake from native acceptance. USB still has its normal bidirectional control
traffic; this is not a physical data diode. The
[hidraw API](https://docs.kernel.org/hid/hidraw.html) identifies host reads as
reports arriving on the interrupt IN endpoint. The
[HID gadget API](https://docs.kernel.org/usb/gadget_hid.html) exposes report I/O
through `/dev/hidgN`.

## Exact gadget profile

Provision a real device-controller board and USB host. The operator owns gadget
creation, USB descriptors, controller binding and udev permissions. Configure a
`hid` function under `/sys/kernel/config/usb_gadget/NAME/functions/hid.NAME`
with `protocol=0`, `subclass=0`, `report_length=64`, `no_out_endpoint=1`, and
these exact 21 binary report-descriptor bytes:

```text
06 00 ff 09 01 a1 01 15 00 26 ff 00 75 08 95 40 09 01 81 02 c0
```

This declares a vendor-defined usage page, one application collection and an
unnumbered 64-byte input report. It declares no keyboard, mouse, output or
feature report. Keep the configfs function root-owned and unwritable by other
users. Device nodes used by the helper must be current-user-owned mode 0600.
No helper command changes permissions, attaches drivers, creates gadgets or
reconfigures an endpoint.

The sender verifies those function attributes and maps its read-only `dev`
major/minor attribute to the opened `/dev/hidgN` node before writing. The host
requires an explicitly selected `/dev/hidrawN`, USB bus type and the exact HID
report descriptor through cached hidraw ioctls. Both ends reject symlinks,
changed device inodes and mismatched device permissions and take an exclusive
advisory lock. Kernel support for the required attributes must be present;
missing attributes fail instead of selecting another mode. See the
[kernel HID function implementation](https://github.com/torvalds/linux/blob/master/drivers/usb/gadget/function/f_hid.c).

## Transfer and import

Prepare an ordinary version-1 offline JSON bundle containing signed Send
envelopes. The complete JSON file must be 1–16384 bytes. Start the host reader
first, then run the sender within the reader's 30-second window:

```sh
# USB host
python3 scripts/usb-hid-bundle.py read --device /dev/hidraw0 \
  --output /absolute/private/received-bundle.json

# Linux USB gadget, in another terminal/host
python3 scripts/usb-hid-bundle.py emit \
  --function /sys/kernel/config/usb_gadget/airc/functions/hid.airc \
  --input /absolute/path/signed-bundle.json

# USB host, after a successful read
airc-gateway import-bundle --file /absolute/path/gateway.json \
  --input /absolute/private/received-bundle.json
```

Paths and device numbers are examples. Re-resolve the actual device after
re-enumeration. Run from the repository, keeping `gateway_device_io.py` and
`usb_hid_transport.py` beside the CLI script. The same private-file and child
ownership module is used by the NFC helper. Output is exclusively created as a
new mode-0600 file in a current-user-owned mode-0700 directory; source/output
paths are absolute and normalized. Failed transfers publish no bundle. An
interrupted sender may have sent a prefix, which the receiver must reject.

The emitter and receiver each run in a supervised child with a 30-second
deadline, bounded stdout and discarded child stderr. Timeout/interruption
terminates the child, allows one second for exit, then kills and reaps it if
needed. The importer owns its existing separate delivery/journal lifecycle.

## Report framing and authority

Each USB report is exactly 64 bytes:

| Bytes | Meaning |
|---|---|
| 0–3 | ASCII `AHI1` |
| 4–5 | Complete bundle length, unsigned big-endian |
| 6–7 | Zero-based report index, unsigned big-endian |
| 8–39 | SHA-256 of the complete bundle |
| 40–63 | Up to 24 bundle bytes; unused final bytes must be zero |

There are exactly `ceil(length / 24)` reports, at most 683. The reader starts at
index zero and requires consecutive indices, identical length/digest metadata,
canonical final padding and the matching complete digest. Short/oversized,
missing, duplicated, reordered or interleaved reports fail. It does not scan
indefinitely for another sequence or concatenate an incomplete transfer with a
later one. USB transfer acknowledgements establish no AIRC intake.

The digest detects incomplete or changed transfer data; it grants no authority.
USB IDs, function names, device permissions and report metadata also grant no
AIRC identity. The importer authenticates every signed envelope against current
keys, audience, revocation, target/Send grants and original freshness before
journal creation, then uses its existing durable replay controls and per-entry
reports. The bundle is signed plaintext, with no carrier confidentiality or
expiry renewal. Retrying uses the original bundle and stable request IDs.

## Verification boundary

Implementation receives source review and Python syntax compilation only.
Existing gateway production builds apply because this mode uses the unchanged
offline importer. No gadget, hidraw/hidg node, USB exchange, signature/replay
case, signal/timeout path or native AIRC delivery has been executed for this
mode. Deferred acceptance must prove actual interrupt IN data transfers using
the fixed descriptor and an independent AIRC observer, then cover exact author,
target and body, malformed/interrupted/replayed transfers, wrong devices,
permissions, bounds, unplug/re-enumeration and joined child shutdown.
