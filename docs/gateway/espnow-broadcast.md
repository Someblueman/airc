# ESP-NOW broadcast signed-bundle ingress

The chosen broadcast implementation uses the same dedicated ESP32,
MicroPython 1.26.0, Linux host and bounded UART helper as
[unicast](espnow-unicast.md), with a separately provisioned `broadcast` mode.
It publishes one signed bundle for reception by multiple configured boards.
Native application ingress remains Send-only through `import-bundle`.

## Provisioning and use

Provision `native/espnow/bridge.py` as each board's `main.py`. The private
`espnow_config.py` requires `MODE = "broadcast"`, the actual `LOCAL_MAC`,
`CHANNEL` (1–11), `ROLE` (`"emit"` or `"read"`) and `AUTH_KEY` (32 random
bytes). The publisher sets `PEER_MAC = b"\xff" * 6`. Each receiver instead
sets `PEER_MAC` to the exact publisher's station MAC. Use a distinct
publisher/group authentication key shared with its receivers. Omit `PMK`
and `LMK`: their presence in this mode is refused so provisioning cannot
suggest radio encryption that broadcast does not provide.

Use the unicast profile's dedicated UART1 GPIO33 TX/GPIO32 RX, private Linux
tty, file permissions, radio/channel provisioning and one-operation-per-boot
lifecycle. Both roles register only the broadcast MAC with encryption disabled.
The firmware verifies the selected channel, station MAC, registered peer
and interface. The host's `--mode broadcast` must agree with board provisioning;
the default host mode remains unicast. Mixed modes fail their framing checks.

On each receiving host, start before the publisher and allow board setup:

```sh
python3 scripts/espnow-bundle.py read --mode broadcast \
  --device /dev/ttyUSB0 --local RECEIVER_MAC --peer PUBLISHER_MAC \
  --channel CHANNEL --output /absolute/private/received-bundle.json
```

Publish from the sender's host:

```sh
python3 scripts/espnow-bundle.py emit --mode broadcast \
  --device /dev/ttyUSB0 --local PUBLISHER_MAC --peer FF:FF:FF:FF:FF:FF \
  --channel CHANNEL --input /absolute/path/signed-bundle.json
```

Every receiving host independently imports its resulting file with the sole
enabled `offline_bundle` ingress and actual sender keys/grants:

```sh
airc-gateway import-bundle --file /absolute/path/gateway.json \
  --input /absolute/private/received-bundle.json
```

Use the intended receiving gateway's realm/audience and binding in the original
signed envelope. Broadcasting it does not authorize other gateways or create
new native identities. See [offline-bundle.md](offline-bundle.md) for admission
and durable outcomes.

## Broadcast framing and trust

UART request/reply layouts and deadlines match unicast, with magic `AEB1`.
Radio records are 248 bytes: `AEB1`, two-byte big-endian total length,
two-byte index, 32-byte complete-bundle SHA-256, 176-byte chunk (last chunk
zero-padded), and 32-byte HMAC-SHA256. The HMAC input is
`airc-espnow-broadcast-v1` plus a zero byte, publisher MAC, six `0xff` bytes,
one-byte channel, and the first 216 record bytes. It uses the provisioned
`AUTH_KEY`. Distinct magic and domain separate broadcast from unicast.

Receivers filter the provisioned source, verify the tag before assembly and
require complete consecutive indices, consistent totals/digests and exact
padding. The existing 2048-byte bundle bound permits at most 12 fragments.
They use a 4096-byte radio ring, 250-byte receive buffer, at most 256 receive
calls and an initially empty read-only RSSI table. A 25-second transaction
deadline and non-fed 30-second watchdog bound device operation; the host
supervises its child for 30 seconds. Radio shutdown precedes a final UART
success reply. Host file publication waits for complete data/digest checks
and successful child/UART cleanup. No automatic retry, recovery cache or
fragment resynchronization is added.

ESP-NOW broadcast is unencrypted and observable by nearby receivers. Its
synchronous send returns success without proving any receiver got the data.
The per-record HMAC only proves possession of the publisher/group key: every
holder can forge such a record. A MAC address, digest or group key never
establishes the native author. The receiving importer independently verifies
the author's signed envelope, current grants, audience and original freshness,
then applies durable replay protection. A receiver holding only the group key
cannot mint another author's signed intent. Native delivery is established
by the import report/receipt and independent AIRC observation, not radio or
helper success. Preserve the original signed intent on any later retry.

## Deferred verification

Host/firmware syntax compilation and source review are complete; there was
no MicroPython execution, provisioning, installation or RF/native testing.
Later acceptance must exercise multiple real receivers, actual broadcast
frames, wrong group key/source/mode, valid-group-key but invalid native author,
loss/order/padding/bounds, original expiry/replay, watchdog/cleanup and native
AIRC delivery. Unicast evidence cannot qualify this mode.

Primary reference: MicroPython's [ESP-NOW broadcast behavior](https://docs.micropython.org/en/v1.26.0/library/espnow.html#broadcast-and-multicast).
