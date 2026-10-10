# I²C ingress through an NXP SC16IS752 bridge

The selected bridge is an NXP SC16IS752 on a Linux I²C bus, with a UART
peer attached to one of its serial ports. The kernel `sc16is7xx_i2c` driver
transfers UART data through I²C and presents `/dev/ttySCN`. The existing
`uart` listener and `serve-uart` command carry framed TLS through this device.
No additional gateway protocol or raw userspace I²C driver is needed.

Enable the board's I²C controller and `CONFIG_SERIAL_SC16IS7XX` with its
`CONFIG_SERIAL_SC16IS7XX_I2C` support. The kernel's
[configuration](https://github.com/torvalds/linux/blob/master/drivers/tty/serial/Kconfig)
and [I²C driver](https://github.com/torvalds/linux/blob/master/drivers/tty/serial/sc16is7xx_i2c.c)
define that interface. Use the actual board clock, I²C address, power, pull-ups,
pin configuration and wired interrupt in a `nxp,sc16is752` device-tree node;
follow the [binding](https://github.com/torvalds/linux/blob/master/Documentation/devicetree/bindings/serial/nxp%2Csc16is7xx.yaml)
and board documentation. This profile requires an interrupt rather than
relying on polling to service the small UART FIFO. Provision an appropriate
bus rate and clock for 115200 baud; throughput and interrupt latency require
physical qualification. Do not enable IrDA or automatic RS-485 mode for this
ordinary UART connection. Keep the device bound to its kernel driver.

Select the direct character-device node for the intended I²C device and UART
port, for example `/dev/ttySC0`. The node number is enumeration metadata, not
an identity or a stable hardware identifier. Grant the service account access
and remove competing console/getty owners before starting. Paths containing
symlinks are rejected. In an otherwise provisioned gateway configuration:

```json
{
  "id": "i2c-sc16is752",
  "protocol": "uart",
  "address": "/dev/ttySC0",
  "enabled": true,
  "tls_cert_file": "/absolute/keys/gateway-chain.pem",
  "tls_key_file": "/absolute/keys/gateway-key.pem",
  "operations": ["send", "receipt.get", "history.read"]
}
```

Exactly one listener may be enabled, with no enabled brokers or bridges.
Use existing AIRC principals, bindings, target grants and a read cursor key.
Run `airc-gateway serve-uart --file CONFIG`. The UART peer must implement TLS
1.3, verify the gateway certificate and name, negotiate `airc-gateway/1` ALPN,
and submit native authenticated requests using the
[framed protocol](framed-stream-v1.md). An I²C address, device node or serial
connection never grants an AIRC identity.

The existing [UART implementation](uart.md) opens and claims the selected tty,
requests raw 115200 8N1 without flow control, checks read/write deadline support,
and restores the saved termios on close. Requests remain bounded to 16 KiB
and whole responses to 64 KiB; UART handshake, frame and write deadlines apply.
Use small read pages and retain original signed request IDs when recovering
after loss. TLS and native receipts distinguish authenticated results from a
successful device write. Bus faults, FIFO overruns or wiring problems can end
the session; no automatic device selection or reopening is added.

This profile reuses existing code. The current production source builds for
Darwin ARM64 and Linux AMD64/ARM64; this physical bridge selects Linux. Source
review is complete, and runtime/device testing is deferred. Later acceptance
must identify the actual I²C device, bound driver and UART port, observe real
I²C data transfers, and verify native author/target/body, whole reads,
authentication refusals, replay, bus loss, frame bounds and joined shutdown.
Existing virtual-serial evidence does not qualify this bridge.
