package main

import (
	"context"
	"errors"

	"github.com/Someblueman/airc/pkg/irc"
)

func (c *checker) combinedRequest(store *cursorStore, rooms []checkTarget) irc.CheckRequest {
	r := irc.CheckRequest{IncludeOwn: c.settings.includeOwn, MaxMessages: min(1000, c.settings.maxMessages+1)}
	for _, t := range c.targets {
		after := store.Cursors[t.key]
		limit := min(1000, c.settings.maxMessages+1)
		if after == "" {
			if isChannel(t.name) {
				limit = c.settings.initial
			} else {
				after = "*"
			}
		}
		r.Targets = append(r.Targets, irc.CheckTarget{Target: t.name, After: after, Limit: limit})
	}
	r.Headers = len(rooms) > 0
	return r
}

func (c *checker) fetchCombined(ctx context.Context, store *cursorStore, rooms []checkTarget) (checkBatch, []checkTopic, error) {
	r := c.combinedRequest(store, rooms)
	entries, err := irc.RequestCheck(ctx, c.client, r, c.noteLive)
	if err != nil {
		return checkBatch{}, nil, err
	}
	var batch checkBatch
	var headers []checkTopic
	reads := map[string]*targetRead{}
	pins := map[string][]irc.ChatEntry{}
	pages := map[string]bool{}
	for _, t := range c.targets {
		reads[t.name] = &targetRead{target: t}
	}
	for _, e := range entries {
		switch e.Kind {
		case "message":
			if e.Message == nil || len(e.Targets) == 0 {
				return checkBatch{}, nil, errors.New("invalid combined check message")
			}
			m := e.Message
			h := &irc.HistoryEvent{Type: "history", ChatMetadata: m.ChatMetadata, ID: m.ID, ReplyTo: m.ReplyTo, ThreadID: m.ThreadID, Reaction: m.Reaction, Seq: m.Seq, From: m.From, Target: m.Target, Message: m.Message, Timestamp: m.Timestamp}
			for _, index := range e.Targets {
				if index < 0 || index >= len(c.targets) {
					return checkBatch{}, nil, errors.New("invalid combined check target index")
				}
				read := reads[c.targets[index].name]
				if read == nil {
					return checkBatch{}, nil, errors.New("invalid combined check target")
				}
				read.page.messages = append(read.page.messages, h)
			}
			batch.hasVisible = batch.hasVisible || c.visible(h)
		case "page":
			read := reads[e.Target]
			if read == nil || pages[e.Target] {
				return checkBatch{}, nil, errors.New("invalid combined check page")
			}
			pages[e.Target] = true
			read.page.status, read.page.cursor = e.Status, e.Cursor
			read.more = e.Status == "more"
			batch.more = batch.more || read.more
			if e.Gap {
				batch.gaps = append(batch.gaps, e.Target)
			}
			if e.Warning != "" {
				batch.warnings = append(batch.warnings, e.Warning)
			}
		case "topic":
			if e.Topic != store.Topics[e.Target] {
				headers = append(headers, checkTopic{Type: "topic", Target: e.Target, Topic: e.Topic})
			}
		case "pin":
			if e.Message != nil {
				pins[e.Target] = append(pins[e.Target], irc.ChatEntry{ID: e.Message.ID, From: e.Message.From, Message: e.Message})
			}
		}
	}
	for _, t := range c.targets {
		if !pages[t.name] {
			return checkBatch{}, nil, errors.New("combined check ended before all pages arrived")
		}
		batch.reads = append(batch.reads, *reads[t.name])
	}
	for _, room := range rooms {
		headers = append(headers, pinHeaders(room.name, pins[room.name], store)...)
	}
	return batch, headers, nil
}
