# airc

IRC-style chat server and CLI for humans and AI agents. Go 1.25, module `github.com/Someblueman/airc`.

## Commands

```sh
go build ./cmd/...                      # binaries at the repo root are gitignored
test -z "$(gofmt -l .)"                 # CI fails on any unformatted file
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
go test -race -count=1 ./...            # what CI runs (ubuntu + macos)
go test -run '^$' -fuzz FuzzParse -fuzztime 20s ./internal/protocol/
go test ./internal/server -run '^$' -bench '^BenchmarkHistory' -benchmem
```

## Layout

- `cmd/aircd` — the daemon; `cmd/airc` — CLI, full-screen UI (`ui_*.go`) and MCP server (`mcp*.go`); `cmd/airc-load` — load generator (docs/LOAD_TESTING.md)
- `internal/protocol` — wire parsing and message formats, shared by client and server
- `internal/server` — rooms, history, accounts, replies, search, persistence
- `internal/service` — launchd/systemd service install and control (docs/SERVICE.md)
- `pkg/irc` — public Go client used by `cmd/airc`; `pkg/bot` — bot framework
- `skills/airc/` — agent skill (`SKILL.md`, `REFERENCE.md`), embedded into the `airc` binary by `skills/embed.go`
- `docs/PROTOCOL.md` — extensions and deliberate differences from IRC; `docs/research/` — dated measurement evidence, not living docs

## Gotchas

- `cmd/airc/skill_test.go` checks every `airc <command>` and `--flag` mentioned in the skill against the CLI. When renaming or adding a command or flag, update `skills/airc/*.md` and that test's command list together.
- Some tests are opt-in: the 30-minute soak needs `AIRC_SOAK_DURATION=30m`, the RTT measurement needs `AIRC_WARM_MEASURE`, and `ui_pty_test.go` skips without `python3`.
- Test against a scratch server on its own port and state dir (`AIRC_STATE_DIR`), never the default 127.0.0.1:6667. Stop test processes by PID, not `pkill -f airc`.
- Replacing `aircd` on disk does not change the running daemon; a restart is a separate, coordinated step.
