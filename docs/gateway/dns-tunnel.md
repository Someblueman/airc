# DNS carrier: iodine 0.8.0

The selected DNS tunnel is [iodine 0.8.0](https://code.kryo.se/iodine/), with
matching client/server versions on Linux. It exposes an IPv4 TUN interface.
The existing `framed` TLS listener and framed client carry authenticated Send
and bounded reads through it without a new gateway protocol or codec.
Deployment and DNS-path acceptance remain deferred.

## Fixed path

Use an owner-controlled authoritative DNS endpoint and a delegated subdomain,
with an explicitly permitted resolver/path. The illustrative tunnel subnet
below is `192.168.99.0/24`, server `192.168.99.1`, provided it does not overlap
existing routes. Keep the normal default route; only the tunnel peer needs
to be reachable. Provision TUN access, a dedicated tunnel account and the
matching iodine binaries separately.

The [iodine operator documentation](https://code.kryo.se/iodine/README.html)
describes delegation and DNS mode. Require the client's `-r` option so iodine
cannot select direct raw UDP instead. An illustrative foreground pair, with
the real DNS name/resolver substituted, is:

```sh
iodined -f -u iodine 192.168.99.1/24 t1.example.net
iodine -f -r -I 1 -u iodine 192.0.2.53 t1.example.net
```

These commands are provisioning examples, not commands run by the gateway.
Enter the separate tunnel password at each prompt, avoiding command-line
secrets. Replace the reserved example domain and resolver with the authorized
deployment values. The gateway neither launches iodine nor modifies DNS,
routes, firewall rules or other services. Do not reuse an existing DNS service's
port without arranging a separate endpoint.

## Gateway configuration

Bind only to the selected tunnel address after it exists:

```json
{
  "id": "dns-tunnel",
  "protocol": "framed",
  "address": "192.168.99.1:7443",
  "enabled": true,
  "tls_cert_file": "/absolute/keys/gateway-chain.pem",
  "tls_key_file": "/absolute/keys/gateway-key.pem",
  "operations": ["send", "receipt.get", "context.read", "history.read", "search"]
}
```

Provision the usual native binding, signing principal, exact target grants,
cursor keys and optional encrypted-envelope recipient key. Start the normal
`airc-gateway serve --file CONFIG`. Use the existing
[`framed-client`](../../examples/gateway/framed-client/main.go) with numeric
`-address 192.168.99.1:7443`, `-ca` and a verified `-server-name`, consuming a
previously signed or encrypted request from stdin. TLS 1.3 and ALPN
`airc-gateway/1` remain mandatory.

Iodine's password and tunnel address grant no native AIRC authority. Its carrier
does not protect application confidentiality; verified end-to-end TLS protects
requests and whole responses through the DNS infrastructure. Native signature,
freshness, durable replay, operation and target checks remain unchanged.

The [framed bounds and deadlines](framed-stream-v1.md#wire-contract) still apply:
16 KiB request and 64 KiB response frames, three-second handshake/write deadlines
and five-second frame assembly. Keep messages and explicit read pages small.
DNS latency, loss and limited capacity may make a path unsuitable; do not extend
production deadlines or infer throughput from local builds. Reconnect and retry
with the original durable intent after an uncertain outcome.

## Deferred verification

This profile reuses the existing production connector and introduces no source
changes. The current source already passed Darwin ARM64, Linux AMD64 and Linux
ARM64 production builds. No iodine installation, DNS change, tunnel process,
network probe or native AIRC test was run.

Later acceptance must prove that requests and replies actually cross DNS with
raw UDP fallback disabled, preserve TLS trust and exact native author/target/body,
return whole bounded read pages/cursors, reject invalid grants/replay conflicts,
respect deadlines under loss, and close the gateway and separately owned tunnel
processes. A direct tunnel-server DNS path does not qualify a recursive-resolver
path; record the exact endpoint and resolver used.
