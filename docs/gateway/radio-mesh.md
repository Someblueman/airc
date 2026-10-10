# Additional radio mesh: Linux IEEE 802.11s

The selected additional radio-mesh protocol is IEEE 802.11s using Linux
mac80211 mesh interfaces, HWMP path selection and SAE-protected peering through
wpa_supplicant. It carries the existing framed TLS connector over IP. This
concrete choice is separate from IEEE 802.15.4, Zigbee and LoRa bridges.

The [Linux mesh documentation](https://wireless.docs.kernel.org/en/latest/en/developers/documentation/ieee80211/802.11s.html)
describes mesh points and wireless forwarding. Provision compatible radios and
drivers with mesh support, a shared mesh ID, compatible permitted channel and
SAE credentials, and a wpa_supplicant build with mesh/SAE support. Its mesh
network mode is `mode=5`; require `key_mgmt=SAE`, not an open mesh. Keep the
radio credential in the platform's private configuration. Radio, peering and
IP setup remain the network operator's responsibility.

After provisioning a nonoverlapping mesh IP subnet, an illustrative gateway
listener is:

```json
{
  "id": "radio-mesh",
  "protocol": "framed",
  "address": "192.168.96.1:7443",
  "enabled": true,
  "tls_cert_file": "/absolute/keys/gateway-chain.pem",
  "tls_key_file": "/absolute/keys/gateway-key.pem",
  "operations": ["send", "receipt.get", "context.read", "history.read", "search"]
}
```

Bind to the actual mesh-interface address, provision native bindings/signing
keys/target grants and read cursor keys, then run
`airc-gateway serve --file CONFIG`. The existing
[framed client](../../examples/gateway/framed-client/main.go) uses that numeric
address with a trusted CA and verified server name. TLS 1.3, ALPN
`airc-gateway/1` and the native envelope checks remain required. Neither the
mesh ID, SAE password, MAC address nor forwarding node grants AIRC identity.

Mesh intermediaries carry the TLS stream. The
[framed contract](framed-stream-v1.md#wire-contract) retains whole request/reply
bounds, explicit read pages, deadlines, shared capacity, stable mutation IDs
and joined shutdown. Path changes or peer loss may break a connection; native
receipts and original-envelope retries resolve delivery uncertainty. The gateway
does not create mesh interfaces, select channels, change routes or manage peers.

No new gateway source is needed for this IP carrier. Existing production builds
passed for Darwin ARM64, Linux AMD64 and Linux ARM64. No radio configuration,
mesh association, network command or native AIRC test ran for this profile.

Deferred acceptance must use actual mesh-capable radios, SAE peering and an
independent AIRC observer. Record mesh/interface/channel and forwarding path;
exercise a real intermediate wireless hop, prove traffic avoids ordinary AP or
wired routes, and verify author/target/body, whole bounded reads, TLS/native
refusals, replay, peer/path loss, capacity and joined shutdown. A two-node link
alone does not establish multi-hop evidence, and ordinary Wi-Fi acceptance does
not qualify this mesh mode.
