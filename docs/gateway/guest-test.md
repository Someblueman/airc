# Restricted guest-account UDP test

Verified on the dedicated VPS on 2026-10-09 (Europe/London): an unprivileged
account can communicate through a gateway owned by a different Unix user, with
its TCP traffic blocked and no access to the gateway's files or native socket.

```text
airc-guest (UID 1001)
  -> loopback UDP 127.0.0.1:9002
  -> gateway running as ubuntu (UID 1000)
  -> private native Unix socket
  -> AIRC, author udp-guest, room #guest-lab
```

## Account and network boundary

`airc-guest` has a locked login password, only its own group, and no sudo access.
Its private client keys are under `/home/airc-guest/airc-client`, owned by that
user: directory mode 0700, files mode 0600. Shared executables under
`/usr/local/lib/airc-lab` are root-owned and executable by the guest.

`airc-lab-guest-firewall.service` loads `/etc/airc-lab-guest.nft`. Its independent
`inet airc_guest_test` output chain rejects TCP packets from sockets owned by
`airc-guest`, for both IPv4 and IPv6, including loopback. It leaves UDP available
and does not change `ubuntu`'s network access. Reloading replaces the rule
atomically. Stopping this service removes the guest TCP restriction.

This is an ordinary Linux account plus a socket-owner firewall restriction, not
a container or a complete sandbox. The account has no privilege to change that
rule. Administrative SSH launches and observes the guest commands; the guest
client opens its own UDP socket and uses its own keys.

The guest's gateway runs as `ubuntu` under `airc-lab-guest-gateway.service`, using
the **same application binary** as the original remote lab. Its listener is
loopback-only and needs no additional public firewall opening. Its configuration
is `/home/ubuntu/airc-test/gateway.guest.json`, and its separate durable journal
is `/home/ubuntu/airc-test/guest-journal`. It uses principal `guest`, native binding
`guest-native`, and reply route `guest-return` at `127.0.0.1:49153`.

The gateway's private directory remains mode 0700, owned by `ubuntu`. The guest
cannot read either native account token, gateway configuration, or gateway
private keys, and cannot connect directly to `native.sock`.

## Repeat the exchange

Log in as the administrator, then enter the restricted account:

```sh
ssh ubuntu@vps-2bb89225.vps.ovh.ca
sudo -iu airc-guest
```

All following commands run as `airc-guest`:

```sh
id
umask 077
request_id="guest-$(date +%s)"
printf '{"version":1,"request_id":"%s","operation":"send","target":{"kind":"room","name":"#guest-lab"},"message":"Hello from the TCP-blocked guest"}\n' "$request_id" \
  | /usr/local/lib/airc-lab/udp-lab envelope --dir "$HOME/airc-client" --signing-id guest-signing > "$HOME/send.jwe"
/usr/local/lib/airc-lab/guest-call < "$HOME/send.jwe"

printf '{"version":1,"request_id":"receipt","operation":"receipt.get","lookup_id":"%s"}\n' "$request_id" \
  | /usr/local/lib/airc-lab/udp-lab envelope --dir "$HOME/airc-client" --signing-id guest-signing \
  | /usr/local/lib/airc-lab/guest-call

printf '%s\n' '{"version":1,"request_id":"history","operation":"history.read","target":{"kind":"room","name":"#guest-lab"},"start":"recent","limit":10}' \
  | /usr/local/lib/airc-lab/udp-lab envelope --dir "$HOME/airc-client" --signing-id guest-signing \
  | /usr/local/lib/airc-lab/guest-call
```

Poll a fresh receipt until `accepted`, or inspect a terminal failure. Save and
reuse `send.jwe` for a retry of the same send; do not regenerate its timestamps
or request ID. History bodies use base64url, as in the [remote lab](vps-test.md).

As the guest, this TCP connection to the host's running SSH listener fails:

```sh
python3 -c 'import socket; socket.create_connection(("127.0.0.1", 22), timeout=2)'
```

After exiting the guest shell, the administrator can inspect the firewall's
rejection counter, service users, and native message history:

```sh
sudo nft list table inet airc_guest_test
ps -o user,uid,pid,args -C airc-gateway
/home/ubuntu/airc-test/bin/airc history '#guest-lab' --unix /home/ubuntu/airc-test/native.sock --json
```

## Evidence and provisioning limitation

Positive controls confirmed that `ubuntu` could reach the same IPv4 TCP, IPv6
TCP, and temporary HTTP listeners that rejected `airc-guest`. The guest could
not read gateway secrets, open the native Unix socket, or use sudo.

The guest then sent native message `62001146d643329180b079afea05642c`, receiving
an accepted/persisted receipt with one send attempt. Independent native history
matched its exact ID, author, room, and Unicode text. Replay before and after a
gateway restart returned the same ID with one attempt and one native copy. The
guest also retrieved native peer reply `330a563917e5ece0ae24aa1d3ef6bc6b` over UDP.
TCP remained blocked after reloading the firewall. Results are in
`/home/airc-guest/results` and locally in `bin/vps-guest/results` (ignored by Git).

Adding the guest to the **original initialized gateway** exposed a separate
limitation: its journal pins the complete realm/principal/backend-binding set.
Even adding a new binding or principal causes startup to fail with
`durable realm or identity binding changed`; static configuration validation does
not catch that runtime mismatch. The original configuration was restored and its
existing Mac receipt was verified again. This test therefore uses a separate
gateway instance and fresh journal. No existing journal was deleted or edited,
and no production gateway code was changed to bypass the restriction.

The result establishes cross-account UDP communication with provisioned keys.
It does not establish automatic guest enrollment, access from a separate network
namespace, or communication when UDP is also prohibited.
