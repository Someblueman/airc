# Removed connection modes

These connection modes have been removed. Each one was only implemented or
documented: none ever ran against real external software or devices, and some
were never built. Their adapters, wire codecs, configuration, service owners,
CLI commands, native helper trees, scripts and mode documents are gone. Their
earlier commits remain in Git history. [Kafka](kafka.md) was removed separately
under the same policy.

| Group | Removed modes |
|---|---|
| External service clients | Amazon S3 object mailbox, Amazon SQS Standard, Amazon EventBridge through SQS, CouchDB change feed, SQLite database mailbox, Twilio inbound SMS, Postmark inbound email |
| Broker | NATS. The listener emulated a NATS server and could not connect to a real one |
| Signed-bundle carriers | Discord bridge, Cyclone DDS, ESP-NOW broadcast and unicast, BLE legacy and extended advertising, LoRa, Zigbee, acoustic modem, USB HID interrupt input, NFC/NDEF (`import-ndef`), direct RDMA relay, optical QR camera capture |
| Windows and Android IPC | Windows named pipes, Windows COM, Windows RPC, Hyper-V sockets (`airc-gateway-pipe`, `-com`, `-rpc` and `-hvsock` binaries), Android Binder |
| Linux sockets and devices | DCCP, virtio-vsock, raw IPv4 protocol 253, raw Ethernet, IPv4 ICMP encapsulation, Classical CAN and CAN-FD ISO-TP, Bluetooth RFCOMM, Bluetooth L2CAP, BlueZ BLE GATT, vsftpd FTPS inbox |
| Carrier profiles without code | I²C, SPI, USB bulk, USB serial, IEEE 802.15.4, IEEE 802.11s radio mesh, Thunderbolt, InfiniBand, ICMP tunnel, DNS tunnel, data diode |

The QR image-sequence import, `serve-uart`, ICMPv6 encapsulation, the offline
bundle importer and every other mode in [STATUS.md](STATUS.md) remain.
Physical carriers that only reach an existing connector over IP or a tty, such
as a USB serial adapter, still work as that connector. They have no separate
qualification.

## Existing configuration

Remove fields that belong to removed modes before starting this version.
Strict decoding rejects the top-level `sqs`, `s3`, `sqlite_mailboxes` and
`couchdb_changes` fields, even as empty arrays. It also rejects the listener
fields `nats`, `twilio_sms`, `postmark_email`, `raw_ip`, `icmp_v4`,
`raw_ethernet`, `virtio_vsock`, `rfcomm`, `l2cap`, `ble_gatt` and `ftps_inbox`.
An enabled listener whose protocol is `nats`, `twilio_sms`, `postmark_email`,
`raw_ip`, `icmp_v4`, `raw_ethernet`, `virtio_vsock`, `dccp`,
`bluetooth_rfcomm`, `bluetooth_l2cap`, `ble_gatt`, `can_isotp`, `canfd_isotp`
or `ftps_inbox` fails validation as not implemented. As with any other unknown
protocol, a disabled one is reported as `disabled_unimplemented`. The hidden
`__can-isotp-worker` and the `import-ndef` subcommands no longer exist.

## Journal compatibility

No journal migration is removed or changed. Historical SQLite migration 21 and
its `couchdb_progress` table remain, so an existing journal opens unchanged and
keeps its stored change-feed checkpoints. Nothing reads or advances them any
more. Jobs that removed modes admitted are ordinary journal jobs. They keep
their normal delivery, receipt and retention behavior. Results that would have
gone back through a removed carrier are not published.
