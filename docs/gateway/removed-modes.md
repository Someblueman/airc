# Removed connection modes

These connection modes were removed because none ever ran against real
external software or devices, and some were never built. Their adapters,
codecs, configuration, CLI commands, native helpers and documents are gone;
earlier commits keep them in git history.

| Group | Removed modes |
|---|---|
| Bridges and provider webhooks | Matrix bridge, GitHub issue-comment webhooks, ChirpStack LoRaWAN uplinks |
| External services | Kafka, Amazon S3 object mailbox, Amazon SQS Standard, Amazon EventBridge through SQS, CouchDB change feed, SQLite database mailbox, Twilio inbound SMS, Postmark inbound email |
| Broker | NATS (the listener emulated a NATS server and could not connect to a real one) |
| Signed-bundle carriers | Discord bridge, Cyclone DDS, ESP-NOW broadcast and unicast, BLE legacy and extended advertising, LoRa, Zigbee, acoustic modem, USB HID interrupt input, NFC/NDEF (`import-ndef`), direct RDMA relay, optical QR camera capture |
| Windows and Android IPC | Windows named pipes, Windows COM, Windows RPC, Hyper-V sockets (`airc-gateway-pipe`, `-com`, `-rpc` and `-hvsock` binaries), Android Binder |
| Linux sockets and devices | DCCP, virtio-vsock, raw IPv4 protocol 253, raw Ethernet, IPv4 ICMP encapsulation, Classical CAN and CAN-FD ISO-TP, Bluetooth RFCOMM, Bluetooth L2CAP, BlueZ BLE GATT, vsftpd FTPS inbox |
| Profiles without code | I²C, SPI, USB bulk, USB serial, IEEE 802.15.4, IEEE 802.11s mesh, Thunderbolt, InfiniBand, ICMP tunnel, DNS tunnel, data diode |

Every carrier in the [carriers table](README.md#carriers) remains. The QR
image-sequence import, `serve-uart`, ICMPv6 encapsulation and the offline
bundle importer, kept at the time, were removed later with the other unused
carriers ([removed-carriers.md](removed-carriers.md)). Physical links that
reach an existing carrier over IP still work as that carrier.

## Existing configuration

Strict decoding rejects the top-level `kafka`, `sqs`, `s3`,
`sqlite_mailboxes` and `couchdb_changes` fields, even as empty arrays, and
the listener fields `nats`, `twilio_sms`, `postmark_email`, `raw_ip`,
`icmp_v4`, `raw_ethernet`, `virtio_vsock`, `rfcomm`, `l2cap`, `ble_gatt` and
`ftps_inbox`. An enabled listener whose protocol is `nats`, `twilio_sms`,
`postmark_email`, `raw_ip`, `icmp_v4`, `raw_ethernet`, `virtio_vsock`,
`dccp`, `bluetooth_rfcomm`, `bluetooth_l2cap`, `ble_gatt`, `can_isotp`,
`canfd_isotp` or `ftps_inbox` fails validation as not implemented; a
disabled one is reported as `disabled_unimplemented`. The hidden
`__can-isotp-worker` and `import-ndef` subcommands no longer exist.

A configuration with the top-level `matrix_bridges` field, a listener
`github_webhook` or `chirpstack` field, or a listener whose protocol is
`github_webhook` or `chirpstack` (enabled or not) fails with one error that
names the field and points here.

## Journal compatibility

The current journal schema has no tables for removed modes. A journal from
an earlier gateway refuses to open; see
[configuration](configuration.md#journal).
