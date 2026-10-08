# Remote UDP test lab

The dedicated test host is `ubuntu@vps-2bb89225.vps.ovh.ca`
(`148.113.195.168`). This deployment exercises the original bridge topology:

```text
Mac UDP client -> Internet UDP :9001 -> gateway -> private Unix socket -> AIRC
```

SSH is used for administration and independent native observation. No SSH tunnel,
HTTP connection, or direct native TCP connection carries the UDP client's messages,
receipts, or history. The host firewall permits incoming SSH and UDP 9001 only;
the native server has no TCP listener.

## Installed state

- `/home/ubuntu/airc-test`, mode 0700, contains server keys, configuration, native
  history/accounts, gateway journal, and binaries.
- `airc-lab-native.service` and `airc-lab-gateway.service` start on boot and run as
  `ubuntu`, with owner-only file creation and restricted filesystem writes.
- The signed client identity is `agent`, mapped to native account `udp-agent`.
  It can use `send`, `receipt.get`, and `history.read` for `#udp-lab` only.
- Local generated credentials are in `bin/vps-lab/client`; compiled clients are
  in `bin/vps-lab-build`. Both are ignored by Git. Keep these credentials private.
- Application binaries were built from committed source
  `42df59a5f44ba1fabd204cd08c86ab97fb2904c5` (check `source-commit` on the host for
  the authoritative full hash). Existing uncommitted transport work was excluded.
  The `udp-lab` provisioning helper is supplied by this change.

The return route uses this Mac's public IPv4 address and port 49152, observed by a
temporary nonce-checked UDP probe. It worked through this network's IPv4 NAT.
Bind the client to local UDP port 49152. **This route is specific to the current
network and is not automatic discovery or NAT traversal.** If the public IP or
mapped port changes, the operator must update `return_routes[0].destination` in
the server's `gateway.json` and restart the gateway. A different client also needs
its own keys, principal, and return route; do not share this test identity.

## Send and read from this checkout

These commands use the existing `udp-client` wire implementation. The lab helper
only generates keys/configuration, registers the native account, and creates the
signed/encrypted envelope. It adds no production protocol or ordinary `airc` CLI
connection mode.

From the repository root, prepare one new intent and save its envelope:

```sh
umask 077
lab="$PWD/bin/vps-lab"
request_id="manual-$(date +%s)"
printf '{"version":1,"request_id":"%s","operation":"send","target":{"kind":"room","name":"#udp-lab"},"message":"Hello over real remote UDP"}\n' "$request_id" \
  | bin/vps-lab-build/udp-lab envelope --dir "$lab/client" > "$lab/manual.jwe"
examples/gateway/udp-lab/call.sh 148.113.195.168:9001 0.0.0.0:49152 < "$lab/manual.jwe"
```

A `queued` result means durable gateway admission, not native acceptance. Poll the
receipt for the same request ID until `state` is `accepted`, or inspect a terminal
failure. Each invocation below prepares a fresh read envelope:

```sh
printf '{"version":1,"request_id":"receipt","operation":"receipt.get","lookup_id":"%s"}\n' "$request_id" \
  | bin/vps-lab-build/udp-lab envelope --dir "$lab/client" \
  | examples/gateway/udp-lab/call.sh 148.113.195.168:9001 0.0.0.0:49152

printf '%s\n' '{"version":1,"request_id":"history","operation":"history.read","target":{"kind":"room","name":"#udp-lab"},"start":"retained","limit":10}' \
  | bin/vps-lab-build/udp-lab envelope --dir "$lab/client" \
  | examples/gateway/udp-lab/call.sh 148.113.195.168:9001 0.0.0.0:49152
```

History bodies are base64url in `body_base64`; the wire format preserves bytes.
Use a returned cursor for subsequent pages. The example prints one verified
response and does not poll or retry automatically. Run calls sequentially because
they share the fixed mailbox port.

