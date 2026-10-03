package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

type conversationContext struct {
	protocol.ContextSummary
	Messages     []checkMessage  `json:"messages"`
	Pins         []checkMessage  `json:"pins"`
	Participants []irc.AgentCard `json:"participants"`
}

func contextMessage(m *irc.MessageMetadata) checkMessage {
	return checkMessage{ChatMetadata: m.ChatMetadata, Type: "message", ID: m.ID, ReplyTo: m.ReplyTo, ThreadID: m.ThreadID, Reaction: m.Reaction, Seq: m.Seq, From: m.From, Target: m.Target, Message: m.Message, Timestamp: m.Timestamp}
}

func runContext(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || !protocol.ValidMessageID(args[0]) {
		return errors.New("usage: airc context MESSAGE_ID [--limit 50] [--max-bytes 32768] [--json]")
	}
	fs := flag.NewFlagSet("airc context", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	limit := fs.Int("limit", 50, "maximum retained conversation messages (1-1000)")
	budget := fs.Int("max-bytes", 32768, "maximum complete JSON context bytes (1024-1048576)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 || *limit < 1 || *limit > 1000 || *budget < 1024 || *budget > 1<<20 {
		return errors.New("context limit must be 1-1000 and max-bytes 1024-1048576")
	}
	if err := queryIdentity(opt); err != nil {
		return err
	}
	return chatRequest(*opt, "CONTEXT", func(ctx context.Context, c *irc.Client) error {
		snapshot, err := irc.RequestContext(ctx, c, args[0], *limit)
		if err != nil {
			return err
		}
		result := conversationContext{ContextSummary: snapshot.ContextSummary, Messages: []checkMessage{}, Pins: []checkMessage{}, Participants: snapshot.Participants}
		if result.Participants == nil {
			result.Participants = []irc.AgentCard{}
		}
		for _, m := range snapshot.Messages {
			result.Messages = append(result.Messages, contextMessage(&m))
		}
		for _, m := range snapshot.Pins {
			result.Pins = append(result.Pins, contextMessage(&m))
		}
		protected := map[string]bool{result.TriggerID: true, result.RootID: true}
		byID := map[string]checkMessage{}
		for _, m := range result.Messages {
			byID[m.ID] = m
		}
		for _, id := range []string{result.TriggerID, result.RootID} {
			current, ok := byID[id]
			visited := map[string]bool{}
			for ok && current.SupersededBy != "" && !visited[current.ID] {
				visited[current.ID] = true
				next, found := byID[current.SupersededBy]
				if !found {
					break
				}
				current = next
			}
			if ok {
				protected[current.ID] = true
			}
		}
		for {
			data, err := json.Marshal(result)
			if err != nil {
				return err
			}
			if len(data)+1 <= *budget {
				if opt.json {
					_, err = fmt.Fprintln(stdout, string(data))
					return err
				}
				return printContext(stdout, result)
			}
			switch {
			case len(result.Participants) > 0:
				result.Participants = result.Participants[:len(result.Participants)-1]
				result.OmittedProfiles++
			case len(result.Pins) > 0:
				result.Pins = result.Pins[:len(result.Pins)-1]
				result.OmittedPins++
			default:
				// Keep originals and latest corrections even when they need a larger budget.
				index := -1
				for i, m := range result.Messages {
					if !protected[m.ID] {
						index = i
						break
					}
				}
				if index < 0 {
					return errors.New("trigger/root/correction context exceeds max-bytes; increase the budget")
				}
				result.Messages = append(result.Messages[:index], result.Messages[index+1:]...)
				result.OmittedMessages++
			}
		}
	})
}

func printContext(w io.Writer, r conversationContext) error {
	if _, err := fmt.Fprintf(w, "Conversation %s (trigger %s); omitted: %d messages, %d pins, %d profiles; missing: %v\n", r.RootID, r.TriggerID, r.OmittedMessages, r.OmittedPins, r.OmittedProfiles, r.Missing); err != nil {
		return err
	}
	for index, group := range [][]checkMessage{r.Messages, r.Pins} {
		label := ""
		if index == 1 {
			label = "pin "
		}
		for _, m := range group {
			if _, err := fmt.Fprintf(w, "%s%s %s: %s\n", label, m.ID, m.From, indentContinuation(chatBody(&irc.MessageEvent{ChatMetadata: m.ChatMetadata, ID: m.ID, From: m.From, Message: m.Message}))); err != nil {
				return err
			}
		}
	}
	for _, card := range r.Participants {
		if _, err := fmt.Fprintln(w, cardText(card)); err != nil {
			return err
		}
	}
	return nil
}
