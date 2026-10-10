# vsftpd FTPS inbox

The selected transfer service is **vsftpd 3.0.5 with OpenSSL on Linux**, using
explicit FTPS: authenticated TLS control sessions and protected passive data
connections. The gateway integrates through a private local directory owned by
the provisioned FTP account. It consumes signed encrypted native requests and
publishes signed encrypted whole results. The existing directory-mailbox file
boundary supplies claim, sync, exclusive ownership and nonoverwriting publication.
Testing and deployment are deferred.

## Gateway configuration

Add a listener like the following to the normal `airc-gateway serve` configuration:

```json
{
  "id": "alice-ftps",
  "protocol": "ftps_inbox",
  "address": "/srv/airc-ftps/alice/mailbox",
  "enabled": true,
  "operations": ["send", "context.read", "history.read", "search"],
  "ftps_inbox": {
    "login_uid": 1001,
    "principal": "alice",
    "target": {"kind": "room", "name": "#general"},
    "recipient_key_id": "alice-results",
    "recipient_public_key_file": "/etc/airc/keys/alice-results.pem"
  }
}
```

Replace the UID and paths with provisioned values. The gateway must run as that
unprivileged UID; the owner check rejects root or another UID. Up to 16 FTPS
listeners may be configured. Each selects an active signing principal, its
existing native binding, one canonical target and an explicit subset of Send,
context, history and search. Every selected operation and target must also be
granted to that principal. History/search require the ordinary cursor key.

Configure ingress decryption keys, the gateway response signer and the fixed
P-256 result recipient's public key. Keep all private gateway state and
credentials outside the FTP jail. The mailbox path must not overlap state,
credentials or other local mailbox resources. Listener TLS, origins, anonymous
access and separate return routes are refused; TLS belongs to vsftpd.

## Transfer-service provisioning

The [vsftpd template](ftps-vsftpd.conf) fixes the selected TLS, account, command
and capacity profile. Its directives are documented in the maintained [vsftpd
3.0.5 manual](https://manpages.ubuntu.com/manpages/jammy/man5/vsftpd.conf.5.html).
Supply a server certificate trusted by the client, its private key, an allowlist
containing only the dedicated account and a PAM service that authenticates that
account without granting shell access. The gateway does not install, configure
or attest the FTP service.

The template requires TLS 1.2 or 1.3 for login and data, session reuse on data
connections and a clean TLS upload shutdown. Use a client that supports those
requirements and verifies the server certificate/name. Plaintext fallback,
active data connections, ASCII conversion, append/resume, directory creation and
permission changes are excluded. The command allowlist still permits modifying
pathnames within the jail; it is not per-directory access control.

Use a root-owned, non-writable jail root such as `/srv/airc-ftps/alice`, with
only this account's mailbox beneath it. Provision the following tree on one
local filesystem; every listed mailbox directory is mode 0700 and owned by the
configured UID:

```text
/srv/airc-ftps/alice/mailbox/
  staging/
  inbox/
  processing/
  results/
```

The gateway checks private mailbox ownership and complete input files, while
vsftpd maps the authenticated login to that UID. It cannot prove whether a file
was created through FTPS or by a local process. The encrypted signed request
therefore remains the independent source of native authority. Filesystem
ownership, filename hashes and FTP replies do not authenticate an AIRC author.

Apply an account/filesystem quota to bound upload and staging disk use. The
gateway's input limit does not limit bytes a remote client can upload to vsftpd.
Coordinate producers so completed inbox/processing files remain immutable and
unread results are retained. Do not rename mailbox directories or modify the
gateway's `processing`, `results` or temporary publication files.

## Upload and result contract

Let `H` be the lowercase SHA-256 hex of the exact compact JWE bytes. Upload the
whole token, with no newline or outer JSON, as `mailbox/staging/H.tmp` using
binary mode and protected data. After the upload completes and its data channel
closes, rename it with RNFR/RNTO to `mailbox/inbox/H.json`. The `.json` filename
is a mailbox convention; its contents are the compact JWE, not a JSON wrapper.
The file must be 0600, single-linked and 1–16384 bytes. Publish only if that name
is absent from inbox, processing and results; do not overwrite a pending request.

The gateway claims complete files in lexical order and processes one at a time.
It checks the filename hash, then requires the exact signed principal, binding,
target and configured operation. A filename mismatch or unsafe filesystem
object stops the owner with the claim retained for operator recovery. A normal
authentication or native-operation refusal receives a protected error result.
Invalid files are not silently deleted or skipped.

Download `mailbox/results/H.json` through protected FTPS. Its contents are one
whole signed encrypted response token, up to 65536 bytes. Verify/decrypt it using
the configured gateway signer, result recipient, scope and existing response
validity rules before consuming it. Its protected payload is:

```json
{
  "version": 1,
  "protocol": "ftps/inbox",
  "request_sha256": "<H>",
  "request_id": "<native ID, present only after matching authority>",
  "target": {"kind": "room", "name": "#general"},
  "result": "<ordinary detached result; mutually exclusive with error>"
}
```

The `result` value above represents the ordinary result object, not a string on
the wire. Errors use the ordinary `core.Failure` object instead. Check the exact
request hash, ID and target. A Send result must be durable and match the original
native ID. Bounded reads preserve the whole ordinary page and explicit cursor;
each next page requires a fresh authorized request. An oversized response is
replaced by a protected capacity error, without publishing a partial page.

`FTPSInboxResponseScope` binds the response to realm, principal and binding. Its
route is SHA-256 over these NUL-joined values, without a trailing separator:

```text
airc-ftps-inbox-response-v1, listener-id, absolute-mailbox-path,
decimal-login-uid, target-kind, target-name
```

Delete only a verified consumed result. The gateway publishes a synced result
without replacement before removing its processing input. An unread result
blocks reuse of that name; 128 unread results pause intake. A crash between
publication and input removal may leave both files. Consume the verified result
first, then let the retained input recover through the journal.

FTP upload or rename success is not native admission or a durability receipt.
Keep the original request and journal across uncertain outcomes. Resubmit the
same intent and stable native ID when needed. An expired encrypted Send can
recover only an identical retained result under current durable grants; it cannot
create another job or renew freshness. Read requests require current validity.
A transient protected error can still have an uncertain Send outcome, so it is
not a reason to generate a replacement native ID. Durable intake and final AIRC
delivery remain distinct.

## Bounds and deferred verification

Each mailbox holds one exclusive directory lock and accepts at most one
processing file, 128 inbox files and 128 results. Larger inventories are refused;
the provider's quota bounds what uploaders can store. Bodies and results are
bounded as above.
Processing uses shared request capacity, a five-second pass context and a
three-second native admission context; local filesystem sync itself follows
the existing mailbox boundary. Polling waits 100 milliseconds between passes.
Shutdown joins owners before closing their directories and clearing signing
keys. A closed journal or unexpected filesystem failure stops the service.

Readiness confirms local mailbox, UID, key and authority preparation; it does
not confirm that vsftpd is running or that its TLS/login policy was verified.
No dependency or gateway journal migration is added.

Production `go build ./...` passes for Darwin ARM64 and Linux AMD64/ARM64 with
`CGO_ENABLED=0`. Source review, formatting, whitespace and source-size advisory
checks pass. These checks do not establish runtime acceptance.

Actual FTPS control/data interoperability and TLS session reuse, account/jail
policy, native AIRC Send and complete cursor reads, publication/crash recovery,
refusals, quotas and joined shutdown remain unexecuted by owner direction.
