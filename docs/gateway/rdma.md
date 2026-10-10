# Direct RDMA: Linux rsocket stream relay

The selected implementation uses Linux rdma-core 56.4's stream rsocket API
on little-endian AMD64 or ARM64, with Python 3.10+ and compatible RDMA endpoints.
`scripts/rdma-bridge.py` relays one connection between a fixed loopback TCP
port and an explicit RDMA peer. Both ends use rsockets; the intervening
carrier is direct RDMA, separate from [IPoIB](infiniband.md). Ordinary framed
clients and the gateway retain their end-to-end TLS connection.

`gateway_rdma.py` binds the selected C ABI through `ctypes`; it calls
`rsocket(AF_INET, SOCK_STREAM, IPPROTO_TCP)` and the corresponding `r*`
functions directly. The `IPPROTO_TCP` argument selects rsocket stream
semantics, not a TCP fallback. No preload interception, datagram fallback,
arbitrary remote-memory mapping, or new gateway operation is exposed.

## Provisioning and invocation

Provision both hosts with compatible HCAs, kernel drivers, the selected
rdma-core userspace libraries/providers, permitted memory-lock/device access
and a working authorized InfiniBand or RoCE fabric/route. Assign explicit,
distinct non-loopback IPv4 addresses to the RDMA endpoints. The helper binds
its configured local RDMA address, uses one configured port, and the receiver
admits only the configured peer address. These addresses are routing filters;
TLS and signed grants establish authority. No fabric, NIC, firewall, route,
driver or system configuration is changed by this helper.

Supply the canonical absolute path of the actual provisioned
`librdmacm.so.1` ELF file, resolving its usual soname symlink, and its trusted
lowercase SHA-256 digest. The file must be root/current-user-owned, not writable
by group/others, without set-ID bits, and at most 16 MiB. The helper checks file
identity and architecture, verifies the digest, copies the exact verified bytes
into a sealed memfd and loads that copy. This prevents path replacement or
in-place edits from substituting the primary library after verification.
Its dynamic dependencies and HCA providers still come from the provisioned
trusted Linux loader environment. The digest is an operator provenance pin,
not automatic verification of a vendor release. Memfd loading/sealing and
`/proc/self/fd` must be available. No dependency installation is performed.

Keep `rdma-bridge.py`, `gateway_rdma.py` and `gateway_device_io.py` together.
Run a TLS-enabled framed gateway on the receiving host's loopback address:

```json
{
  "id": "rdma-tls",
  "protocol": "framed",
  "address": "127.0.0.1:7443",
  "enabled": true,
  "tls_cert_file": "/absolute/keys/gateway-chain.pem",
  "tls_key_file": "/absolute/keys/gateway-key.pem",
  "operations": ["send", "receipt.get", "context.read", "history.read", "search"]
}
```

Use the existing native bindings, sender verification keys, exact
principal/target grants, journal and cursor configuration. Start with
`airc-gateway serve --file CONFIG`. TLS is required for this relay profile,
including on loopback; a plaintext upstream would expose responses across
the RDMA fabric. The relay transports opaque bytes and does not independently
verify certificates or reinterpret application authentication.

On the gateway host, start the one-shot acceptor (illustrative provisioned
RDMA addresses shown):

```sh
python3 scripts/rdma-bridge.py accept \
  --local-ip 192.0.2.1 --peer-ip 192.0.2.2 --port 7471 --tcp-port 7443 \
  --library /absolute/real/librdmacm.so.1.3.56.4 --sha256 PROVISIONED_SHA256
```

On the client host, start its one-shot relay:

```sh
python3 scripts/rdma-bridge.py connect \
  --local-ip 192.0.2.2 --peer-ip 192.0.2.1 --port 7471 --tcp-port 7444 \
  --library /absolute/real/librdmacm.so.1.3.56.4 --sha256 PROVISIONED_SHA256
```

The actual library filename is distribution-specific; replace all paths,
pins, addresses and ports with provisioning values. Each helper stays in the
foreground while serving its one connection. Start the receiver, then the
client relay, then invoke the existing client from another terminal within
the 20-second accept window:

```sh
go run ./examples/gateway/framed-client \
  --address 127.0.0.1:7444 --ca /absolute/keys/ca.pem \
  --server-name rdma-gateway.example --profile signed --correlation request-1 \
  < /absolute/private/original-request.jws
```

The client verifies the receiving gateway's actual certificate name and CA,
TLS 1.3 and ALPN `airc-gateway/1` through both relays. Never use its
`--local-plaintext` option with this profile. Reinvoke both helpers for another
connection; no unattended retry or listener restart is built in. Internal
underscore-prefixed helper commands are supervisor plumbing.

## Bounds, reads and outcomes

The listener backlog is one; each helper accepts one peer, closes its listener
and connects only to its fixed destination. Acceptance waits at most 20 seconds,
connection completion five seconds, and relay activity 30 seconds. A parent
supervises the child for 65 seconds including library load/setup and cleanup,
then terminates, kills if necessary and reaps it. No children are left running.
Normal cleanup closes both endpoints through their respective APIs. Native
driver/library stalls may need forced termination; they are not reported as
successful cleanup.

Each direction has a 64 KiB application buffer and a four-MiB total byte cap.
Reads pause while that direction's buffer is full; partial writes advance only
by the reported count. EOF is propagated by a write half-close after queued
bytes drain, allowing final replies in the other direction. Errors, over-cap
data or deadline expiry close the connection with an uncertain application
outcome. Both directions must finish before a successful helper exit.

The rsocket profile requests 64 KiB native send/receive buffers, 128-entry
send/receive queues, zero inline payload and zero remote-I/O mappings. After
connection creation it verifies the buffers and mapping count, requires queues
within the 128-entry cap, and bounds provider-rounded inline capacity to 256
bytes. Accepted sockets inherit the requested settings. Polling mixes at most two
descriptors in bounded 100-ms waits. The child additionally lowers its address
space limit to at most 512 MiB and descriptor limit to at most 64, preserving
any stricter existing limit. Provider/kernel/HCA resources still require
operator provisioning; these process bounds are not a hardware resource proof.

The existing [framed contract](framed-stream-v1.md) enforces 16 KiB requests,
64 KiB complete responses, bounded authenticated pages/cursors, original intent
freshness and durable receipt/replay behavior. A caller can submit Send, recover
its receipt and request bounded history/context/search through this same
connection profile. A relay exit, fabric completion or TLS write does not mean
native AIRC acceptance. After loss, recover using the original signed mutation
and durable request ID; never mint a replacement timestamp just to retry.

## Deferred verification

Python source review, syntax compilation and diff checks were performed.
The pinned C header/source informed the ABI and bounds, but no native library
was loaded and no RDMA endpoint, route, TLS session or AIRC request was exercised.
No performance, zero-copy or latency benefit is claimed.

Later acceptance requires compatible real endpoints and independent native
AIRC observation. Verify the actual provider/fabric path, certificate/name
and author/target/body, complete bounded reads and cursor renewal, wrong peer
and library pins, replay, congestion/backpressure/half-close, malformed native
results, resource caps and interruption/driver-loss cleanup. IPoIB or ordinary
TCP evidence does not qualify direct RDMA.

Primary references: rdma-core 56.4 [rsocket ABI](https://github.com/linux-rdma/rdma-core/blob/v56.4/librdmacm/rsocket.h),
[implementation](https://github.com/linux-rdma/rdma-core/blob/v56.4/librdmacm/rsocket.c)
and [rsocket manual](https://github.com/linux-rdma/rdma-core/blob/v56.4/librdmacm/man/rsocket.7.in).
