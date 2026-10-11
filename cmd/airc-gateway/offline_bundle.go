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

func importOfflineBundle(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("import-bundle", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("file", "", "owner-provisioned JSON configuration")
	input := flags.String("input", "", "absolute bundle file path")
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
		return errors.New("import-bundle requires --file CONFIG --input BUNDLE without positional arguments")
	}
	c, err := config.Load(*path)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	report, err := service.ImportOfflineBundleAs(ctx, c, *input, bound)
	if report.Version != 0 {
		err = errors.Join(err, json.NewEncoder(out).Encode(report))
	}
	return err
}

// importBoundAuthor is the importer-level bound author: plain requests that
// name no author post as this nickname, with the account token read from an
// owner-only file at startup (never from the command line).
func importBoundAuthor(nick, tokenFile string) (*config.BoundAuthor, error) {
	if nick == "" {
		if tokenFile != "" {
			return nil, errors.New("--author-token-file requires --author-nick")
		}
		return nil, nil
	}
	return &config.BoundAuthor{Nick: nick, AccountTokenFile: tokenFile}, nil
}
