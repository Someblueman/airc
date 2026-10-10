# SPI ingress through a WIZnet W5500 bridge

The selected SPI device bridge is a WIZnet W5500 Ethernet controller attached
to a Linux SPI host. The kernel WIZnet driver presents a network interface;
the existing framed TLS connector receives authenticated AIRC requests through
the controller's Ethernet link and actual SPI data transfers. This is a concrete
network-device bridge, not a generic protocol for arbitrary SPI peripherals.

Linux enables this family with `CONFIG_WIZNET_W5100`. Its
[driver configuration](https://github.com/torvalds/linux/blob/master/drivers/net/ethernet/wiznet/Kconfig)
explicitly uses MAC/PHY functions and leaves the chip's TCP/IP offload unused.
The host Linux TCP/IP stack and gateway TLS code therefore retain their normal
contracts. The [SPI driver](https://github.com/torvalds/linux/blob/master/drivers/net/ethernet/wiznet/w5100-spi.c)
implements the W5500 register and data-buffer transfers.

Provision a compatible Linux board/SPI controller, W5500 module and Ethernet
peer. The operator supplies board-correct wiring, power, pin control, chip select
and interrupt configuration. The device-tree node uses `wiznet,w5500`, its
actual chip select and interrupt, and a SPI clock within the board's limits.
Follow the [kernel binding](https://github.com/torvalds/linux/blob/master/Documentation/devicetree/bindings/net/wiznet%2Cw5x00.txt)
and the board's own wiring documentation; example GPIO assignments from another
board are not portable. Keep the device bound to its network driver rather than
simultaneously exposing it to raw userspace SPI commands.

For an illustrative private link with W5500 interface `192.168.94.1/24` and
Ethernet peer `192.168.94.2/24`, the gateway listener is:

```json
{
  "id": "spi-w5500",
  "protocol": "framed",
  "address": "192.168.94.1:7443",
  "enabled": true,
  "tls_cert_file": "/absolute/keys/gateway-chain.pem",
  "tls_key_file": "/absolute/keys/gateway-key.pem",
  "operations": ["send", "receipt.get", "context.read", "history.read", "search"]
}
```

Use actual nonoverlapping interface addresses, existing AIRC bindings, signing
keys, target grants and read cursor keys. Run `airc-gateway serve --file CONFIG`.
The [framed client](../../examples/gateway/framed-client/main.go) connects from
the Ethernet peer with a trusted CA and verified server name. Require TLS 1.3,
ALPN `airc-gateway/1` and signed/encrypted native envelopes. SPI bus membership,
chip select and the controller's MAC address do not establish sender identity.

The [framed protocol](framed-stream-v1.md#wire-contract) retains request/reply
bounds, read pages, deadlines, capacity and joined shutdown. SPI/IRQ faults or
Ethernet loss can break the connection; preserve original signed request IDs
on retry and use native receipts to distinguish acceptance from transport
failure. The gateway does not change device trees, drive pins, load drivers or
reconfigure the bus. No performance or timing guarantee is added.

No new gateway code is required for this bridge. Current production builds
passed for Darwin ARM64 and Linux AMD64/ARM64; the physical bridge selects
Linux. Review is complete and all physical/runtime testing is deferred. Later
acceptance must identify the actual W5500 SPI device, bound driver and network
route, observe real SPI transfers, and verify native author/target/body, whole
reads, TLS/sender refusals, replay, bus/link loss, bounds and joined shutdown.
An ordinary Ethernet adapter does not provide SPI evidence.
