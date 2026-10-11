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
	return importRequests(args, out, "import-bundle", "absolute bundle file path", service.ImportOfflineBundle)
}

func importBPv7(args []string, out io.Writer) error {
	return importRequests(args, out, "import-bpv7", "absolute BPv7 file path", service.ImportBPv7)
}

// importRequests runs an import command: name's flags, then importer on the
// loaded configuration, printing its report.
func importRequests(args []string, out io.Writer, name, inputUsage string, importer func(context.Context, config.Config, string, *config.BoundAuthor) (service.OfflineImportReport, error)) error {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("file", "", "owner-provisioned JSON configuration")
	input := flags.String("input", "", inputUsage)
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
		return errors.New(name + " requires --file CONFIG --input BUNDLE without positional arguments")
	}
	c, err := config.Load(*path)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	report, err := importer(ctx, c, *input, bound)
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
