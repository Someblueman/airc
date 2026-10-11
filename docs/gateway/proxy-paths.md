# SOCKS and HTTP CONNECT acceptance

The existing HTTPS connector works through two independently tested local proxy
paths: SOCKS5 through OpenSSH `ssh -D`, and HTTP CONNECT through mitmproxy
`mitmdump` in regular proxy mode. Neither needs a new gateway transport.

Both opt-in tests use isolated loopback processes and a real Go HTTP client.
The client verifies the gateway TLS certificate and sends author requests
through the proxy. An independent native AIRC observer and CHECK reader verify
the author, target, complete bodies, order and message IDs. Each test rejects an
invalid author and an oversized request, replays identical requests with one
send attempt, reads one-record pages through the final empty cursor, and joins
its owned proxy processes.

The SOCKS fixture reuses the pinned-host-key SSH fixture, disables the user's SSH
configuration and agent, rejects an unlisted client key, and restricts forwarding
to the exact gateway endpoint with `PermitOpen`. A request to another live local
listener must fail. The HTTP CONNECT fixture uses a private configuration
directory and logs the requested CONNECT authority. Its exclusion rule passes
TLS through only for the fixture's exact host and port; the client verifies the
original server certificate. In mitmproxy 12.2.3, the
[matching implementation](https://github.com/mitmproxy/mitmproxy/blob/v12.2.3/mitmproxy/addons/next_layer.py#L203-L242)
includes the port in the destination string.

Run the selected path with:

```sh
AIRC_TEST_SSH_SOCKS=1 go test ./integration/gateway -run '^TestHTTPDurableSendAndBoundedHistoryThroughSSHSOCKS$' -count=1 -timeout=180s
AGW_CONNECT_PROXY=1 go test ./integration/gateway -run '^TestHTTPConnectProxyPassThroughTLSAcceptance$' -count=1 -timeout=180s
```

Add `-race` for instrumented acceptance. Explicit opt-in fails if the required
installed executable is unavailable; the tests do not obtain or install tools.
This evidence covers the isolated Darwin ARM64 paths with OpenSSH 10.3p1 and
mitmproxy 12.2.3. Remote proxies, other platforms and deployed network paths
require their own runtime evidence.

On 2026-10-09, the final two-test selection passed normally (3.873 seconds) and
with `-race` (5.313 seconds). Per-test times were:

| Path | Normal | Race |
| --- | ---: | ---: |
| SOCKS5 | 0.79s | 0.94s |
| HTTP CONNECT | 2.59s | 2.73s |

The existing SSH local and reverse tests also passed under race detection after
the shared SSH helper gained dynamic forwarding. Integration vet, staticcheck,
formatting and diff checks passed. These are focused mode results; the current
full-checkpoint status remains in [testing.md](testing.md).
