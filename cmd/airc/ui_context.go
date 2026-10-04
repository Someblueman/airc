package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

type contextIn struct{ snapshot irc.ConversationContext }

func (m *uiModel) selectedMessage() string {
	if b := m.cur(); b != nil {
		for _, item := range b.items {
			if e, ok := item.(*irc.MessageEvent); ok && e.ID == b.selectedID {
				return e.ID
			}
		}
	}
	m.setStatus("Select a message with Shift-Up/Down (or Option/Ctrl) first", true)
	return ""
}

func (m *uiModel) selectMessage(delta int) {
	b := m.cur()
	if b == nil {
		return
	}
	var messages []*irc.MessageEvent
	at := -1
	for _, item := range b.items {
		if e, ok := item.(*irc.MessageEvent); ok && e.ID != "" {
			if e.ID == b.selectedID {
				at = len(messages)
			}
			messages = append(messages, e)
		}
	}
	if len(messages) == 0 {
		return
	}
	if at < 0 {
		at = len(messages) - 1
	} else {
		at = max(0, min(len(messages)-1, at+delta))
	}
	b.selectedID = messages[at].ID
	m.setStatus("Selected "+b.selectedID+": "+cleanText(messages[at].Message), false)
	// Reveal the selected record using the same rendering and width as the view.
	// Its status preview also makes selection visible in very narrow terminals.
	left, right, center := m.layout()
	if left > 0 || right > 0 {
		center--
	}
	lines := m.logLines(b, center)
	b.scroll = max(0, len(lines)-b.eventLines[b.selectedID]-max(m.height-3, 1))
}

func (b *uiBackend) openContext(ctx context.Context, c *irc.Client, id string, translate func(irc.Event)) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Observe first: messages arriving during the snapshot must not fall into a
	// retrieval/subscription gap. The server resolves a reply ID to its root.
	alias := "thread:" + id
	var live []*irc.MessageEvent
	forward := func(event irc.Event) {
		translate(event)
		if message, ok := event.(*irc.MessageEvent); ok {
			if len(live) == bufferLimit {
				copy(live, live[1:])
				live = live[:len(live)-1]
			}
			live = append(live, message)
		}
	}
	if err := b.subscribe(ctx, c, []string{alias}, forward); err != nil {
		return err
	}
	snapshot, err := irc.RequestContextWithEvents(ctx, c, id, 100, forward)
	if err != nil {
		return err
	}
	if snapshot.RootID == "" {
		return errors.New("context has no retained conversation root")
	}
	if len(b.threads) >= 16 && !b.threads[snapshot.RootID] {
		return errors.New("Close a thread view before opening context (limit 16)")
	}
	target := "thread:" + snapshot.RootID
	if b.threads == nil {
		b.threads = map[string]bool{}
	}
	delete(b.observed, alias)
	b.observed[target] = true
	b.threads[snapshot.RootID] = true
	b.emit(ctx, contextIn{snapshot})
	for _, message := range live {
		if message.ID == snapshot.RootID || message.ThreadID == snapshot.RootID {
			b.emit(ctx, msgIn{event: message})
		}
	}

	return nil
}

func (m *uiModel) showContext(snapshot irc.ConversationContext) {
	name := "thread:" + snapshot.RootID
	m.showQuery(queryIn{name: name})
	b := m.find(name)
	if b == nil {
		return
	}
	b.pins = nil
	for _, p := range snapshot.Pins {
		b.pins = append(b.pins, metadataEvent(p))
	}
	for _, message := range snapshot.Messages {
		b.add(metadataEvent(message))
	}
	b.selectedID = snapshot.TriggerID
	b.topic = fmt.Sprintf("Context · omitted %d messages, %d pins, %d profiles · missing: %s", snapshot.OmittedMessages, snapshot.OmittedPins, snapshot.OmittedProfiles, strings.Join(snapshot.Missing, ", "))
	b.version++
	m.status = ""
}

func metadataEvent(e irc.MessageMetadata) *irc.MessageEvent {
	return &irc.MessageEvent{ChatMetadata: e.ChatMetadata, Type: "message", ID: e.ID, From: e.From, Target: e.Target, Message: e.Message, ReplyTo: e.ReplyTo, ThreadID: e.ThreadID, Reaction: e.Reaction, Timestamp: e.Timestamp}
}
