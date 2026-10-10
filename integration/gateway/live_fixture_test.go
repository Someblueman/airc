package gateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

type liveWirePage struct {
	Messages []json.RawMessage `json:"messages"`
	Events   []json.RawMessage `json:"events"`
	Cursor   string            `json:"cursor"`
	Status   string            `json:"status"`
}

type liveWireEvent struct {
	Kind   string          `json:"kind"`
	Target string          `json:"target"`
	Actor  string          `json:"actor"`
	Member string          `json:"member"`
	Reason string          `json:"reason"`
	Topic  *string         `json:"topic"`
	Mode   string          `json:"mode"`
	Signal json.RawMessage `json:"signal"`
}

func decodeLiveEvent(t *testing.T, raw json.RawMessage) liveWireEvent {
	t.Helper()
	var event liveWireEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatal(err, string(raw))
	}
	return event
}

func nextLiveEvent(t *testing.T, page liveWirePage, action string) liveWireEvent {
	t.Helper()
	for _, raw := range page.Events {
		event := decodeLiveEvent(t, raw)
		if action == "" {
			return event
		}
		var signal irc.ChatEntry
		if len(event.Signal) != 0 && json.Unmarshal(event.Signal, &signal) == nil && signal.Action == action {
			return event
		}
	}
	t.Fatalf("live page omitted signal %q: %+v", action, page.Events)
	return liveWireEvent{}
}

func liveRequest(action string) irc.ChatRequest {
	return irc.ChatRequest{Action: action, Target: "#gateway-fixture", Seconds: 10, Text: "live " + action}
}

func triggerLiveSignal(t *testing.T, author *irc.Client, action string) {
	t.Helper()
	triggerLiveChat(t, author, liveRequest(action))
}

func triggerLiveChat(t *testing.T, author *irc.Client, request irc.ChatRequest) {
	t.Helper()
	if err := author.Chat(request); err != nil {
		t.Fatal(err)
	}
	next(t, author, func(event *irc.ChatEvent) bool { return event.Action == request.Action })
	next(t, author, func(event *irc.EndOfChatEvent) bool { return true })
}

func liveQuery() string {
	return url.Values{"kind": {"room"}, "target": {"#gateway-fixture"}, "limit": {"16"}}.Encode()
}

func liveHTTPCall(t *testing.T, client *http.Client, endpoint, token string) (<-chan struct {
	status int
	proto  int
	page   liveWirePage
	err    error
}, *http.Request, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	done := make(chan struct {
		status int
		proto  int
		page   liveWirePage
		err    error
	}, 1)
	go func() {
		response, err := client.Do(req)
		if err != nil {
			done <- struct {
				status int
				proto  int
				page   liveWirePage
				err    error
			}{err: err}
			return
		}
		defer response.Body.Close()
		var page liveWirePage
		decodeErr := json.NewDecoder(response.Body).Decode(&page)
		done <- struct {
			status int
			proto  int
			page   liveWirePage
			err    error
		}{status: response.StatusCode, proto: response.ProtoMajor, page: page, err: decodeErr}
	}()
	return done, req, cancel
}

func liveFrame(t *testing.T, request core.Request, credentials *detachedCredentials, profile string) adapter.FrameRequest {
	t.Helper()
	return adapter.FrameRequest{Version: 1, CorrelationID: fmt.Sprintf("live-%s", request.RequestID), Profile: profile, Envelope: credentials.token(t, request, profile == "encrypted"), Stream: true}
}
