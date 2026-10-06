# Unix sequenced-packet platform boundary

Gateway implementation and native AIRC acceptance remain outstanding. This
checkpoint establishes only which available host can support the connector.

A standalone Go 1.27.1 probe opened a real filesystem `unixpacket` listener and
client in a private temporary directory. On Darwin ARM64/macOS 26.6.2,
`net.ListenUnix` failed with `protocol not supported`; the probe returned its
explicit unsupported status 77. Darwin cannot qualify this mode through the
existing Unix-stream implementation.

The same source was cross-compiled with `CGO_ENABLED=0 GOOS=linux GOARCH=arm64`
and ran successfully in the existing Ubuntu 24.04 ARM64 container, Docker
29.7.2 and kernel `7.0.12-linuxkit`. With no network, a read-only root/artifact
mount and an automatically removed anonymous local-filesystem volume, it
verified complete 16 KiB and 64 KiB sequenced records and `MSG_TRUNC` on an
oversize 16 KiB + 1 record read into a 16 KiB buffer. Terminal exit was 0.

Probe source SHA256:
`23bb529ef8791710d98d9102b37f87632f473f532e8b9bd64826db0af55ef8ab`.
Linux executable SHA256:
`714a82ea6af6be53f79036ab2c14eed8feb9253e694d98b4e54d71cea786f5cb`.
Raw artifacts are `/tmp/airc-seqpacket-availability.go` and
`/tmp/airc-linux-native-159afc4/seqpacket-availability{,.log,.json}`.

The implementation target is Linux filesystem Unix sequenced-packet sockets.
It still needs configured sender authority, private socket ownership and peer
credentials, bounded complete-record authentication, whole response records,
joined lifecycle and independent native delivery/read/replay evidence.
The availability probe does not establish any of those gateway properties.
