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

func importBPv7(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("import-bpv7", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("file", "", "owner-provisioned JSON configuration")
	input := flags.String("input", "", "absolute BPv7 file path")
	nick := flags.String("author-nick", "", "bound author for plain requests that name none")
	tokenFile := flags.String("author-token-file", "", "owner-only account token file for --author-nick")
	if err := flags.Parse(args); err != nil {
		return err
	}
	bound, err := importBoundAuthor(*nick, *tokenFile)
	if err != nil {
		return err
	}
	if *path == "" || *input == "" || flags.NArg() != 0 {
		return errors.New("import-bpv7 requires --file CONFIG --input BUNDLE without positional arguments")
	}
	c, err := config.Load(*path)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	report, err := service.ImportBPv7As(ctx, c, *input, bound)
	if report.Version != 0 {
		err = errors.Join(err, json.NewEncoder(out).Encode(report))
	}
	return err
}
