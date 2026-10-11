//go:build darwin || linux

package gateway_test

import (
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func channelsEqual(a, b []core.Channel) bool {
	if a == nil || b == nil || len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

func seedChannels(t *testing.T, socket string) *irc.Client {
	history := dial(t, socket, "channels-history", "", false, false)
	if err := history.Join("#channels-history"); err != nil {
		t.Fatal(err)
	}
	next(t, history, func(e *irc.JoinEvent) bool { return e.Agent == "channels-history" })
	for i, body := range []string{"channels history one", "channels history two"} {
		id := "channels-history-" + string(rune('a'+i))
		if err := history.SendWithID("#channels-history", body, id); err != nil {
			t.Fatal(err)
		}
		next(t, history, func(e *irc.MessageEvent) bool { return e.Message == body && e.Target == "#channels-history" })
	}
	if err := history.Part("#channels-history", "done"); err != nil {
		t.Fatal(err)
	}
	next(t, history, func(e *irc.PartEvent) bool { return e.Agent == "channels-history" })
	topic := dial(t, socket, "channels-topic", "", false, false)
	if err := topic.SetTopic("#channels-topic", "whole topic 🙂"); err != nil {
		t.Fatal(err)
	}
	next(t, topic, func(e *irc.TopicEvent) bool { return e.Channel == "#channels-topic" && e.Topic == "whole topic 🙂" })
	for _, value := range []struct{ room, topic string }{{"#channels-Case", "case-sensitive upper"}, {"#channels-case", "case-sensitive lower"}} {
		if err := topic.SetTopic(value.room, value.topic); err != nil {
			t.Fatal(err)
		}
		next(t, topic, func(e *irc.TopicEvent) bool { return e.Channel == value.room && e.Topic == value.topic })
	}
	if err := history.SendWithID("channels-topic", "private discovery control", "channels-dm-control"); err != nil {
		t.Fatal(err)
	}
	next(t, history, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "channels-dm-control" })
	live := dial(t, socket, "channels-live", "", false, false)
	if err := live.Join("#channels-live"); err != nil {
		t.Fatal(err)
	}
	next(t, live, func(e *irc.JoinEvent) bool { return e.Agent == "channels-live" })
	return live
}

func nativeChannels(t *testing.T, socket string) []core.Channel {
	t.Helper()
	probe := dial(t, socket, "channels-probe", "", false, true)
	defer probe.Close()
	if err := probe.Channels(); err != nil {
		t.Fatal(err)
	}
	result := []core.Channel{}
	for {
		select {
		case event, ok := <-probe.Events():
			if !ok {
				t.Fatal("native CHANNELS probe disconnected")
			}
			switch event := event.(type) {
			case *irc.ChannelEvent:
				result = append(result, core.Channel{Name: event.Channel.Name, Members: event.Channel.Members, Messages: event.Channel.Messages, LastActivity: event.Channel.LastActivity, Topic: event.Channel.Topic})
				if len(result) > core.MaxChannels {
					t.Fatal("native CHANNELS exceeded bounded maximum")
				}
			case *irc.EndOfChannelsEvent:
				return result
			}
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		case <-time.After(5 * time.Second):
			t.Fatal("native CHANNELS probe timed out")
		}
	}
}

func headerChannels(t *testing.T, f *reactionTransportFixture, query, token string) (int, []byte) {
	t.Helper()
	path := "/v1/channels" + query
	if strings.HasPrefix(query, "/v1/") {
		path = query
	}
	request, err := http.NewRequest(http.MethodGet, f.httpURL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	setAuthorHeaders(request, token)
	response, err := f.http.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	wantProto := 1
	if f.http.Transport.(*http.Transport).ForceAttemptHTTP2 {
		wantProto = 2
	}
	if response.ProtoMajor != wantProto {
		t.Fatalf("channels HTTP protocol=%s want major %d", response.Proto, wantProto)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, core.MaxChannelsBytes+1))
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, body
}
