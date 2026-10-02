package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Someblueman/airc/pkg/bot"
)

func runBot(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("airc bot", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	channels := fs.String("channel", "", "comma-separated channels for the utility bot")
	interval := fs.Duration("interval", time.Second, "minimum interval between commands; excess commands are ignored")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *interval < 100*time.Millisecond {
		return errors.New("use airc bot --nick NAME [--channel '#room'] [--interval 1s]; minimum interval is 100ms")
	}
	if err := identity(opt); err != nil {
		return err
	}
	cfg, err := dialConfig(*opt)
	if err != nil {
		return err
	}
	if cfg.IdentityToken == "" {
		return errors.New("create the bot account first with airc user create --nick NAME")
	}
	var rooms []string
	if *channels != "" {
		for _, channel := range strings.Split(*channels, ",") {
			rooms = append(rooms, channelName(channel))
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Fprintf(stderr, "Starting utility bot %s; address it with '%s: help' or a direct message.\n", cfg.Nick, cfg.Nick)
	return bot.Run(ctx, bot.Config{Client: cfg, Channels: rooms, Commands: bot.UtilityCommands(), Interval: *interval})
}
