# BLE GATT through BlueZ

The Linux `ble_gatt` listener registers a local GATT service through BlueZ's
`org.bluez.GattManager1` and `GattCharacteristic1` D-Bus interfaces. A
provisioned LE peer uploads an encrypted signed native request, commits it,
and reads a whole encrypted response in pages. The chosen client dependency
is `github.com/godbus/dbus/v5` v5.2.0. Device and native AIRC testing are
deferred.

## Provisioning and configuration

Provision a root-owned BlueZ daemon, a powered LE controller, authenticated pairing and system
bus permission for the gateway account to register a local GATT application.
The system bus is the fixed root-owned Unix socket
`/run/dbus/system_bus_socket`; environment variables cannot select another
bus. The gateway checks the controller's address as well as its object path.
It neither installs bus policy nor pairs devices or changes controller
settings. LE advertising/discoverability must be provisioned separately;
this listener registers GATT attributes without an advertising service.

Within an otherwise provisioned gateway configuration:

```json
{
  "id": "gatt-link",
  "protocol": "ble_gatt",
  "address": "/org/bluez/hci0",
  "enabled": true,
  "operations": ["send", "context.read", "history.read", "search"],
  "ble_gatt": {
    "adapter_address": "00:11:22:33:44:55",
    "peers": [{
      "address": "aa:bb:cc:dd:ee:ff",
      "address_type": "public",
      "principal": "sensor",
      "target": {"kind": "room", "name": "devices"},
      "recipient_key_id": "sensor-result",
      "recipient_public_key_file": "/absolute/keys/sensor-result.pem"
    }]
  }
}
```

Run `airc-gateway serve --file CONFIG`. Controller and peer addresses use
canonical lowercase six-octet notation; zero/all-FF addresses and local
or duplicate peers are rejected. The adapter path ends in a canonical
decimal hci index from 0 through 65535. There are 1–16 peers per listener;
`address_type` is `public` or `random`. Provision the stable paired identity
address that BlueZ exposes, not a transient private advertising address.

Each mapped principal must be active, have signing keys and grant every
configured operation on its exact target. Ingress decryption keys, a gateway
response signer and each peer's fixed P-256 response public key are required.
History and search use the ordinary cursor configuration. Only Send,
context.read, history.read and search are enabled. TLS settings, browser
origins, anonymous access, separate return routes and streaming watches are
refused for this profile.

