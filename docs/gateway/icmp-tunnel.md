# ICMP carrier: ptunnel-ng 1.42

The selected ICMP tunnel is [ptunnel-ng 1.42](https://github.com/utoni/ptunnel-ng),
running matching Linux builds in IPv4 ICMP mode. It forwards an existing TCP
stream between a local client port and one fixed gateway destination. The
gateway's existing `framed` TLS connector supplies authenticated Send, scoped
receipts and bounded reads. This is separate from the gateway's native AGD1
[`icmp_v4` packet listener](icmp-v4.md).

## Fixed path and ownership

Provision a permitted ICMP path between two explicit peer addresses, raw ICMP
socket access for the tunnel, and a dedicated tunnel account. The proxy runs
on the gateway host and restricts forwarding to `127.0.0.1:7443`. The client's
local forwarder listens on port 7444. Its upstream implementation binds that
TCP port on all IPv4 interfaces; isolate the forwarder with host/network-namespace
access controls that permit only local application clients. Do not describe
that upstream listener as a loopback bind.

The illustrative foreground commands below require those controls to be in
place. Substitute the actual authorized peer for the reserved example address:

```sh
ptunnel-ng --remote-addr=127.0.0.1 --remote-port=7443 --connections=16 --user=ptunnel --group=ptunnel
ptunnel-ng --proxy=192.0.2.20 --listen=7444 --remote-addr=127.0.0.1 --remote-port=7443 --connections=16 --user=ptunnel --group=ptunnel
```

The [upstream options](https://github.com/utoni/ptunnel-ng/blob/master/src/options.c)
make explicit destination restrictions significant on the proxy. Keep
`--udp` absent: UDP mode does not qualify the ICMP path. Keep the standard
privileged raw-ICMP mode rather than substituting the separate unprivileged
echo mode. A provisioned service owner manages privilege drop and process
shutdown independently of `airc-gateway`.

These are deployment examples; no tunnel was installed or started. The profile
uses endpoint/network restrictions and end-to-end TLS/native authentication.
The tunnel's optional password is not an AIRC credential or encryption layer.
No gateway account or signing key belongs in tunnel command-line arguments.

## Gateway and client

On the proxy/gateway host, enable the existing TLS listener:

```json
{
  "id": "icmp-tunnel",
  "protocol": "framed",
  "address": "127.0.0.1:7443",
  "enabled": true,
  "tls_cert_file": "/absolute/keys/gateway-chain.pem",
  "tls_key_file": "/absolute/keys/gateway-key.pem",
  "operations": ["send", "receipt.get", "context.read", "history.read", "search"]
}
```

Use ordinary native bindings, signer grants and cursor configuration, then
`airc-gateway serve --file CONFIG`. From the client host, connect the existing
[`framed-client`](../../examples/gateway/framed-client/main.go) to
`-address 127.0.0.1:7444` with a trusted `-ca` and explicit `-server-name` matching
the gateway certificate. Do not select plaintext mode for this loopback forwarder:
the stream crosses a remote ICMP carrier after entering that port. Require
TLS 1.3 and ALPN `airc-gateway/1`; native signed/encrypted envelopes still bind
the principal, operation, target, original lifetime and durable request ID.

Framed limits and deadlines remain unchanged, including whole 16 KiB requests,
whole 64 KiB responses, bounded concurrent work and joined gateway shutdown.
The tunnel does not change replay or uncertain-outcome semantics. Use small,
explicit read pages, retain original mutation envelopes for retries, and judge
acceptance from the native receipt rather than ICMP echo or TCP write success.
Packet loss or latency can make a permitted path unusable within those limits.

## Deferred verification

No production source change is required for this carrier. The existing source
passed Darwin ARM64, Linux AMD64 and Linux ARM64 production builds before this
documentation change. No tunnel process, ICMP probe or native AIRC test ran.

Later qualification must use real paired ptunnel-ng endpoints and independently
observe AIRC author/target/body and whole read pages. Verify actual ICMP traffic,
fixed proxy destination and forwarder access controls, gateway certificate/name
refusals, native authentication/replay, packet loss and uncertain delivery,
capacity and shutdown of both separately owned tunnel processes. Native AGD1
ICMP acceptance does not substitute for this tunnel's evidence.
