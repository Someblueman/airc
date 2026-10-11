//go:build darwin || linux

package gateway_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func seedTopic(t *testing.T, socket, text string) core.TopicResult {
	t.Helper()
	setter := dial(t, socket, "fixture-author", fixtureAccount, false, true)
	defer setter.Close()
	if err := setter.SetTopic("#gateway-fixture", text); err != nil {
		t.Fatal(err)
	}
	next(t, setter, func(e *irc.TopicEvent) bool { return e.Channel == "#gateway-fixture" && e.Topic == text })
	next(t, setter, func(e *irc.RawEvent) bool { return e.Command == "333" })
	native := queryNativeTopic(t, socket, "#gateway-fixture")
	if native.Topic != text || native.SetBy != "fixture-author" || native.SetAt.IsZero() {
		t.Fatalf("native topic mismatch: %+v", native)
	}
	return native
}

func queryNativeTopic(t *testing.T, socket, target string) core.TopicResult {
	t.Helper()
	c := dial(t, socket, fmt.Sprintf("topic-reader-%d", roomReaderSequence.Add(1)), "", false, false)
	defer c.Close()
	if err := c.Topic(target); err != nil {
		t.Fatal(err)
	}
	event := next(t, c, func(e *irc.TopicEvent) bool { return e.Channel == target })
	result := core.TopicResult{Target: core.Target{Kind: "room", Name: target}, Topic: event.Topic, SetBy: event.SetBy}
	if event.Topic == "" {
		return result
	}
	raw := next(t, c, func(e *irc.RawEvent) bool { return e.Command == "333" })
	if len(raw.Params) != 4 || raw.Params[0] != c.Nick() || raw.Params[1] != target {
		t.Fatalf("native topic metadata shape: %+v", raw)
	}
	seconds, err := strconv.ParseInt(raw.Params[3], 10, 64)
	if err != nil {
		t.Fatalf("native topic timestamp %q: %v", raw.Params[3], err)
	}
	result.SetBy = raw.Params[2]
	result.SetAt = time.Unix(seconds, 0).UTC()
	return result
}

func seedTopicHistoryMarker(t *testing.T, socket string) string {
	t.Helper()
	author := dial(t, socket, "fixture-author", fixtureAccount, false, true)
	defer author.Close()
	const id = "topic-history-marker"
	if err := author.SendWithID("#gateway-fixture", "topic marker", id); err != nil {
		t.Fatal(err)
	}
	receipt := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == id })
	if receipt.ID == "" {
		t.Fatal("marker receipt missing native ID")
	}
	return receipt.ID
}

func evictTopicHistory(t *testing.T, socket, marker string) {
	t.Helper()
	author := dial(t, socket, "fixture-author", fixtureAccount, false, true)
	defer author.Close()
	if before := topicHistorySnapshot(t, socket); before[marker] != "topic marker" {
		t.Fatal("native marker missing before eviction", marker, before)
	}
	for i := 0; i < 40; i++ {
		id := "topic-evict-" + strconv.Itoa(i)
		if err := author.SendWithID("#gateway-fixture", "topic eviction "+id, id); err != nil {
			t.Fatal(err)
		}
		next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == id })
	}
	retained := topicHistorySnapshot(t, socket)
	if retained[marker] != "" {
		t.Fatalf("history marker was not evicted: %s", marker)
	}
	latest := false
	for _, body := range retained {
		if body == "topic eviction topic-evict-39" {
			latest = true
		}
	}
	if len(retained) != 32 || !latest {
		t.Fatalf("history eviction control count=%d latest=%v", len(retained), latest)
	}
}

func clearTopic(t *testing.T, socket string) {
	t.Helper()
	c := dial(t, socket, "fixture-author", fixtureAccount, false, true)
	defer c.Close()
	if err := c.SetTopic("#gateway-fixture", ""); err != nil {
		t.Fatal(err)
	}
	next(t, c, func(e *irc.TopicEvent) bool { return e.Channel == "#gateway-fixture" && e.Topic == "" })
	native := queryNativeTopic(t, socket, "#gateway-fixture")
	if native.Topic != "" || native.SetBy != "" || !native.SetAt.IsZero() {
		t.Fatalf("native topic clear mismatch: %+v", native)
	}
}

func topicHistorySnapshot(t *testing.T, socket string) map[string]string {
	t.Helper()
	author := dial(t, socket, "fixture-author", fixtureAccount, false, true)
	defer author.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	entries, err := irc.RequestCheck(ctx, author, irc.CheckRequest{Targets: []irc.CheckTarget{{Target: "#gateway-fixture", Limit: 64}}, IncludeOwn: true, MaxMessages: 128}, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := make(map[string]string)
	for _, entry := range entries {
		if entry.Message != nil {
			snapshot[entry.Message.ID] = entry.Message.Message
		}
	}
	return snapshot
}
