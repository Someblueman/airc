# USB bulk transfers through CDC-NCM

The selected USB bulk bridge is Linux CDC-NCM: a Linux USB host using
`cdc_ncm` and a Linux device-controller endpoint using the `usb_f_ncm` gadget
function. NCM carries Ethernet/IP data through bulk IN and OUT endpoints, so
the existing framed TLS connector implements authenticated Send and bounded
reads over the link. NCM's interrupt endpoint carries status notifications;
this profile does not qualify application ingress through interrupt transfers.
The endpoint types are explicit in the
[kernel NCM gadget source](https://github.com/torvalds/linux/blob/master/drivers/usb/gadget/function/f_ncm.c).

Provision a Linux board with a USB device controller, a compatible cable and
Linux USB host. Enable the appropriate controller, composite/configfs, NCM
gadget and host driver support. The operator creates the `ncm` gadget function,
assigns valid device descriptors and distinct MAC addresses, links the function
into a configuration and binds the selected device controller. Use the
[kernel gadget provisioning guide](https://docs.kernel.org/usb/gadget_configfs.html)
and [NCM function reference](https://docs.kernel.org/usb/gadget-testing.html#ncm-function).
An ordinary host-only USB port cannot serve as the gadget endpoint.

After enumeration, configure a private nonoverlapping IP subnet on the two
resulting network interfaces. No routing, forwarding, DHCP or shared Internet
connection is needed for a direct pair. For an illustrative gadget address
`192.168.95.1/24` and host address `192.168.95.2/24`, the gateway on the gadget is:

```json
{
  "id": "usb-bulk",
  "protocol": "framed",
  "address": "192.168.95.1:7443",
  "enabled": true,
  "tls_cert_file": "/absolute/keys/gateway-chain.pem",
  "tls_key_file": "/absolute/keys/gateway-key.pem",
  "operations": ["send", "receipt.get", "context.read", "history.read", "search"]
}
```

Replace the example addresses with the provisioned USB-interface addresses.
Configure existing principal signing keys, native bindings, target grants and
read cursor keys, then run `airc-gateway serve --file CONFIG`. The
[framed client](../../examples/gateway/framed-client/main.go) connects over the
NCM interface with a trusted CA and verified server name. TLS 1.3, ALPN
`airc-gateway/1` and native envelope authentication remain mandatory. USB
descriptors, serial numbers, MAC addresses and cable possession do not grant
AIRC identity. No plaintext mode or USB-ID-based identity mapping is introduced.

The kernel NCM drivers own USB packet aggregation, transfer completion and
network-device lifecycle. The [framed contract](framed-stream-v1.md#wire-contract)
retains whole requests/replies, read page boundaries, deadlines, capacity and
joined service shutdown. Unplug or re-enumeration can break the TCP stream;
retain original signed request IDs for retries and inspect native receipts to
resolve uncertain delivery. The gateway does not detach a kernel driver, claim
raw USB endpoints, provision gadgets or change the host network.

No new gateway code is needed for the selected bulk carrier. Current production
builds passed for Darwin ARM64 and Linux AMD64/ARM64; this gadget/host profile
selects Linux at both endpoints. Source and documentation review are complete,
with no gadget, cable, network command or runtime test executed. Deferred
acceptance must use a real NCM gadget and host, record the bound drivers and
bulk data endpoints, prove the request path crosses that USB link, and cover
author/target/body, whole reads, TLS/native refusals, replay, transfer loss,
unplug/re-enumeration, bounds and shutdown. Serial or status-notification
evidence cannot substitute for the bulk data path.
