package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/Someblueman/airc/pkg/irc"
)

func runWatch(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("airc watch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	channel := fs.String("channel", "", "channel, @nick, or comma-separated list to watch")
	colorMode := fs.String("color", "auto", "colored output: auto, always, or never (auto also honors NO_COLOR)")
	width := fs.Int("width", 0, "wrap text to this many columns (default: terminal width)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *channel == "" {
		return errors.New("--channel is required")
	}
	if *colorMode != "auto" && *colorMode != "always" && *colorMode != "never" {
		return errors.New("--color must be auto, always, or never")
	}
	if opt.nick == "" {
		opt.nick = defaultQueryNick()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	client, err := irc.DialContext(ctx, clientConfig(*opt))
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.Raw("OBSERVE " + *channel); err != nil {
		return err
	}
	if err := waitForObservation(ctx, client, *channel); err != nil {
		return err
	}
	targets := strings.Split(*channel, ",")
	view := newRenderer(useColor(*colorMode, stdout), outputWidth(*width, stdout), len(targets) > 1)
	if !opt.json {
		if _, err := io.WriteString(stdout, view.banner(targets)); err != nil {
			return err
		}
	}
	encoder := json.NewEncoder(stdout)
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				if !opt.json {
					_, _ = io.WriteString(stdout, view.disconnected())
				}
				return errors.New("server closed the connection")
			}
			if !isWatchEvent(event) {
				continue
			}
			if opt.json {
				if err := encoder.Encode(event); err != nil {
					return err
				}
			} else if _, err := io.WriteString(stdout, view.render(event)); err != nil {
				return err
			}
		case <-ctx.Done():
			return nil
		}
	}
}

// useColor decides whether to emit ANSI styling: only for a real terminal
// unless forced, and never when NO_COLOR is set or the terminal is "dumb".
func useColor(mode string, out io.Writer) bool {
	switch mode {
	case "always":
		return true
	case "never":
		return false
	}
	file, ok := out.(*os.File)
	return ok && isTerminal(file) && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
}

// outputWidth picks the wrap width: --width, else the terminal, else $COLUMNS,
// capped so very wide terminals still produce readable lines.
func outputWidth(requested int, out io.Writer) int {
	width := requested
	if width <= 0 {
		if file, ok := out.(*os.File); ok {
			width = terminalWidth(file)
		}
	}
	if width <= 0 {
		width, _ = strconv.Atoi(os.Getenv("COLUMNS"))
	}
	if width <= 0 {
		width = 100
	}
	if requested <= 0 {
		width = min(width-1, 120)
	}
	return width
}

// waitForObservation waits until the server has acknowledged every target in a
// comma-separated list; the server confirms each one separately.
func waitForObservation(ctx context.Context, client *irc.Client, targets string) error {
	return awaitObservation(ctx, client, len(strings.Split(targets, ",")))
}

func isWatchEvent(event irc.Event) bool {
	switch event.(type) {
	case *irc.MessageEvent, *irc.JoinEvent, *irc.PartEvent, *irc.QuitEvent, *irc.ConnectionEvent:
		return true
	default:
		return false
	}
}
