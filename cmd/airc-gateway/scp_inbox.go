package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/service"
)

func serveSCPInbox(args []string) error {
	flags := flag.NewFlagSet("scp-inbox", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("file", "", "fixed owner-provisioned SCP configuration")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *path == "" || flags.NArg() != 0 {
		return errors.New("scp-inbox requires --file CONFIG without positional arguments")
	}
	originalCommand := os.Getenv("SSH_ORIGINAL_COMMAND")
	if originalCommand != "scp -t inbox" || os.Getenv("SSH_TTY") != "" {
		return errors.New("SCP inbox requires an unallocated-PTY scp -t inbox command")
	}
	c, err := config.Load(*path)
	if err != nil {
		return err
	}
	stream, err := adapter.OpenPipeStream(os.Stdin, os.Stdout)
	if err != nil {
		return err
	}
	defer stream.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return service.RunSCPInbox(ctx, c, stream, originalCommand)
}
