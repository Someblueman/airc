package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

type sendResult struct {
	*irc.MessageEvent
	// Delivered is reported for direct messages: true when the recipient was
	// connected, false when the message was queued for them to read later.
	Delivered *bool `json:"delivered,omitempty"`
}

func runSend(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("airc send", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	channel := fs.String("channel", "", "channel target (env AIRC_CHANNEL)")
	to := fs.String("to", "", "direct message recipient")
	check := fs.Bool("check", false, "read bounded new messages after sending, using the same connection")
	maxMessages := fs.Int("max-messages", 100, "maximum messages returned by --check (1-1000)")
	maxBytes := fs.Int("max-bytes", 32768, "maximum combined send/check output bytes (1024-1048576)")
	message := fs.String("message", "", "message body; may span lines; use - to read it from stdin")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: airc send --message TEXT (--channel ROOM | --to NICK) [--check]")
	}
	if *channel == "" && *to == "" {
		*channel = os.Getenv("AIRC_CHANNEL")
	}
	*channel = channelName(*channel)
	target := *channel
	if (*channel == "") == (*to == "") {
		return errors.New("provide exactly one of --channel or --to")
	}
	if *to != "" {
		target = *to
	}
	if *message == "-" {
		data, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
		if err != nil {
			return fmt.Errorf("read message from stdin: %w", err)
		}
		*message = strings.TrimRight(string(data), "\r\n")
	}
	*message = irc.NormalizeMessage(*message)
	if strings.TrimSpace(*message) == "" {
		return errors.New("--message is required")
	}
	if err := identity(opt); err != nil {
		return err
	}
	ctx, cancel := commandContext()
	defer cancel()
	settings := &checkOptions{limit: 100, initial: 20, maxMessages: *maxMessages, maxBytes: *maxBytes}
	if *channel != "" {
		settings.channels = listFlag{*channel}
	}
	if *check {
		if _, err := settings.targets(opt.nick); err != nil {
			return err
		}
		encoded, _ := json.Marshal(*message)
		minimum := len(encoded) + len(target) + len(opt.nick) + 512 + 1024
		if *maxBytes < minimum {
			return fmt.Errorf("send --check needs --max-bytes at least %d for the receipt and check metadata", minimum)
		}
	}
	client, err := dialOneShot(ctx, *opt)
	if err != nil {
		return err
	}
	defer client.Close()
	stopClose := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer stopClose()
	if *channel != "" && !client.Ephemeral() {
		// Servers without one-shot sessions only accept channel messages from members.
		if err := client.Join(*channel); err != nil {
			return err
		}
	}
	if err := client.Send(target, *message); err != nil {
		return err
	}
	timer := time.NewTimer(requestTimeout)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return errors.New("server disconnected before confirming the message")
			}
			if err := serverError(event); err != nil {
				return fmt.Errorf("send to %s failed: %w", target, err)
			}
			var msg *irc.MessageEvent
			queued := false
			switch value := event.(type) {
			case *irc.MessageEvent:
				msg = value
			case *irc.SendReceiptEvent:
				msg, queued = value.MessageEvent(), value.Queued
			}
			if msg == nil || !strings.EqualFold(msg.From, opt.nick) || !sameTarget(msg.Target, target) || msg.Message != *message {
				continue
			}
			result := sendResult{MessageEvent: msg}
			if *to != "" {
				delivered := !queued
				result.Delivered = &delivered
			}
			var response bytes.Buffer
			if opt.json {
				err = json.NewEncoder(&response).Encode(result)
			} else {
				suffix := ""
				if queued {
					suffix = fmt.Sprintf(" (queued: %s can read it with airc check)", msg.Target)
				}
				_, err = fmt.Fprintf(&response, "%s -> %s: %s%s\n", msg.From, msg.Target, indentContinuation(msg.Message), suffix)
			}
			if err != nil {
				return err
			}
			if *check && response.Len()+1024 > *maxBytes {
				return fmt.Errorf("message sent as %s; combined output needs a larger --max-bytes budget; do not resend", msg.ID)
			}
			receiptBytes := response.Len()
			if _, err := io.Copy(stdout, &response); err != nil {
				return err
			}
			if *check {
				settings.maxBytes -= receiptBytes
				if err := checkWithClient(ctx, *opt, settings, client, stdout, stderr); err != nil {
					return fmt.Errorf("message sent as %s; check failed: %w (do not resend; use airc check)", msg.ID, err)
				}
			}
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errors.New("timed out waiting for server message confirmation")
		case <-client.Done():
			return errors.New("connection closed before message confirmation")
		}
	}
}
