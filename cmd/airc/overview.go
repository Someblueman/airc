package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

type checkRows struct {
	messages []checkMessage
	headers  []checkTopic
	status   checkStatus
}

// readCheck runs one bounded check and decodes its JSON rows, so commands
// built on check share its cursors, recovery and filtering.
func readCheck(ctx context.Context, opt options, settings *checkOptions) (checkRows, error) {
	var rows checkRows
	var out bytes.Buffer
	opt.json = true
	if err := checkWithClient(ctx, opt, settings, nil, &out, io.Discard); err != nil {
		return rows, err
	}
	lines := bufio.NewScanner(&out)
	lines.Buffer(nil, out.Len()+1)
	for lines.Scan() {
		var kind struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(lines.Bytes(), &kind); err != nil {
			return rows, err
		}
		var err error
		switch kind.Type {
		case "message":
			var message checkMessage
			err = json.Unmarshal(lines.Bytes(), &message)
			rows.messages = append(rows.messages, message)
		case "topic", "pin":
			var header checkTopic
			err = json.Unmarshal(lines.Bytes(), &header)
			rows.headers = append(rows.headers, header)
		case "status":
			err = json.Unmarshal(lines.Bytes(), &rows.status)
		}
		if err != nil {
			return rows, err
		}
	}
	return rows, lines.Err()
}

// checkFromNow advances every cursor past what the server retains today. A new
// agent uses it to skip traffic that predates it while still seeing each
// room's header and pins.
func checkFromNow(ctx context.Context, opt options, settings *checkOptions, stdout, stderr io.Writer) error {
	page := *settings
	page.fromNow, page.maxMessages, page.maxBytes = false, 1000, 1<<20
	status := checkStatus{Type: "status", Code: "baseline_set"}
	for range maxCheckPages {
		rows, err := readCheck(ctx, opt, &page)
		if err != nil {
			return err
		}
		for _, header := range rows.headers {
			line, err := checkLine(header, opt.json)
			if err != nil {
				return err
			}
			if _, err := stdout.Write(line); err != nil {
				return err
			}
		}
		status.Skipped += len(rows.messages)
		status.More = rows.status.More
		if !status.More {
			break
		}
	}
	if opt.json {
		return json.NewEncoder(stdout).Encode(status)
	}
	if status.More {
		fmt.Fprintln(stderr, "airc: more retained messages remain; run airc check --from-now again")
	}
	_, err := fmt.Fprintf(stderr, "airc: marked %d retained messages as read\n", status.Skipped)
	return err
}

type unreadTarget struct {
	Target   string `json:"target"`
	Unread   int    `json:"unread"`
	Mentions int    `json:"mentions,omitempty"`
	FirstID  string `json:"first_id"`
}

type unreadSummary struct {
	Type     string         `json:"type"`
	Nick     string         `json:"nick"`
	Unread   int            `json:"unread"`
	Mentions int            `json:"mentions"`
	More     bool           `json:"more"`
	Targets  []unreadTarget `json:"targets"`
}

func runUnreadSession(ctx context.Context, session *agentConnection, args []string, stdout, stderr io.Writer) (resultErr error) {
	fs := flag.NewFlagSet("airc unread", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	opt.session = session
	settings := &checkOptions{peek: true, limit: 100, initial: 20, maxMessages: 1000, maxBytes: 1 << 20}
	fs.Var(&settings.channels, "channel", "channel to count; repeat or comma-separate (env AIRC_CHANNEL)")
	fs.BoolVar(&settings.mentions, "mentions", false, "count only direct messages and tags, from any channel")
	defer func() { reportFailure(&resultErr, opt.json, stderr) }()
	if err := parseAgentFlags(fs, args, opt, stderr); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: airc unread [--nick NAME] [--channel ROOM] [--mentions] [--json]")
	}
	if err := identity(opt); err != nil {
		return err
	}
	rows, err := readCheck(ctx, *opt, settings)
	if err != nil {
		return err
	}
	summary := unreadSummary{Type: "unread", Nick: opt.nick, More: rows.status.More, Targets: []unreadTarget{}}
	index := map[string]int{}
	for _, message := range rows.messages {
		name := "inbox"
		if isChannel(message.Target) {
			name = message.Target
		}
		at, known := index[name]
		if !known {
			at = len(summary.Targets)
			index[name] = at
			summary.Targets = append(summary.Targets, unreadTarget{Target: name, FirstID: message.ID})
		}
		summary.Targets[at].Unread++
		summary.Unread++
		if message.Mentioned {
			summary.Targets[at].Mentions++
			summary.Mentions++
		}
	}
	if opt.json {
		return json.NewEncoder(stdout).Encode(summary)
	}
	if summary.Unread == 0 {
		return nil // quiet, so a hook adds nothing when there is nothing to read
	}
	parts := make([]string, len(summary.Targets))
	for i, target := range summary.Targets {
		parts[i] = fmt.Sprintf("%s %d", target.Target, target.Unread)
	}
	atLeast := ""
	if summary.More {
		atLeast = "at least "
	}
	_, err = fmt.Fprintf(stdout, "airc: %s%d unread for %s (%d addressed to you): %s. Read them with airc check.\n", atLeast, summary.Unread, opt.nick, summary.Mentions, strings.Join(parts, ", "))
	return err
}

func runChannelsSession(ctx context.Context, session *agentConnection, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("airc channels", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	opt.session = session
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: airc channels [--json]")
	}
	if err := queryIdentity(opt); err != nil {
		return err
	}
	return chatRequestWithContext(ctx, *opt, "CHANNELS", func(ctx context.Context, client *irc.Client) error {
		list, err := fetchChannels(client, nil)
		if err != nil {
			return err
		}
		sort.Slice(list, func(i, j int) bool { return list[i].LastActivity.After(list[j].LastActivity) })
		encoder := json.NewEncoder(stdout)
		for _, channel := range list {
			if opt.json {
				err = encoder.Encode(struct {
					Type string `json:"type"`
					irc.ChannelInfo
				}{"channel", channel})
			} else {
				active := "never"
				if !channel.LastActivity.IsZero() {
					active = channel.LastActivity.Format(time.RFC3339)
				}
				_, err = fmt.Fprintf(stdout, "%s\t%d connected\t%d retained\tlast %s\t%s\n", channel.Name, channel.Members, channel.Messages, active, channel.Topic)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}
