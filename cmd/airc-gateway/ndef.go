package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/service"
)

func importNDEF(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("import-ndef", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("file", "", "owner-provisioned JSON configuration")
	input := flags.String("input", "", "absolute raw NDEF file path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *path == "" || *input == "" || flags.NArg() != 0 {
		return errors.New("import-ndef requires --file CONFIG --input RAW_NDEF without positional arguments")
	}
	c, err := config.Load(*path)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	report, err := service.ImportNDEF(ctx, c, *input)
	if report.Version != 0 {
		err = errors.Join(err, json.NewEncoder(out).Encode(report))
	}
	return err
}
