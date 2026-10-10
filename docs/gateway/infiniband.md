# InfiniBand: Linux IPoIB datagram mode

The selected InfiniBand profile uses Linux `ib_ipoib` in datagram mode and
the existing framed TLS gateway connector. The kernel supplies IP transport
over the real InfiniBand fabric; the gateway supplies bounded application
framing and native authentication. No new gateway listener protocol or
userspace verbs library is needed for this profile. Direct RDMA is a separate
roadmap item and is not implemented by this selection.

The [Linux IPoIB documentation](https://docs.kernel.org/infiniband/ipoib.html)
describes the driver, partition keys and datagram mode. Provision compatible
InfiniBand adapters, a working subnet manager/fabric, matching authorized
partition membership and an up IPoIB interface on each endpoint. Use a
nonoverlapping IP subnet dedicated to that link and its actual supported MTU.
Datagram-mode IP MTU is the fabric link MTU minus the four-byte IPoIB header;
2044 is an example for a 2048-byte fabric, not a universal setting.

## Gateway profile

For an illustrative gateway IPoIB address `192.168.98.1` and peer
`192.168.98.2` on an otherwise unused subnet:

```json
{
  "id": "infiniband",
  "protocol": "framed",
  "address": "192.168.98.1:7443",
  "enabled": true,
  "tls_cert_file": "/absolute/keys/gateway-chain.pem",
  "tls_key_file": "/absolute/keys/gateway-key.pem",
  "operations": ["send", "receipt.get", "context.read", "history.read", "search"]
}
```

Provision the ordinary native binding, signing keys, exact principal/target
grants and read cursor configuration, then use `airc-gateway serve --file CONFIG`.
The existing [framed client](../../examples/gateway/framed-client/main.go) connects
to the numeric IPoIB address with a trusted `-ca` and `-server-name`. TLS 1.3,
ALPN `airc-gateway/1` and the native signed/encrypted request checks all remain
required. An InfiniBand partition, source IP or device GUID grants no AIRC
identity or operation authority.

The kernel TCP path handles segmentation/retransmission over IPoIB datagrams.
The [existing framed contract](framed-stream-v1.md#wire-contract) retains whole
request/response limits, capacity, freshness, durable retry identity, scoped
receipts, bounded read pages and joined shutdown. A link completion or successful
write is not native message acceptance. Preserve the original mutation envelope
after an uncertain outcome and verify the returned native result.

Interface, partition, routing, subnet-manager and hardware provisioning belong
to the fabric operator. The gateway does not change them. Route the peer address
through the intended IPoIB interface without an alternate Ethernet/VPN route
that would defeat later carrier qualification. No throughput, latency or
zero-copy benefit is claimed from merely choosing this carrier.

## Deferred verification

This uses existing production source, whose Darwin ARM64, Linux AMD64 and Linux
ARM64 builds passed before the documentation change. No fabric, HCA, IPoIB
interface, driver load, network command or native AIRC test was exercised.

Later acceptance requires real paired InfiniBand endpoints and an independent
AIRC observer. Record the actual HCA, interface, partition, datagram mode, MTU
and peer route; verify traffic crosses the fabric, exact author/target/body,
whole bounded cursor reads, invalid certificate/native grants, replay, fabric
loss, capacity and joined shutdown. Ordinary TCP or loopback evidence does
not qualify InfiniBand, connected IPoIB or direct RDMA.