To retry a send after a timeout, reuse `manual.jwe`. Do not generate a new request
ID or regenerate its timestamps for the same intent. New envelopes expire after
120 seconds; preserved explicit send timestamps are not extended. Consult
[UDP semantics](udp.md) for retained receipts and the bounded retry policy.

Independently inspect AIRC, or restart both services:

```sh
ssh ubuntu@vps-2bb89225.vps.ovh.ca \
  '/home/ubuntu/airc-test/bin/airc history "#udp-lab" --unix /home/ubuntu/airc-test/native.sock --json'
ssh ubuntu@vps-2bb89225.vps.ovh.ca \
  'sudo systemctl restart airc-lab-native.service airc-lab-gateway.service'
ssh ubuntu@vps-2bb89225.vps.ovh.ca \
  'sudo journalctl -u airc-lab-native.service -u airc-lab-gateway.service -n 40 --no-pager'
```

The native peer `vps-peer` is available on the host with its private identity at
`/home/ubuntu/airc-test/peer-identity.json`. To send a message for the UDP client
to retrieve:

```sh
ssh ubuntu@vps-2bb89225.vps.ovh.ca \
  '/home/ubuntu/airc-test/bin/airc send --channel "#udp-lab" --message "Reply from the VPS" --unix /home/ubuntu/airc-test/native.sock --identity /home/ubuntu/airc-test/peer-identity.json --json'
```

## Rebuilding or provisioning another isolated lab

Build the Go examples locally and cross-compile the server binaries for the
host architecture (`amd64` for this VPS):

```sh
go build -o bin/vps-lab-build/udp-lab ./examples/gateway/udp-lab
go build -o bin/vps-lab-build/udp-client ./examples/gateway/udp-client
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/vps-lab-build/linux-amd64/ \
  ./cmd/aircd ./cmd/airc ./cmd/airc-gateway ./examples/gateway/udp-lab
```

Build from a clean, known source revision when deploying. The current server
binaries have not incorporated subsequent working-tree changes.

`udp-lab init --dir ABSOLUTE_NEW_LOCAL_DIRECTORY --server-dir ABSOLUTE_REMOTE_DIRECTORY
--return NUMERIC_PUBLIC_CLIENT_IP:PORT` creates separate `server` and `client`
directories, with a default UDP listener on `0.0.0.0:9001`. It refuses to overwrite
an existing output directory. Transfer only `server` to the deployment directory,
and place the Linux binaries under its `bin` directory. Keep client private keys
on the client machine. The bundled service files target `/home/ubuntu/airc-test`
and the `ubuntu` user; adjust these for another deployment.

Validate the server config with `airc-gateway config validate --file ...`, install
the two service files, and start the native service first. Wait until its Unix
socket exists, then run `udp-lab register --dir ABSOLUTE_REMOTE_DIRECTORY` on the
server. Start the gateway after registration. Configure the firewall to preserve
management access and permit the chosen UDP port. Registration reuses the same
account credential when rerun; initialization must not be used to rotate it.

## Observed result, 2026-10-09 (Europe/London)

The real Mac-to-VPS run, using these installed binaries, demonstrated:

- Encrypted/signed UDP send reached native AIRC as `udp-agent` in `#udp-lab`.
  Native message ID: `a5acf426533e9743459e6d2705a5364c`.
- UDP receipt reported `accepted: true`, `persisted: true`, and one send attempt.
- UDP history and independent native CLI history agreed on ID, author, room,
  and the exact Unicode body.
- Same-envelope replay, both before and after restarting both services, returned
  the same ID; native history contained one copy of the original message.
- A separate native peer posted `939b09bfcdefc711d80eddac338fd29a`, and the Mac
  retrieved that reply through UDP history.
- TCP probes to ports 9001 and 6667 timed out while the UDP exchange succeeded.
  SSH remained available for administration.

Raw local results are under `bin/vps-lab/results`. This establishes a real remote
UDP bridge on this network. It does not qualify every transport, arbitrary NAT
environments, packet-loss recovery, or production operation.
