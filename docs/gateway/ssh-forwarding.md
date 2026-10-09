# SSH forwarding acceptance

The opt-in local and reverse tests run an isolated, unprivileged OpenSSH server
on loopback. They use ephemeral host and client keys, pin the host key in a
private `known_hosts` file, disable the user SSH config and agent, reject an
unlisted client key, and constrain the allowed forwarding direction and target
or listener port.

Set the mode's environment variable to `1` to opt in. The local test uses
`ssh -L`; the reverse test uses `ssh -R`. Both verify the gateway certificate
and send through the ordinary framed request protocol. An independent native
IRC observer checks author, target, and complete message bodies. The tests also
check invalid gateway credentials, stable send replay, single-message history
pages and resume cursors, then join both SSH processes during cleanup. No SSH
adapter or other tunnel mode is added.

Run each acceptance with:

```sh
AIRC_TEST_SSH_FORWARD=1 go test ./integration/gateway -run '^TestFramedTLSAuthenticatedSendAndBoundedResumeThroughSSHLocalForward$' -count=1 -timeout=90s
AIRC_TEST_SSH_REVERSE=1 go test ./integration/gateway -run '^TestFramedTLSAuthenticatedSendAndBoundedResumeThroughSSHReverseForward$' -count=1 -timeout=90s
```

This qualifies SSH local and reverse forwarding on the test host. It does not
qualify SOCKS, a remote deployment, or other network paths.

On Darwin ARM64 with OpenSSH `10.3p1`, both opt-in tests passed normally and
under the race detector:

| Mode | Normal | Race |
| --- | ---: | ---: |
| Local forwarding | 5.847s | 9.140s |
| Reverse forwarding | 6.028s | 8.643s |