BlueZ presents method calls on behalf of remote devices. The gateway pins
BlueZ's unique bus owner, checks each method sender, and validates the
allowlisted Device1 path/address/type plus paired and connected state. Each
operation must identify its link as LE. Characteristic permissions require
authenticated encryption with `encrypt-authenticated-write` or
`encrypt-authenticated-read`. Bluetooth pairing is an admission boundary;
the encrypted native envelope independently proves the AIRC principal,
binding, target, operation and original freshness. BlueZ's
[GATT characteristic API](https://github.com/bluez/bluez/blob/master/doc/org.bluez.GattCharacteristic.rst)
defines per-operation device context and permission flags.

## Application protocol

The service UUID is `c74de000-3bcd-4efb-9d4d-860ab6927e80`.
The command characteristic is `c74de001-3bcd-4efb-9d4d-860ab6927e80` and
the result characteristic is `c74de002-3bcd-4efb-9d4d-860ab6927e80`.
There is no shared `Value` property or notification stream containing
responses from different peers.

Use normal acknowledged ATT writes. Commands require offset zero, LE link,
MTU at least 23 and no prepare authorization. The characteristic advertises
`write`, without `write-without-response`.
[BlueZ's server implementation](https://github.com/bluez/bluez/blob/master/src/gatt-database.c)
omits the server-side `type` option; when present it must be `request`. Clients must use acknowledged
writes and inspect the protected commit result. Prepared writes are refused.
Each value is at
most `min(512, MTU-3)` bytes. All multibyte integers below are big-endian.
Choose a fresh nonzero eight-byte transfer ID for each upload. This ID is
separate from the durable request ID inside the encrypted native envelope.

| Command | Value layout |
|---|---|
| Chunk | version byte `1`, opcode byte `0`, transfer ID (8 bytes), request offset (uint16), one or more payload bytes |
| Commit | version `1`, opcode `1`, transfer ID, total request length (uint16); exactly 12 bytes |
| Select result page | version `1`, opcode `2`, transfer ID, result offset (uint32); exactly 14 bytes |
| Reset | version `1`, opcode `3`, transfer ID; exactly 10 bytes |

Upload the exact compact encrypted native token, in contiguous chunks
starting at offset zero, up to 16 KiB. A peer has one transfer slot.
Commit only when the complete payload has been uploaded. A successful
commit makes a protected result available; the client must decrypt and
inspect that result to determine whether the native request succeeded.
An ATT acknowledgement alone does not establish AIRC delivery. Repeating
the same commit while its result remains retained returns the same result
without redispatching. To retry after loss or expiry, use the same native
durable Send ID and original intent so the journal can resolve its outcome.

Select an offset, then read the result characteristic at ATT offset zero.
The returned value has version byte `1`, transfer ID (8 bytes), whole-token
length (uint32), selected offset (uint32), then ciphertext bytes. Its size
is at most `min(512, MTU-1)`. Repeated reads return the same page; advance
with another select command. A selected offset equal to the total length
returns an empty tail. The explicit 32-bit offsets support the complete
64 KiB protected-token bound without relying on ATT long-value offsets.

Reassemble the whole token before decrypting and checking the gateway
signature, recipient, lifetime and scope. The protected payload is version
1, protocol `ble/gatt`, hex `transfer_id`, SHA-256 of the exact uploaded
ciphertext, fixed target, and either a result or failure. An authenticated
request also includes its native `request_id`. Reject mismatched transfer,
hash, target or response scope. Scope fields are realm, principal, binding
and route: the route is lowercase SHA-256 of NUL-joined
`airc-ble-gatt-response-v1`, listener ID, adapter path, adapter address, peer
address, address type, target kind and target name, without a trailing NUL.

Reset after consuming a result. An in-progress commit cannot be reset.
Partial uploads expire after 30 seconds and retained results after 60
seconds. Expiry releases their shared service slot. A mismatched chunk
sequence or incomplete commit discards the partial upload. Small MTUs
require more radio round trips; use smaller requests or negotiate a larger
MTU if an upload cannot finish in time, and request smaller native history
pages if a result cannot be fetched before expiry. No radio-throughput
guarantee is claimed.

## Ownership and verification boundary

BlueZ registration uses a dedicated ObjectManager hierarchy with stable
service/characteristic paths and properties. A replaced daemon or lost bus
connection invalidates the registration and stops the owner. Shutdown
stops intake, unregisters the application, joins request handlers and clears
retained buffers before closing response-signing keys. The shared session
limit bounds partial transfers and results; native requests additionally
use the shared request limit and bounded context. D-Bus frames are capped
at 64 KiB before decoding. The root-owned system bus must be dbus-daemon
or dbus-broker; both validate message bodies before forwarding. The
[BlueZ GATT manager API](https://github.com/bluez/bluez/blob/master/doc/org.bluez.GattManager.rst)
defines registration and disconnection behavior.

Production `go build ./...` passed with `CGO_ENABLED=0` for Darwin ARM64,
Linux AMD64 and Linux ARM64. Formatting and whitespace checks passed.
No radio probe, BlueZ runtime acceptance, tests or normal/race gate were run
for this implementation. Actual system-bus policy, daemon identity changes,
pairing refusal, MTU chunking, multiple peers, encrypted Send/read/replay,
loss and joined shutdown need a real Linux BlueZ fixture. BLE advertising
ingress and extended advertising are separate roadmap modes.
