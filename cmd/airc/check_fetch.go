package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Someblueman/airc/pkg/irc"
)

type checker struct {
	client   *irc.Client
	nick     string
	settings *checkOptions
	targets  []checkTarget
	sawLive  bool
}

type targetRead struct {
	target checkTarget
	page   historyPage
	more   bool
}

type checkBatch struct {
	reads      []targetRead
	gaps       []string
	warnings   []string
	more       bool
	hasVisible bool
}

func (c *checker) visible(message *irc.HistoryEvent) bool {
	return c.settings.includeOwn || !strings.EqualFold(message.From, c.nick)
}

// Fetch at most one output budget plus a lookahead per target. Cursors are
// committed later, through only the prefix actually emitted or filtered out.
func (c *checker) fetchNew(ctx context.Context, cursors map[string]string) (checkBatch, error) {
	var batch checkBatch
	for _, target := range c.targets {
		after := cursors[target.key]
		limit := min(c.settings.limit, c.settings.maxMessages+1)
		if after == "" {
			if isChannel(target.name) {
				limit = c.settings.initial
			} else if c.client.Supports("HISTORY_START") {
				after = "*"
			} else {
				limit = 1000 // oldest available through an older daemon's latest-only API
			}
		}
		read := targetRead{target: target}
		visible := 0
		for pageNumber := 0; pageNumber < maxCheckPages; pageNumber++ {
			page, err := fetchHistory(ctx, c.client, target.name, after, limit, c.noteLive)
			if err != nil {
				// 430: the daemon no longer retains this conversation.
				var rejected *irc.RejectedError
				if strings.HasPrefix(target.name, "thread:") && errors.As(err, &rejected) && rejected.Code == "430" {
					batch.warnings = append(batch.warnings, "Followed "+target.name+" expired; use airc unfollow to remove it")
					break
				}
				return checkBatch{}, err
			}
			if page.status == "expired" {
				batch.gaps = append(batch.gaps, target.name)
				if c.client.Supports("HISTORY_START") {
					page, err = fetchHistory(ctx, c.client, target.name, "*", limit, c.noteLive)
				} else {
					page, err = fetchHistory(ctx, c.client, target.name, "", 1000, c.noteLive)
				}
				if err != nil {
					return checkBatch{}, err
				}
			}
			if !c.client.Supports("HISTORY_START") && len(page.messages) == 1000 && (after == "" || len(batch.gaps) > 0) {
				batch.warnings = append(batch.warnings, "Older daemon recovery is limited to the latest 1000 messages for "+target.name)
			}
			read.page.status, read.page.cursor = page.status, page.cursor
			for i, message := range page.messages {
				read.page.messages = append(read.page.messages, message)
				if c.visible(message) {
					visible++
					batch.hasVisible = true
				}
				if visible >= c.settings.maxMessages+1 {
					read.more = i+1 < len(page.messages) || page.status == "more"
					break
				}
			}
			if visible >= c.settings.maxMessages+1 {
				break
			}
			if page.status != "more" {
				break
			}
			read.more = true
			if len(page.messages) == 0 {
				return checkBatch{}, errors.New("server returned more history without a message cursor")
			}
			after = page.messages[len(page.messages)-1].ID
			limit = min(c.settings.limit, c.settings.maxMessages+1-visible)
			if pageNumber+1 < maxCheckPages {
				read.more = false
			}
		}
		batch.more = batch.more || read.more
		batch.reads = append(batch.reads, read)
	}
	return batch, nil
}

func (c *checker) noteLive(event irc.Event) {
	message, ok := event.(*irc.MessageEvent)
	if !ok || (!c.settings.includeOwn && strings.EqualFold(message.From, c.nick)) {
		return
	}
	if c.settings.replyTo != "" && (message.ReplyTo != c.settings.replyTo || message.Reaction != "") {
		return
	}
	if !c.settings.mentions || addressedTo(c.nick, message.Target, message.Message) {
		c.sawLive = true
	}
}

func (c *checker) waitForLive(ctx context.Context) error {
	for {
		select {
		case event, ok := <-c.client.Events():
			if !ok {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return fmt.Errorf("server disconnected while waiting for messages: %w", io.EOF)
			}
			c.noteLive(event)
			if c.sawLive {
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		case <-c.client.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("server disconnected while waiting for messages: %w", io.EOF)
		}
	}
}
