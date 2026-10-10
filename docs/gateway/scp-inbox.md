# OpenSSH SCP upload inbox

The selected transfer service is **OpenSSH**, using its forced-command facility
and the legacy SCP upload protocol selected by `scp -O`. The gateway consumes
one encrypted, author-signed native Send request from the SSH session. It does
not write an uploaded file or interpret a remote pathname. This is a Send-only
inbox; the separately implemented SFTP mailbox supports protected read replies.
Testing and deployment are deferred.

## Configuration and account mapping

Configure exactly one enabled listener and no enabled broker or mailbox owners:

```json
{
  "listeners": [{
    "id": "alice-scp",
    "protocol": "scp_inbox",
    "address": "",
    "enabled": true,
    "operations": ["send"],
    "scp_inbox": {
      "login_uid": 1001,
      "principal": "alice",
      "target": {"kind": "room", "name": "#general"}
    }
  }]
}
```

Replace UID 1001 with the dedicated, unprivileged SSH account's numeric UID.
The command checks its effective UID against this fixed mapping before opening
the journal. UID zero and values above 2147483647 are refused. The principal
must be active, hold a signing key and grant Send to the exact target. Configure
the usual backend binding and request-decryption keys. TLS, origins, anonymous
access and a listener address are not used. Darwin and Linux are supported by
the existing deadline-capable inherited-pipe boundary.

Provision a dedicated SSH account, verified host key and explicitly authorized
public keys. Restrict all access to the fixed command, without a PTY, forwarding,
user startup scripts, password login or unrestricted alternate keys. Keep the
SSH policy, authorized keys, executable and fixed configuration under trusted
administrator control. The gateway does not install or audit SSH policy.
One account/configuration maps to one native principal and target.

For a provisioned account with those restrictions, an authorized-key entry can
use OpenSSH's [forced command and restrict
options](https://man.openbsd.org/sshd.8#AUTHORIZED_KEYS_FILE_FORMAT):

```text
restrict,command="/opt/airc/bin/airc-gateway scp-inbox --file /etc/airc/alice-scp.json" ssh-ed25519 <provisioned-public-key> alice-upload
```

Do not substitute client-controlled text into this command or execute
`SSH_ORIGINAL_COMMAND`. The gateway compares that variable to the exact literal
`scp -t inbox`; all other commands are refused before journal creation. The
variable only constrains protocol syntax. SSH authenticates the account, the
effective UID selects its configured mapping, and the signed encrypted payload
independently proves the native author and current grants. Environment values,
filenames and the SSH peer address grant no native authority.

The command requires separate stdin/stdout pipes, as used by OpenSSH's ordinary
non-PTY command execution. Terminals and regular-file descriptors are refused.
Ordinary `airc-gateway serve` refuses this listener; the forced command is:

```sh
airc-gateway scp-inbox --file /etc/airc/alice-scp.json
```

## Producer and acknowledgement contract

Create a local file named `request.jwe`, mode 0600, containing exactly the compact
encrypted native intent, without a newline or outer JSON framing. Retain that
file and its stable native request ID until the outcome is resolved. Its size
must be 1–16384 bytes. Upload a single file to the fixed destination:

```sh
scp -O -o BatchMode=yes -o StrictHostKeyChecking=yes \
  -o UserKnownHostsFile=/private/client/known_hosts \
  -i /private/client/upload_key request.jwe airc_upload_alice@gateway.example.net:inbox
```

Provision the expected server host key in that known-hosts file. OpenSSH's
[`-O` option selects the legacy SCP protocol](https://man.openbsd.org/scp.1#O);
its default SFTP protocol is a different connector. This profile accepts neither
recursive copies, preserved timestamps, verbose remote commands nor multiple
files. The only accepted control header is
`C0600 <canonical-decimal-size> request.jwe` followed by a newline.

The receiver sends the initial protocol acknowledgement, checks the header and
acknowledges it, then reads exactly the declared body and sender success byte.
Only after validating and durably admitting the native Send does it send the
final acknowledgement. Refusals use a fixed SCP error message without native
content or credential details. The gateway never opens a path named by the
client and emits no readiness or JSON records onto the SCP stream.

A final acknowledgement means durable native journal admission, including an
identical retained request under current grants. It does not mean final AIRC
delivery. An expired request can recover only an identical retained Send; it
cannot create another job or renew freshness. A missing final acknowledgement
or failed SSH exit leaves the outcome uncertain: retry the original intent and
stable ID, keeping the same journal. Do not generate a replacement ID merely
because the transfer failed.

After successful intake, the one-shot command gives its delivery worker up to
five seconds to drain pending work, then cancels and joins it. Pending jobs
remain in the journal for a later invocation. Use a separately authorized native
receipt/read path to resolve final acceptance; this upload profile publishes no
native receipt body or read result. Its return path carries only SCP controls.

## Bounds and deferred verification

There is one upload and admission per process, with exclusive journal ownership;
concurrent commands using the same state directory are refused. Provision SSH
connection/session limits for the account as part of the transfer service.
The protocol header is limited to 64 bytes, transfer reads to ten seconds,
each write to three seconds and the receive operation to twenty seconds.
Admission has a four-second budget including shared request-capacity wait.
Signals and deadlines close the inherited stream and join its cancellation
callback. Delivery workers join before journal close. No dependency, provider
resource or journal schema migration is added.

Production `go build ./...` passes for Darwin ARM64 and Linux AMD64/ARM64 with
`CGO_ENABLED=0`. Source review, formatting, whitespace and source-size advisory
checks pass. These checks do not establish runtime acceptance.

Real OpenSSH upload interoperability, account restrictions and foreign-UID
refusals, native AIRC author/target/body, original freshness, lost-acknowledgement
replay, process interruption, malformed controls and joined shutdown remain
unexecuted by owner direction.
