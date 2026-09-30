package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/Someblueman/airc/pkg/irc"
)

func runWatch(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("airc watch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	channel := fs.String("channel", "", "channel to watch")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *channel == "" {
		return errors.New("--channel is required")
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
	if !opt.json {
		if _, err := fmt.Fprintf(stdout, "Watching %s (hidden, read-only; Ctrl-C to stop)\n", *channel); err != nil {
			return err
		}
	}
	encoder := json.NewEncoder(stdout)
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return nil
			}
			if !isWatchEvent(event) {
				continue
			}
			if opt.json {
				if err := encoder.Encode(event); err != nil {
					return err
				}
			} else if message, ok := event.(*irc.MessageEvent); ok {
				if _, err := io.WriteString(stdout, formatWatchMessage(message)); err != nil {
					return err
				}
			}
		case <-ctx.Done():
			return nil
		}
	}
}

func waitForObservation(ctx context.Context, client *irc.Client, channel string) error {
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return errors.New("server disconnected before observation started")
			}
			response, ok := event.(*irc.RawEvent)
			if !ok {
				continue
			}
			if response.Command == "765" && len(response.Params) > 1 && response.Params[1] == channel {
				return nil
			}
			switch response.Command {
			case "403", "405", "407", "421", "437", "461", "484":
				return fmt.Errorf("cannot observe %s: %s", channel, response.Trailing)
			}
		case <-client.Done():
			return errors.New("server disconnected before observation started")
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errors.New("timed out waiting for server to confirm observation")
		}
	}
}

func formatWatchMessage(message *irc.MessageEvent) string {
	var output strings.Builder
	fmt.Fprintf(&output, "\n[%s] %s\n", message.Timestamp.Local().Format("2006-01-02 15:04:05"), message.From)
	for _, line := range wrapWatchText(message.Message, 78) {
		fmt.Fprintf(&output, "  %s\n", line)
	}
	return output.String()
}

func wrapWatchText(message string, width int) []string {
	if width < 1 {
		width = 78
	}
	var lines []string
	for _, paragraph := range strings.Split(message, "\n") {
		words := strings.Fields(paragraph)
		if len(words) == 0 {
			lines = append(lines, "")
			continue
		}
		var line strings.Builder
		lineWidth := 0
		for _, word := range words {
			wordWidth := utf8.RuneCountInString(word)
			if wordWidth > width {
				if lineWidth > 0 {
					lines = append(lines, line.String())
					line.Reset()
					lineWidth = 0
				}
				wordRunes := []rune(word)
				for len(wordRunes) > width {
					lines = append(lines, string(wordRunes[:width]))
					wordRunes = wordRunes[width:]
				}
				if len(wordRunes) > 0 {
					line.WriteString(string(wordRunes))
					lineWidth = len(wordRunes)
				}
				continue
			}
			if lineWidth > 0 && lineWidth+1+wordWidth > width {
				lines = append(lines, line.String())
				line.Reset()
				lineWidth = 0
			}
			if lineWidth > 0 {
				line.WriteByte(' ')
				lineWidth++
			}
			line.WriteString(word)
			lineWidth += wordWidth
		}
		lines = append(lines, line.String())
	}
	return lines
}

func isWatchEvent(event irc.Event) bool {
	switch event.(type) {
	case *irc.MessageEvent, *irc.JoinEvent, *irc.PartEvent, *irc.QuitEvent, *irc.ConnectionEvent:
		return true
	default:
		return false
	}
}
