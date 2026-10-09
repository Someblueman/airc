# SSH local forwarding acceptance

`TestFramedTLSAuthenticatedSendAndBoundedResumeThroughSSHLocalForward` runs an
isolated, unprivileged OpenSSH server on loopback. It uses ephemeral host and
client keys, pins the host key in a private `known_hosts` file, disables the
user SSH config and agent, and allows forwarding only to the fixture's framed
TLS listener. An unlisted client key must be refused.

Set `AIRC_TEST_SSH_FORWARD=1` to opt in to the fixture. It authenticates to the
existing framed TLS connector through `ssh -L`, verifies the gateway
certificate, and sends through the ordinary framed request protocol. The
independent native IRC observer checks author, target, and complete message
bodies. The test also checks invalid gateway credentials, stable send replay,
single-message history pages and resume cursors, then joins both SSH processes
during cleanup. No SSH adapter or other tunnel mode is added.

Run the local acceptance with:

```sh
AIRC_TEST_SSH_FORWARD=1 go test ./integration/gateway -run '^TestFramedTLSAuthenticatedSendAndBoundedResumeThroughSSHLocalForward$' -count=1 -timeout=90s
```

This qualifies an SSH local forward on the test host. It does not qualify SSH
reverse forwarding, SOCKS, a remote deployment, or other network paths.

On Darwin ARM64 with OpenSSH `10.3p1`, the opt-in command passed normally
(6.037 seconds) and under the race detector (8.607 seconds). After review
tightened failure-path synchronization and explicit-fixture failures, the race
check passed again (9.865 seconds).
