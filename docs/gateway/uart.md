# UART and virtual serial framing

`serve-uart` owns one explicitly selected tty for one authenticated TLS session.
The line settings are fixed at 115200 baud, eight data bits, no parity, one stop
bit, raw binary mode and no software or hardware flow control. It does not scan
for devices, change their permissions, reconnect after link loss, or configure
modems. The service account needs existing read/write access to the device.

Use this listener in an otherwise provisioned gateway configuration:

```json
{
  "id": "serial",
  "protocol": "uart",
  "address": "/absolute/canonical/tty/path",
  "enabled": true,
  "tls_cert_file": "/absolute/path/server.crt",
  "tls_key_file": "/absolute/path/server.key",
  "operations": ["send", "receipt.get", "history.read", "watch"]
}
```

Exactly one listener may be enabled, with no enabled brokers or bridges. Existing
principal signing keys, binding credentials and permissions still control AIRC
identity and targets. History/watch also require the configured cursor key.
Start the session with:

```sh
airc-gateway serve-uart --file /absolute/path/gateway.json
```

The tty path must be absolute and canonical, without symlinks. Startup verifies
the opened character-device identity, takes a nonblocking advisory `flock`,
requests kernel tty exclusivity, saves the original termios, applies the fixed
settings and checks that Go supports read/write deadlines on the descriptor.
Cleanup restores termios, releases tty exclusivity and closes the descriptor.
The advisory lock excludes other gateway openers; it does not authenticate the
peer or prevent software that ignores the lock from opening a tty. In particular,
the tested Darwin PTY did not enforce `TIOCEXCL` against another opener.

The peer negotiates TLS 1.3 with `airc-gateway/1` ALPN and verifies the configured
server certificate and name. Requests and replies then use the existing
[version-1 framed protocol](framed-stream-v1.md). Signed and encrypted request
profiles retain their existing principal checks and durable replay IDs; TLS
also protects response bodies. Access to the device is never an AIRC identity.

Frames retain the 16 KiB request and 64 KiB response bounds. The TLS handshake
has three seconds, the first request byte five seconds, later idle requests
thirty seconds, and the rest of a started request five seconds. A UART write
has ten seconds: transmitting 64 KiB at 115200 baud with 8N1 takes about 5.7
seconds before TLS overhead. Other framed carriers keep their existing
three-second write deadline. Small explicit page limits remain useful on a
serial link.

The client should be ready when the gateway starts. The command emits no
readiness record; a verified TLS handshake establishes the session. Link loss,
malformed framing, deadline expiry or a termination signal ends it visibly.
Clean EOF drains admitted handlers and allows the existing bounded delivery
grace; interruption joins workers and retains durable outcomes for a deliberate
restart. No tty path is reopened automatically.

The implementation is built for Darwin and Linux; other platforms reject an
enabled UART listener. A PTY can qualify a virtual serial path, but does not
establish electrical behavior, real baud throughput, modem control, RS-232,
RS-485 or USB serial qualification. The [Linux CDC ACM USB serial profile](usb-serial.md)
uses this implementation; real USB-device acceptance remains separate.

## Native virtual-serial qualification on Darwin and Linux

On 2026-10-09, Darwin ARM64 and Linux ARM64 passed four acceptance cases using
real kernel PTY pairs, the gateway's `serve-uart` command and a separately
connected native AIRC observer. The fixture opens and inspects the PTY through
Go and the existing `x/sys/unix` dependency; no Python or additional client
package is needed. The client verified the TLS 1.3 certificate chain and ALPN.
The fixture observed raw 115200 8N1 settings and advisory-lock ownership before
sending any TLS bytes.

The tests verified split framing, invalid authentication, two native-observed
sends under the configured AIRC author and target, exact durable replay with
one delivery attempt, complete one-record history pages through cursor
exhaustion, and oversized-frame refusal before intake. A separate 24-message
history response exceeded 48 KiB, stayed within 64 KiB and contained every
whole record in order. Shutdown joined both an active watch and a write with
response bytes queued in a full PTY output buffer; termios and lock ownership
were restored. These are virtual-link checks, not a physical baud measurement.

Both fixtures begin with verified distinct input/output rates (9600/19200).
The first Linux run exposed retained input-baud bits in `CIBAUD`; startup now
clears those bits so input follows the configured 115200 output rate. All four
Linux cases then passed. Original rates are restored at session shutdown.

```sh
go test -v ./integration/gateway -run '^TestUART' -count=1 -timeout=120s
go test -race -v ./integration/gateway -run '^TestUART' -count=1 -timeout=180s
```

The four-case Darwin normal run passed in 5.830 seconds and race run in 11.173
seconds with Go 1.27.1. Affected framed, QUIC, WebTransport and local-stream race
checks also passed. Logs are `/tmp/airc-uart-native-pty-darwin-normal.out`,
`/tmp/airc-uart-native-pty-darwin-race.out` and
`/tmp/airc-uart-affected-race.out`. Relevant vet, staticcheck, formatting and diff
checks passed. The Darwin test-only PTY-name ioctl has a documented local
staticcheck deprecation exception because `x/sys` has no variable-buffer
wrapper; production UART uses its libSystem-backed termios wrappers.

The Linux run used host-cross-compiled, cgo-disabled binaries in the cached
Ubuntu 24.04 ARM64 image
`sha256:33ceb71981b602c1a7443a53469e4dba065f7503eab3078a2d7a57a2ab987517`.
The container had no network, a read-only root and artifacts, no capabilities,
no-new-privileges, and an automatically removed local volume for private test
state. It exited zero in 1.166 seconds, including container startup. The test
fixture verified the supplied gateway and native daemon SHA256 values before
execution. Exact command, hashes and output are in
`/tmp/airc-uart-linux-native-20261009/provenance.json` and
`/tmp/airc-uart-linux-native-20261009/native-acceptance.log`; the failing
pre-correction witness is `before-input-baud-fix.log` in the same directory.
No image or package was obtained. Linux race execution is not claimed.

These timings establish acceptance, not a speed comparison. Physical UART,
RS-232, RS-485, modem and USB serial links remain unqualified.
