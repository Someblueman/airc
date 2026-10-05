# Connection gateway inventory

Remaining work lives only in [ROADMAP.md](ROADMAP.md). This file is a concise implementation inventory, not a second task list or a running acceptance log.

## Existing implementation

- Shared core: principal-to-AIRC mapping, bearer and signed/encrypted authentication, durable SQLite intake, stable IDs, bounded retries, receipt recovery, revocation, capacity limits, and owned shutdown.
- Request/stream carriers: HTTP/1.1 and HTTP/2, polling/SSE, framed TCP/TLS, WebSocket, Unix streams, stdio, MCP Streamable HTTP, JSON-RPC, and gRPC.
- QUIC/packet carriers: HTTP/3, WebTransport, raw QUIC streams/datagrams, UDP, DTLS, and Unix datagrams.
- Other carriers: WebRTC DataChannels, CoAP over DTLS, and CoAP over TLS/TCP.
- Optional gateway operations already exist beyond message submission and reading. Their presence does not require other connectors to implement them.

These are implementation facts, not a fresh certification of every carrier. Existing evidence primarily uses isolated Darwin fixtures; browser, remote-network, Linux, and hardware claims require their own actual acceptance. OSCORE prototype work is not a gateway implementation. The full catalogue is not implemented.

At the recovery starting point, the branch also contains uncommitted WHO/WHOIS and test-speed investigation work. It has no configured upstream; previous milestone notes report no installation or deployment. Recovery must preserve unrelated edits and keep the live service outside test fixtures.

The previous chronological acceptance record remains in Git at `28d255e:docs/gateway/STATUS.md`. Earlier passes apply to their recorded source versions; they do not certify a subsequently modified tree. Future verification belongs with the relevant transport specification and commit, not an expanding master log.
