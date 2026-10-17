# Staying connected through the gateway

Built 17 October 2026 on the `stay-connected` branch from `main`
(`a3a8b1d`), Linux AMD64, Go 1.27.0. This note records what was built,
what the tests prove, and what was not measured. The plan it follows is
summarized at the end.

## What changed

1. **Session reclaim in aircd.** A persistent session that lost its link
   silently kept its nickname until the two-minute read timeout, so a
   client that noticed first was refused with `433` until then.
   Registration now issues a resume secret (`767`); a new connection
   presenting it with `RESUME`, or authenticated as the same account, takes
   the stale session's nickname, channels, observations and away state, is
   shown them as if it had just joined, and the stale session closes
   without `QUIT` or offline notices. `pkg/irc` presents the secret on every
   reconnect. Advertised as `RESUME=1` ([protocol](../../PROTOCOL.md#extensions)).
2. **Native tunnels.** `irc_websocket` (the IRCv3 WebSocket convention) and
   `irc_connect` (HTTP CONNECT) carry aircd's own protocol through the
   gateway after `WEBIRC`, so nothing on the native feature set is lost
   through port 443 or a proxy. `airc` races them beside aircd's port in
   every command, including the ones that had no fallback before
   ([native](../../gateway/native.md)).
3. **Followed checks.** On a connection carrier a check with
   `stream: true` is one stream for `limits.follow_seconds` (default five
   minutes): the snapshot, a frame per batch of news, an empty frame every
   `limits.follow_keepalive_seconds` while quiet, each frame carrying the
   positions to resume from. `airc watch`, the full-screen UI and
   `check --wait` follow their rooms this way while native IRC is down
   ([following](../../gateway/contract-v1.md#following)).
4. **Transport.** The QUIC carriers accept 0-RTT (the contract is
   replay-safe) and `airc` resumes TLS sessions, so a redial after an idle
   close costs no round trip. A connection with a stream active is no
   longer closed for the client's silence.

## Evidence

Every item is covered by tests in this tree (`go test -race`), none by a
third-party client yet:

| Change | Test | What it shows |
| --- | --- | --- |
| Reclaim, guest | `internal/server` `TestGuestReclaimsAStaleSessionWithItsSecret` | takeover with the secret; wrong secret, ephemeral and spent secret refused; the room and a monitor see no departure or arrival |
| Reclaim, account | `TestAccountReclaimsItsOwnStaleSession` | takeover by the same account, including a standard client that sends `NICK` during `CAP` |
| Reclaim, client | `pkg/irc` `TestReconnectTakesOverTheSessionTheServerStillHolds` | through a proxy that goes silent without closing the server side, the client is back in its room on the first reconnect, with no `QUIT` or second `JOIN` seen by a peer |
| Tunnels | `pkg/fallback` `TestNativeTunnelsCarryNativeIRC`, `integration/gateway` `TestNativeTunnelsThroughTheService`, `cmd/airc` `TestNativeTunnelsCarryNativeOnlyCommands` | posts, retries, receipts, history and discovery through both tunnels as a guest and an account; `unread`, a native-only command, runs through a tunnel with native IRC down |
| Follow | `integration/gateway` `TestBackendFollowStreamsNewsForItsLease`, `TestFollowOverAFramedStream`; `pkg/fallback` `TestFollowStreamsARoomUntilTheLeaseEnds`, `TestFollowOutlivesTheConnectionIdle` | snapshot, news within the test's timeout, keepalives, lease end as `lease_expired`, resume without repeats; a quiet follow outlives the connection idle bound on framed and WebSocket |
| 0-RTT | `pkg/fallback` `TestHTTP3EndpointDialsByNameAndRedials` | the redial reports `Used0RTT` |

## Not measured

The plan's targets were not run on a harness here: silent drop to a bot
posting again (the test proves promptness, not a number), requests per
second for 200 quiet followers (the 2026-10-14 harness polls `check`; it
has not been taught to follow), and the saved round trip of 0-RTT on a
real network. The datagram tests that need multicast, UDP-Lite over IPv6
and `/dev/mqueue` fail in this container as they do on `main`.

## Left for later

WebSocket over HTTP/2 and HTTP/3, per-listener idle and keepalive
settings, multipath QUIC, MQTT 5 sessions, Web Push and wake webhooks, and
dual-carrier following ([roadmap](../../gateway/ROADMAP.md)).
