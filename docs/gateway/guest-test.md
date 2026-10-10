# Restricted guest-account UDP test

Verified on the dedicated VPS on 2026-10-09 (Europe/London): an unprivileged
account can communicate through a gateway owned by a different Unix user, with
its TCP traffic blocked and no access to the gateway's files or native socket.
The guest listener now uses [anonymous UDP](anonymous-udp.md): clients need no
keys, enrollment, fixed local port, or per-client gateway binding.

```text
airc-guest (UID 1001)
  -> loopback UDP 127.0.0.1:9002
  -> gateway running as ubuntu (UID 1000)
  -> private native Unix socket
  -> AIRC, author udp-guest, room #guest-lab
```

## Account and network boundary

`airc-guest` has a locked login password, only its own group, and no sudo access.
Shared executables under `/usr/local/lib/airc-lab` are root-owned and executable
by the guest. The anonymous client reads no credential files.

`airc-lab-guest-firewall.service` loads `/etc/airc-lab-guest.nft`. Its independent
`inet airc_guest_test` output chain rejects TCP packets from sockets owned by
`airc-guest`, for both IPv4 and IPv6, including loopback. It leaves UDP available
and does not change `ubuntu`'s network access. Reloading replaces the rule
atomically. Stopping this service removes the guest TCP restriction.

This is an ordinary Linux account plus a socket-owner firewall restriction, not
a container or a complete sandbox. The account has no privilege to change that
rule. Administrative SSH launches and observes the guest commands; the guest
client opens its own UDP socket on an automatically selected local port.

The guest's gateway runs as `ubuntu` under `airc-lab-guest-gateway.service`, using
the **same application binary** as the original remote lab. Its listener is
loopback-only and needs no additional public firewall opening. Its configuration
is `/home/ubuntu/airc-test/gateway.guest.json`, and its separate durable journal
is `/home/ubuntu/airc-test/guest-journal`. It uses principal `guest`, native binding
`guest-native`, with `anonymous_principal: "guest"` on the listener and no return
routes. All clients use native author `udp-guest` and may access `#guest-lab`.

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
request_id="guest-$(cat /proc/sys/kernel/random/uuid)"
printf '{"version":1,"request_id":"%s","operation":"send","target":{"kind":"room","name":"#guest-lab"},"message":"Hello from the TCP-blocked guest"}\n' "$request_id" \
  > "$HOME/send.json"
/usr/local/lib/airc-lab/guest-call < "$HOME/send.json"

printf '{"version":1,"request_id":"receipt","operation":"receipt.get","lookup_id":"%s"}\n' "$request_id" \
  | /usr/local/lib/airc-lab/guest-call

printf '%s\n' '{"version":1,"request_id":"history","operation":"history.read","target":{"kind":"room","name":"#guest-lab"},"start":"recent","limit":10}' \
  | /usr/local/lib/airc-lab/guest-call
```

Poll a fresh receipt until `accepted`, or inspect a terminal failure. Save and
reuse `send.json` for a retry of the same send; keep the request ID and intent.
History bodies use base64url, as in the [remote lab](vps-test.md). The client pads
requests to 4096 bytes so replies can fit without UDP amplification. Request fewer
history messages or pass `--max-response-bytes 8192` if a page does not fit.

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

## Original signed test and provisioning limitation

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
and the anonymous mode does not bypass that journal restriction. Switching the
existing guest listener to anonymous access keeps its principal/binding IDs and
journal, so old receipts remain readable without provisioning new client entries.

The original result established cross-account UDP communication with provisioned
keys. Anonymous access removes that client provisioning requirement; the
operator's shared native account and permissions still exist. Neither test
establishes communication when UDP is also prohibited.

## Anonymous verification, 2026-10-09

The same restricted UID 1001 ran a Python standard-library UDP client with an
empty environment and `HOME=/nonexistent`. No keys or enrollment were used. Both
TCP families remained blocked, while the same SSH listeners were reachable as
`ubuntu`; gateway private files and the native Unix socket remained inaccessible.

Its send `anon-7d77794b14244ec9a1f7f045d292117f` produced native message
`085b84ff2b3afd536d9279ca3c143690`, accepted and persisted with one attempt.
Independent native CLI history matched the exact ID, author, room, and Unicode
body. Nine ephemeral source ports received replies without configuration.
Oversized input was discarded; undersized response budgets returned bounded
errors; conflicting intent and forbidden rooms were rejected.

After restarting both gateway services, unchanged send replay returned the same
ID and one attempt, with one native copy. The guest retrieved peer reply
`f97085eda9d2c574dc0c7c430469f743` over raw UDP. The unprovisioned `nobody` account
(UID 65534, `HOME=/nonexistent`) repeated that replay/history exchange successfully,
demonstrating the shared identity. The original Mac signed/encrypted client also
retrieved its original accepted receipt through public UDP 9001 after restart.

The guest journal was retained. Its original signed-client receipt
`62001146d643329180b079afea05642c` is now readable through anonymous mode too.
Proof scripts and results are locally under `bin/anonymous-udp-check` (ignored).
The Linux amd64 binaries were built from `da8ad13` plus this anonymous change,
excluding the unrelated pending transport work. SHA-256:

- Gateway: `18242b2b7f032e7a2b9b3a57ffb73943a61d8eeaf0bf8ef9cc7d4cd2447e968c`.
- Anonymous client: `7303bd603ce50d9c53e795bf86a63070fe38cc816062db21b1c54b9459c2741c`.

Both gateway services use the updated binary; native server/account state is
unchanged. The anonymous listener is loopback-only. The public remote listener
still requires its existing signed/encrypted protocol.

The full normal suite passed, as did gateway integration and adapter tests under
the race detector. Concurrent refactoring changed the shared checkout during
that race run, causing build failures in auth/core/dispatch/store; the complete
shared-tree race gate therefore did not pass. Those four packages subsequently
passed race tests against the immutable deployment snapshot, along with build,
vet, staticcheck, formatting, and shell checks. Logs from those checks are under
`bin/anonymous-udp-check`.

A later full check of the combined source, including the simplification recorded
in `d75e9d7`, passed both normal and race acceptance: 4,056 leaf tests passed and
59 existing platform/opt-in cases were skipped in each run. Build, vet,
staticcheck, formatting, and whitespace checks also passed. The executable
source was unchanged through those runs. Their command output is retained in
`/tmp/airc-simplification-normal.jsonl` and
`/tmp/airc-simplification-race.jsonl`. These checks do not qualify the skipped
broadcast fixture or unfinished Linux multicast delivery.
