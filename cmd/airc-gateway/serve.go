package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/service"
)

func serve(c config.Config, out io.Writer) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return service.Run(ctx, c, out)
}

func serveStdio(c config.Config, stream adapter.FramedStream) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return service.RunStdio(ctx, c, stream)
}

func serveFileMailbox(c config.Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return service.RunFileMailbox(ctx, c)
}
