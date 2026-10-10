# Specialized interconnect: Linux ThunderboltIP

The additional specialized interconnect is Linux ThunderboltIP using the
`thunderbolt-net` driver between two compatible Linux hosts connected by a
Thunderbolt cable. The resulting network interface carries the existing
framed TLS connector. This is the concrete scope for the catalogue's otherwise
open-ended specialized-interconnect label; direct PCIe or remote-memory APIs
are not part of this profile.

The [kernel networking documentation](https://docs.kernel.org/admin-guide/thunderbolt.html#networking-over-thunderbolt-cable)
describes host-to-host ThunderboltIP and the per-port virtual Ethernet interface.
Provision compatible controllers/cable, driver support and the platform's
existing device authorization policy. Configure an up link and a nonoverlapping
IP subnet with normal system networking tools. Driver loading, authorization
and route changes are deployment work; the gateway does not alter them.

For example, after provisioning gateway `192.168.97.1` and peer `192.168.97.2`
on that interface, enable:

```json
{
  "id": "thunderbolt",
  "protocol": "framed",
  "address": "192.168.97.1:7443",
  "enabled": true,
  "tls_cert_file": "/absolute/keys/gateway-chain.pem",
  "tls_key_file": "/absolute/keys/gateway-key.pem",
  "operations": ["send", "receipt.get", "context.read", "history.read", "search"]
}
```

Use the standard native account binding, signing principal, exact operation
and target grants and bounded-read cursor configuration. Start
`airc-gateway serve --file CONFIG` and connect the existing
[framed client](../../examples/gateway/framed-client/main.go) to that numeric
address with a trusted CA and verified certificate name. TLS 1.3 and ALPN
`airc-gateway/1` remain mandatory. Cable attachment and platform device
authorization do not grant AIRC identity or messaging permissions.

The [framed wire contract](framed-stream-v1.md#wire-contract) provides whole
bounded requests/responses, signed intent, freshness, durable replay identity,
native receipts and joined shutdown. Link removal is a transport failure;
accepted durable work stays journaled. Retry uncertain mutations with their
original envelopes and recover reads from the last complete cursor. Reconnection
does not bypass certificate or principal checks. No speed or latency claim is
made without measurements on the selected hardware.

This profile reuses existing production source and introduces no new codec.
Darwin ARM64, Linux AMD64 and Linux ARM64 production builds had passed before
this documentation change. No driver, device authorization, cable operation,
network setup or runtime test was performed.

Deferred acceptance requires real paired Thunderbolt hosts: record controller,
cable, kernel, interface and peer route; prove traffic uses that link rather
than Wi-Fi/Ethernet; verify native author/target/body, whole bounded reads,
certificate and grant refusals, replay, capacity, unplug/reconnect and joined
shutdown. USB serial/bulk/interrupt, direct USB4 streams and other specialized
interconnects require their own evidence and are not qualified by this profile.
