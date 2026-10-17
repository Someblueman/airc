package main

import (
	"context"
	"io"
	"maps"
	"os"
	"os/signal"
	"slices"
	"syscall"
)

// sessionRunner runs one command over an optional leased connection. The CLI
// passes a nil session and a process context; MCP passes its call context and
// the connection it leased, so both go through the same parsers and operations.
type sessionRunner func(ctx context.Context, session *agentConnection, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error

type sessionCommand struct {
	run sessionRunner
	// mcp marks commands the MCP agent tool may run.
	mcp bool
	// signalsOnly runs the command under a context that only signals cancel,
	// with no request deadline.
	signalsOnly bool
}

// plain adapts a runner that needs neither the command name nor stdin.
func plain(run func(context.Context, *agentConnection, []string, io.Writer, io.Writer) error) sessionRunner {
	return func(ctx context.Context, session *agentConnection, _ string, args []string, _ io.Reader, stdout, stderr io.Writer) error {
		return run(ctx, session, args, stdout, stderr)
	}
}

// named adapts a runner that serves several commands and so takes the name.
func named(run func(context.Context, *agentConnection, string, []string, io.Writer, io.Writer) error) sessionRunner {
	return func(ctx context.Context, session *agentConnection, name string, args []string, _ io.Reader, stdout, stderr io.Writer) error {
		return run(ctx, session, name, args, stdout, stderr)
	}
}

// sessionCommands is the one table of commands that run over a connection
// someone else may own: dispatch and the MCP adapter both route through it.
var sessionCommands = map[string]sessionCommand{
	"send": {run: func(ctx context.Context, session *agentConnection, _ string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
		return runSendSession(ctx, session, args, stdin, stdout, stderr)
	}, mcp: true},
	"check":     {run: plain(runCheckSession), mcp: true, signalsOnly: true},
	"context":   {run: plain(runContextSession), mcp: true},
	"unread":    {run: plain(runUnreadSession), mcp: true},
	"channels":  {run: plain(runChannelsSession), mcp: true},
	"history":   {run: plain(runHistorySession), mcp: true},
	"search":    {run: plain(runSearchSession), mcp: true},
	"directory": {run: named(runDirectoryCommandSession), mcp: true},
	"presence":  {run: named(runDirectoryCommandSession), mcp: true},
	"profile":   {run: named(runDirectoryCommandSession), mcp: true},
	"follow":    {run: named(runFollowSession), mcp: true},
	"unfollow":  {run: named(runFollowSession), mcp: true},
	"following": {run: named(runFollowSession)},
	"correct":   {run: named(runChatCommandSession), mcp: true},
	"retract":   {run: named(runChatCommandSession), mcp: true},
	"prepare":   {run: named(runChatCommandSession), mcp: true},
	"waiting":   {run: named(runChatCommandSession), mcp: true},

	"pin":          {run: named(runChatCommandSession)},
	"unpin":        {run: named(runChatCommandSession)},
	"pins":         {run: named(runChatCommandSession)},
	"room":         {run: named(runChatCommandSession)},
	"me":           {run: named(runChatCommandSession)},
	"typing":       {run: named(runChatCommandSession)},
	"thinking":     {run: named(runChatCommandSession)},
	"poll":         {run: named(runChatCommandSession)},
	"vote":         {run: named(runChatCommandSession)},
	"poll-results": {run: named(runChatCommandSession)},
	"poll-close":   {run: named(runChatCommandSession)},
}

// otherCommands are dispatch's subcommands that manage their own context or
// connection and so are not session commands.
var otherCommands = []string{
	"op", "deop", "operators", "kick", "monitor", "away", "bot", "service", "user", "admin",
	"watch", "agents", "skill", "topic", "ui", "thread", "names", "doctor", "interactive", "mcp", "react", "wake",
}

// commands names dispatch's subcommands, so a command typed as a flag
// (airc --watch) gets a hint instead of only an unknown-flag error. It is
// filled in init because the session functions reach hints, which read it.
var commands []string

func init() {
	commands = slices.Concat(slices.Sorted(maps.Keys(sessionCommands)), otherCommands)
}

// runSessionCommand runs a registered command as a CLI entry point: with its
// own bounded process context and no leased connection.
func runSessionCommand(name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	command := sessionCommands[name]
	var ctx context.Context
	var cancel context.CancelFunc
	if command.signalsOnly {
		ctx, cancel = signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	} else {
		ctx, cancel = commandContext()
	}
	defer cancel()
	return command.run(ctx, nil, name, args, stdin, stdout, stderr)
}
