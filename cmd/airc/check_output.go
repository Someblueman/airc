package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"sort"

	"github.com/Someblueman/airc/pkg/irc"
)

func checkLine(value any, machine bool) ([]byte, error) {
	if machine {
		data, err := json.Marshal(value)
		return append(data, '\n'), err
	}
	switch v := value.(type) {
	case checkMessage:
		if v.Reaction != "" {
			return []byte(fmt.Sprintf("%s %s %s reacted %s to %s\n", v.Timestamp.Format("2006-01-02T15:04:05Z07:00"), v.Target, v.From, v.Reaction, v.ReplyTo)), nil
		}
		note := ""
		if v.Mentioned && isChannel(v.Target) {
			note = " (mentions you)"
		}
		if v.ReplyTo != "" {
			note += " (reply to " + v.ReplyTo + ")"
		}
		return []byte(fmt.Sprintf("%s %s %s%s [%s]: %s\n", v.Timestamp.Format("2006-01-02T15:04:05Z07:00"), v.Target, v.From, note, v.ID, indentContinuation(chatBody(&irc.MessageEvent{ChatMetadata: v.ChatMetadata, From: v.From, Message: v.Message})))), nil
	case checkTopic:
		if v.Type == "pin" {
			return []byte(fmt.Sprintf("%s pinned %s by %s (preview): %s\n", v.Target, v.ID, v.From, v.Topic)), nil
		}
		return []byte(fmt.Sprintf("%s topic: %s\n", v.Target, v.Topic)), nil
	case checkStatus:
		if v.More {
			return []byte("airc: more messages or headers remain; run airc check again\n"), nil
		}
		return nil, nil
	}
	return nil, fmt.Errorf("unknown check output %T", value)
}

func (c *checker) output(batch checkBatch, headers []checkTopic, store *cursorStore, machine bool, stdout, stderr io.Writer) error {
	status := checkStatus{Type: "status", Code: "no_messages", More: batch.more, Gaps: batch.gaps, Warnings: batch.warnings}
	if batch.hasVisible || len(headers) > 0 {
		status.Code = "messages"
	}
	// Reserve enough space for the final status even if output hits its budget.
	reserve := status
	reserve.Code = "history_incomplete"
	reserve.More = false // false is one byte longer than true in JSON
	tail, err := checkLine(reserve, machine)
	if err != nil {
		return err
	}
	if !machine {
		tail, _ = checkLine(checkStatus{More: true}, false)
	}
	available := c.settings.maxBytes - len(tail)
	var out bytes.Buffer
	var shownHeaders []checkTopic
	for _, header := range headers {
		if header.Topic == "" {
			shownHeaders = append(shownHeaders, header)
			continue
		}
		line, err := checkLine(header, machine)
		if err != nil {
			return err
		}
		if out.Len()+len(line) > available {
			status.More = true
			continue
		}
		out.Write(line)
		shownHeaders = append(shownHeaders, header)
	}
	seen := map[string]bool{}
	var messages []*irc.HistoryEvent
	for _, read := range batch.reads {
		for _, message := range read.page.messages {
			if !seen[message.ID] && c.visible(message) {
				seen[message.ID] = true
				messages = append(messages, message)
			}
		}
	}
	sort.SliceStable(messages, func(i, j int) bool {
		if !messages[i].Timestamp.Equal(messages[j].Timestamp) {
			return messages[i].Timestamp.Before(messages[j].Timestamp)
		}
		return messages[i].Seq < messages[j].Seq
	})
	emitted := map[string]bool{}
	for _, message := range messages {
		entry := checkMessage{ChatMetadata: message.ChatMetadata, Type: "message", ID: message.ID, ReplyTo: message.ReplyTo, ThreadID: message.ThreadID, Reaction: message.Reaction, Seq: message.Seq, From: message.From, Target: message.Target, Message: message.Message, Timestamp: message.Timestamp, Mentioned: addressedTo(c.nick, message.Target, message.Message)}
		if c.settings.compact {
			entry = entry.compacted()
		}
		line, err := checkLine(entry, machine)
		if err != nil {
			return err
		}
		if len(emitted) >= c.settings.maxMessages || out.Len()+len(line) > available {
			if len(emitted) == 0 && len(line) > available {
				return fmt.Errorf("message %s needs a larger --max-bytes budget (at least %d); it remains unread", message.ID, out.Len()+len(line)+len(tail))
			}
			status.More = true
			break
		}
		out.Write(line)
		emitted[message.ID] = true
	}
	if status.More || len(status.Gaps) > 0 {
		status.Code = "history_incomplete"
	}
	if machine || status.More || len(status.Gaps) > 0 || len(status.Warnings) > 0 {
		line, err := checkLine(status, machine)
		if err != nil {
			return err
		}
		out.Write(line)
	}
	if out.Len() > c.settings.maxBytes {
		return fmt.Errorf("check metadata exceeds --max-bytes %d; increase the budget", c.settings.maxBytes)
	}
	if _, err := io.Copy(stdout, &out); err != nil {
		return err // leave every cursor untouched on output failure
	}
	for _, target := range status.Gaps {
		fmt.Fprintf(stderr, "airc: warning: the cursor for %s expired; messages may have been missed. Recovering the oldest available messages.\n", target)
	}
	for _, warning := range status.Warnings {
		fmt.Fprintln(stderr, "airc: warning:", warning)
	}
	if c.settings.peek {
		return nil
	}
	cursors := make(map[string]string, len(store.Cursors))
	maps.Copy(cursors, store.Cursors)
	for _, read := range batch.reads {
		complete := true
		for _, message := range read.page.messages {
			if c.visible(message) && !emitted[message.ID] {
				complete = false
				break
			}
			cursors[read.target.key] = message.ID
		}
		if complete && !read.more && read.page.status != "more" && read.page.cursor != "" {
			cursors[read.target.key] = read.page.cursor
		}
	}
	for _, header := range shownHeaders {
		if header.Type == "pin" {
			if store.Pins == nil {
				store.Pins = map[string]string{}
			}
			if _, exists := store.Pins[header.PinKey]; !exists && len(store.Pins) >= 128 {
				clear(store.Pins)
			}
			store.Pins[header.PinKey] = header.Fingerprint
			continue
		}
		if header.Topic == "" {
			delete(store.Topics, header.Target)
		} else {
			store.Topics[header.Target] = header.Topic
		}
	}
	if c.settings.replyTo != "" {
		store.rememberReplyTarget("replies:"+c.settings.replyTo, cursors)
	}
	return store.save(cursors)
}
