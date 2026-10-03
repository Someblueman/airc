package main

import (
	"context"
	"errors"
	"strings"

	"github.com/Someblueman/airc/pkg/irc"
)

type uiQuery struct {
	Kind, Target, Text, Cursor, Status string
	Gap                                bool
}

func (q uiQuery) description() string {
	text := "End of results · /first restarts · /next checks for newer results"
	if q.Status == "more" {
		text = "More retained results · /next continues · /first restarts"
	}
	if q.Gap {
		text = "Retention gap: some results are no longer available · " + text
	}
	return text + " · last 500 loaded records kept"
}

func (b *uiBackend) pageQuery(ctx context.Context, c *irc.Client, cmd uiCmd, translate func(irc.Event)) error {
	if b.queries == nil {
		b.queries = map[string]uiQuery{}
	}
	if b.threads == nil {
		b.threads = map[string]bool{}
	}
	name := cmd.target
	q, continuing := b.queries[name]
	appendPage := cmd.kind == "query-next"
	if cmd.kind == "thread" || cmd.kind == "search" {
		q = uiQuery{Kind: cmd.kind, Target: cmd.target, Text: cmd.text, Cursor: "*"}
		name = cmd.kind + ":" + cmd.target
		continuing = false
	} else if !continuing {
		return errors.New("query is no longer open; reopen the thread or search")
	}
	if cmd.kind == "query-first" {
		q.Cursor = "*"
		q.Gap = false
	}
	if _, exists := b.queries[name]; !exists && len(b.queries) >= 16 {
		return errors.New("close a query before opening more (limit 16)")
	}
	if q.Kind == "thread" {
		if len(b.threads) >= 16 && !b.threads[q.Target] {
			return errors.New("UI thread limit reached (16)")
		}
		if err := b.subscribe(ctx, c, []string{"thread:" + q.Target}, translate); err != nil {
			return err
		}
	}
	var live []*irc.MessageEvent
	forward := func(e irc.Event) {
		translate(e)
		if m, ok := e.(*irc.MessageEvent); ok {
			if len(live) == bufferLimit {
				live = live[1:]
			}
			live = append(live, m)
		}
	}
	fetch := func(after string) (historyPage, error) {
		if q.Kind == "thread" {
			return fetchHistory(ctx, c, "thread:"+q.Target, after, 100, forward)
		}
		if err := c.Search(q.Target, q.Text, "", after, 50); err != nil {
			return historyPage{}, err
		}
		return awaitHistory(ctx, c, q.Target, forward)
	}
	page, err := fetch(q.Cursor)
	if err != nil {
		return err
	} // The old cursor remains usable after reconnect.
	if page.status == "expired" {
		q.Gap = true
		page, err = fetch("*")
		if err != nil {
			return err
		}
	}
	if q.Kind == "thread" && len(page.messages) > 0 {
		root := page.messages[0].ThreadID
		if root == "" {
			root = page.messages[0].ID
		}
		delete(b.observed, "thread:"+q.Target)
		q.Target = root
		name = "thread:" + root
		b.observed[name] = true
		b.threads[root] = true
	}
	q.Status = page.status
	if page.cursor != "" {
		q.Cursor = page.cursor
	}
	b.queries[name] = q
	b.emit(ctx, queryIn{name: name, thread: q.Kind == "thread", messages: page.messages, page: &q, appendPage: appendPage})
	for _, m := range live {
		b.emit(ctx, msgIn{event: m})
	}
	return nil
}

func queryRoom(b *uiBuffer) string {
	if b != nil && b.query != nil && b.query.Kind == "search" {
		return b.query.Target
	}
	if b != nil {
		return strings.TrimPrefix(b.name, "search:")
	}
	return ""
}
