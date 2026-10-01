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
	"slices"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

func runFollow(action string, args []string, stdout, stderr io.Writer) error {
	id := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		id, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("airc "+action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || action != "following" && !protocol.ValidMessageID(id) || action == "following" && id != "" {
		return errors.New("use follow|unfollow MESSAGE_ID or following, with --nick N")
	}
	if err := identity(opt); err != nil {
		return err
	}
	store, err := openCursors(*opt, opt.nick)
	if err != nil {
		return err
	}
	defer store.close()
	if action == "follow" {
		err = chatRequest(*opt, "REPLIES", func(ctx context.Context, c *irc.Client) error {
			page, err := fetchHistory(ctx, c, "thread:"+id, "*", 1, nil)
			if err != nil {
				return err
			}
			if len(page.messages) == 0 {
				return errors.New("thread no longer retained")
			}
			m := page.messages[0]
			id = m.ID
			if m.ThreadID != "" {
				id = m.ThreadID
			}
			return nil
		})
		if err != nil {
			return err
		}
		if !slices.Contains(store.Follows, id) {
			if len(store.Follows) >= 16 {
				return errors.New("follow limit reached (16 threads)")
			}
			store.Follows = append(store.Follows, id)
		}
	} else if action == "unfollow" {
		store.Follows = slices.DeleteFunc(store.Follows, func(candidate string) bool { return candidate == id })
		delete(store.Cursors, "thread:"+id)
	}
	if action != "following" {
		if err := store.save(store.Cursors); err != nil {
			return err
		}
	}
	for _, followed := range store.Follows {
		if opt.json {
			if err := json.NewEncoder(stdout).Encode(map[string]string{"thread_id": followed}); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintln(stdout, followed); err != nil {
			return err
		}
	}
	return nil
}

func waitForAnswer(opt options, id string, duration time.Duration, stdout, stderr io.Writer) error {
	if !protocol.ValidMessageID(id) {
		return errors.New("waiting requires a valid message ID")
	}
	base, stopSignal := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stopSignal()
	ctx, cancel := context.WithTimeout(base, duration)
	defer cancel()
	client, err := dialOneShot(ctx, opt)
	if err != nil {
		return err
	}
	defer client.Close()
	stop := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer stop()
	entries, err := irc.RequestChat(ctx, client, irc.ChatRequest{Action: "waiting", ID: id}, nil)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if opt.json {
			data, _ := checkLine(entry, true)
			if _, err := stdout.Write(data); err != nil {
				return err
			}
		} else {
			if _, err := fmt.Fprintln(stdout, chatEntryText(entry)); err != nil {
				return err
			}
		}
	}
	settings := &checkOptions{replyTo: id, wait: duration, limit: 50, maxMessages: 100, maxBytes: 32768, initial: 20}
	return checkWithClient(ctx, opt, settings, client, stdout, stderr)
}
