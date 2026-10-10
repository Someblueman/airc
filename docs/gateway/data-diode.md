# Data-diode ingress through signed bundle files

The selected application boundary is one-way delivery of the existing
[version-1 signed bundle](offline-bundle.md) into a receiving host's local
filesystem, followed by `airc-gateway import-bundle`. A provisioned hardware
diode and its file-transfer endpoints carry the original bytes. The gateway
does not implement the appliance's physical link or transfer protocol.

This uses an implemented Send-only importer on Darwin and Linux. It neither
opens a connection back across the diode nor needs a sender acknowledgement,
TLS handshake, read request or response route on that link. Ordinary framed
TLS and the gateway's bidirectional UDP request/result profile are not the
selected carrier here. Hardware one-way enforcement remains a property of
the provisioned diode, not a claim made by the importer.

## Delivery and import

Provision the sender's existing signing key and the receiver's exact native
principal, binding and Send grants before transfer. The source creates the
ordinary bundle JSON containing 1–32 original compact signed envelopes. Each
envelope is at most 32 KiB; the entire file is at most 1 MiB. Signatures do not
encrypt the content. The diode endpoints must preserve the complete bytes;
filenames, appliance accounts and source addresses confer no AIRC authority.

Use the appliance's supported one-way file delivery to a receiver-owned inbox.
Configure its receiver to finish and close each file before exposing it for
import, for example by publishing a completed temporary file under a new final
name. Keep partially received files separate. This file-completion contract is
required provisioning; the importer is a bounded one-shot command, not a file
watcher or an appliance controller. It never executes, deletes or acknowledges
the received file.

Configure exactly one enabled ingress in the receiving gateway configuration:

```json
{"id":"diode-import","protocol":"offline_bundle","enabled":true,"operations":["send"]}
```

Add the ordinary native bindings, signing public keys, target grants and private
journal configuration described in [offline-bundle.md](offline-bundle.md), then
invoke the existing command locally on the receiving side:

```sh
airc-gateway import-bundle --file /absolute/path/gateway.json \
  --input /absolute/diode-inbox/completed-bundle.json
```

The importer authenticates every signed envelope before opening the journal.
It requires the original audience, validity, operation and target grants;
transport delay or duplicate delivery cannot renew a signed intent. The input
must be a bounded regular file, and a final-component symlink is refused.
Stable request identity and the existing journal retain replay and pending
delivery recovery. Intake is per entry, so inspect the complete local report
after a partial failure or replay rather than treating an exit code as rollback.

The command's JSON report remains on the receiving side. Durable intake and
native AIRC acceptance are separate states. A sender cannot infer either from
an appliance upload result, and this profile provides no remote receipt or
conversation read. A receiver operator may inspect the existing native receipt
and message history locally. No return channel is created by this profile.

## Implementation and deferred verification

The production path is `cmd/airc-gateway/offline_bundle.go` through
`service.ImportOfflineBundle`, `adapter.ReadOfflineBundle` and the existing
authenticated bundle admission/delivery worker. It requires no new gateway
wire format or codec. The importer has recorded Darwin/Linux native evidence
in [offline-bundle.md](offline-bundle.md#native-importer-acceptance).

That evidence does not qualify a diode. Actual one-way file transfer, completed
file publication, loss/duplication, delay past signed expiry, altered content,
receiver interruption and independently observed native author/target/body
remain deferred. A provisioned hardware diode, its supported file-transfer
endpoints and an isolated receiver are required for those checks. No appliance
was installed, configured or exercised while selecting this profile.
