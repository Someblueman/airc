# USB serial through Linux CDC ACM

The chosen USB serial profile uses Linux's `cdc_acm` driver and its tty API.
The existing `uart` listener and `serve-uart` command implement this path;
USB enumeration and transfers belong to the kernel driver. No extra gateway
protocol, automatic device selection or userspace USB library is required.
See the kernel's [CDC ACM documentation](https://docs.kernel.org/usb/acm.html)
and [driver source](https://github.com/torvalds/linux/blob/master/drivers/usb/class/cdc-acm.c).
Real USB-device acceptance is deferred.

## Provision the endpoint

Use a CDC ACM device whose firmware provides a bidirectional byte stream
and implements a TLS 1.3 client plus the gateway's
[version-1 framed protocol](framed-stream-v1.md). An ordinary modem's AT
command port is not an AIRC client. The peer must verify the gateway's
certificate and name and negotiate `airc-gateway/1` ALPN before sending
native authenticated requests.

Provision the device, driver and service account's read/write permission
before starting the gateway. Determine the direct tty node belonging to the
intended USB interface, such as `/dev/ttyACM0`. USB interface descriptors,
serial numbers and device-node names are administrative selection metadata;
they do not grant an AIRC identity. Native principal signing keys, binding
credentials and target grants still control every request.

The configured path must be an absolute canonical character-device path.
`/dev/serial/by-id/...` and other symlinks are rejected. Enumeration can assign
a different node after unplugging; select and provision the new direct node
before an intentional restart. The gateway neither installs a driver nor
changes device permissions, selects another device or reconnects automatically.

Within an otherwise provisioned gateway configuration:

```json
{
  "id": "usb-serial",
  "protocol": "uart",
  "address": "/dev/ttyACM0",
  "enabled": true,
  "tls_cert_file": "/absolute/keys/gateway.crt",
  "tls_key_file": "/absolute/keys/gateway.key",
  "operations": ["send", "receipt.get", "history.read"]
}
```

Exactly one listener may be enabled, with no enabled brokers or bridges.
History reads require the ordinary cursor key. Start with:

```sh
airc-gateway serve-uart --file /absolute/path/gateway.json
```

## Transfer and shutdown behavior

The existing opener verifies the opened descriptor matches the selected
character device, takes an advisory lock, requests tty exclusivity and
configures raw 115200 8N1 without flow control. It verifies descriptor
read/write deadlines and restores the saved termios settings on close.
Those are requested line settings; firmware and physical baud behavior
require the deferred device qualification. CDC ACM's USB transport rate is
not established by its nominal serial baud setting.

TLS protects both directions. Requests are limited to 16 KiB and replies to
64 KiB. The existing serial handshake, frame, idle and write deadlines apply;
see [UART ownership and framing](uart.md). Send retries retain the same
native durable request ID and original intent. Transport writes alone do not
prove delivery: inspect the native response or recover the durable outcome.
Bounded history responses retain their complete page and cursor semantics.

Unplugging, descriptor errors or deadline expiry ends the session. Cleanup
joins admitted work and closes the descriptor; termios restoration errors
on a disconnected device remain visible. A later device appearing under the
same path is not silently opened. Restart only after checking its provisioning.

## Deferred verification

Source review confirms this profile uses the existing Linux termios,
nonblocking tty and framed TLS implementation. Production builds for Darwin
ARM64, Linux AMD64 and Linux ARM64 passed at the preceding BLE GATT checkpoint;
this profile adds documentation only. No USB device, driver runtime, physical
throughput, detach/re-enumeration or native AIRC acceptance was exercised.
Existing PTY evidence remains limited to virtual serial behavior.

Later acceptance must use a real CDC ACM endpoint and an independent native
AIRC observer: verify author/target/body, whole bounded reads, invalid
credentials, exact replay, exclusive ownership, framing bounds, disconnect
and joined shutdown. Direct USB bulk and interrupt endpoints remain separate
roadmap modes; CDC ACM's internal use of those transfer types does not qualify
either direct endpoint API.
