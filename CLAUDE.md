# airc

IRC-style chat server and CLI for humans and AI agents. Go 1.27, module `github.com/Someblueman/airc`; the gateway is a second module, `github.com/Someblueman/airc/gateway` in `gateway/`, joined by `go.work`.

## Commands

```sh
go build ./... ./gateway/...            # both modules; a binary built at the repo root is gitignored
test -z "$(gofmt -l .)"                 # every file must be gofmt-clean
go vet ./... ./gateway/...
go tool staticcheck ./... ./gateway/... # the version pinned by the root go.mod's tool directive
GOWORK=off go test -race -count=1 -timeout 60m ./...    # the product: aircd, airc, airc-load and their packages (~1 min)
go test -C gateway -race -count=1 -timeout 120m ./...   # the gateway, its integration suite included (~25 min); there is no CI, so run both before pushing
go test -run '^$' -fuzz FuzzParse -fuzztime 20s ./internal/protocol/
go test ./internal/server -run '^$' -bench '^BenchmarkHistory' -benchmem  # FullWindow fills 1 GiB
scripts/githooks/install.sh             # git hooks: gofmt and vet on commit; gofmt, vet, staticcheck and the skill test on push
```

In workspace mode `./...` from the root still names only the root module's packages, so every command above that should cover the gateway names `./gateway/...` too or runs inside `gateway/` (`-C gateway`). `GOWORK=off` at the root is how a consumer of the product module builds it.

## Layout

- `cmd/aircd` — the daemon; `cmd/airc` — CLI, full-screen UI (`ui_*.go`) and MCP server (`mcp*.go`); `cmd/airc-load` — load generator (docs/LOAD_TESTING.md)
- `internal/protocol` — wire parsing and message formats, shared by client and server
- `internal/server` — rooms, history, accounts, replies, search, persistence
- `internal/service` — launchd/systemd service install and control (docs/SERVICE.md); it writes the gateway's JSON configuration itself and never imports a gateway package
- `pkg/irc` — public Go client used by `cmd/airc`; `pkg/bot` — bot framework; `pkg/fallback` — the CLI's gateway client
- `skills/airc/` — agent skill (`SKILL.md`, `REFERENCE.md`), embedded into the `airc` binary by `skills/embed.go`
- `gateway/` — the multi-transport gateway, its own module: `gateway/cmd/airc-gateway`, `gateway/internal/...` (adapter, config, core, dispatch, service, store, ...), `gateway/integration` (the cross-carrier suite), `gateway/api/v1` (the gRPC contract), `gateway/examples`. It imports the root module's shared packages (`internal/protocol`, `internal/mcpserver`, `pkg/irc`, `pkg/fallback`, the `internal/test*` helpers); nothing in the root module imports it, which is what keeps quic-go's server side, go-coap, pion, sqlite, grpc and protobuf out of `aircd` and `airc`. `GOWORK=off go list -deps ./cmd/aircd ./cmd/airc | grep /gateway/` must print nothing.
- `docs/PROTOCOL.md` — extensions and deliberate differences from IRC; `docs/gateway/README.md` — the gateway; `docs/research/` — dated measurement evidence, not living docs

## Gotchas

- `cmd/airc/skill_test.go` checks every `airc <command>` and `--flag` mentioned in the skill against the CLI. When renaming or adding a command or flag, update `skills/airc/*.md` and that test's command list together.
- `cmd/airc`'s fallback tests build `airc-gateway` from `gateway/` once per test binary and run it as a child process (`gateway_fixture_test.go`); they need the gateway module's dependencies in the module cache but link none of them.
- Some tests are opt-in: the 30-minute soak needs `AIRC_SOAK_DURATION=30m`, the fault-injection soak across aircd and the gateway (`gateway/integration`) needs `AIRC_CHAOS_DURATION=30m` (`AIRC_CHAOS_SEED` replays a run's faults), the RTT measurement needs `AIRC_WARM_MEASURE`, and `ui_pty_test.go` skips without `python3`.
- Test against a scratch server on its own port and state dir (`AIRC_STATE_DIR`), never the default 127.0.0.1:6667. Stop test processes by PID, not `pkill -f airc`.
- Replacing `aircd` on disk does not change the running daemon; a restart is a separate, coordinated step.
