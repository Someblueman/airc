package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// CLI entry points create a bounded process context; MCP passes its call context
// and exclusively leased connection to the same parsers and operations.
func runSend(args []string, stdin io.Reader, stdout, stderr io.Writer) (resultErr error) {
	ctx, cancel := commandContext()
	defer cancel()
	return runSendSession(ctx, nil, args, stdin, stdout, stderr)
}

func runHistory(args []string, stdout, stderr io.Writer) error {
	ctx, cancel := commandContext()
	defer cancel()
	return runHistorySession(ctx, nil, args, stdout, stderr)
}

func runSearch(args []string, stdout, stderr io.Writer) error {
	ctx, cancel := commandContext()
	defer cancel()
	return runSearchSession(ctx, nil, args, stdout, stderr)
}

func runContext(args []string, stdout, stderr io.Writer) error {
	ctx, cancel := commandContext()
	defer cancel()
	return runContextSession(ctx, nil, args, stdout, stderr)
}

func runDirectoryCommand(kind string, args []string, stdout, stderr io.Writer) error {
	ctx, cancel := commandContext()
	defer cancel()
	return runDirectoryCommandSession(ctx, nil, kind, args, stdout, stderr)
}

func runChatCommand(kind string, args []string, stdout, stderr io.Writer) error {
	ctx, cancel := commandContext()
	defer cancel()
	return runChatCommandSession(ctx, nil, kind, args, stdout, stderr)
}

func runFollow(action string, args []string, stdout, stderr io.Writer) error {
	ctx, cancel := commandContext()
	defer cancel()
	return runFollowSession(ctx, nil, action, args, stdout, stderr)
}

func runCheck(args []string, stdout, stderr io.Writer) (resultErr error) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return runCheckSession(ctx, nil, args, stdout, stderr)
}
