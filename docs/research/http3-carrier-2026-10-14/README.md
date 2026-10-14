# HTTP/3 carrier for airc

Measured 14 October 2026 on the `hostile-networks` branch against `main`
(`88fb026`), on a 4-vCPU Xeon with Go 1.27.0.

## What changed

- **The `airc` client speaks `http3`.** That is the gateway's HTTP routes over QUIC (quic-go v0.63.0), always with TLS.
  - It is raced like the other carriers, against every address of its host, and reports the gateway's handshake packets to the race.
  - It dials a new connection once the gateway has closed an idle one.
  - QUIC runs over UDP, so it reaches a gateway where TCP is blocked or throttled.
  - Its connection survives the client's address changing without a new handshake. A test moves the client to a new source address mid-connection, and the next read succeeds on the same connection.
- **The bundled service.** `airc service install --gateway http3` serves it on UDP with aircd's certificate, by default on `http`'s port number. The gateway now lets a UDP and a TCP listener share a port. Service readiness probes UDP carriers over UDP, which fixes `udp` and `coap_udp` readiness too.
- **Docs.** `docs/SERVICE.md` covers running HTTPS and HTTP/3 on port 443 for networks that only let web traffic out.

## Latency (loopback)

[latency-harness.go.txt](latency-harness.go.txt) runs both carriers on one port of one gateway, both with TLS. It uses 40 cold posts per carrier (new client, connection, post and receipt) and 100 warm 50-message room reads on one connection ([latency-samples.txt](latency-samples.txt)).

| Operation | HTTPS p50 | HTTP/3 p50 |
| --- | ---: | ---: |
| Cold post | 57.7 ms | 58.8 ms |
| Warm 50-message read | 2.7 ms | 2.8 ms |

They are equal within noise on loopback. The cold post is dominated by the gateway's receipt polling, not the transport. On real networks QUIC needs one round trip to connect, where TCP plus TLS 1.3 needs two.

## Cost

- **Size.** Installed `airc` (`-trimpath -ldflags='-s -w'`) grows from 9,564,423 to 11,182,343 bytes (+1.6 MB), over the earlier 10 MB target, as agreed.
- **Startup.** `airc version` started in 4.2 ms on `main` and 4.5 ms on the branch (p50 of 60 interleaved runs), which is within noise.
- **Tests.**
  - `go test -race` passes for `./pkg/...`, `./cmd/...`, `./internal/service` and `./internal/gateway/config`.
  - gofmt, vet and staticcheck are clean.
