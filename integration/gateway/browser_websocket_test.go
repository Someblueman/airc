//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

type browserMessageEvidence struct {
	Operation         string      `json:"operation"`
	Result            core.Result `json:"result"`
	ParentID          string      `json:"parent_id"`
	Message           string      `json:"message"`
	OriginalRetracted bool        `json:"original_retracted"`
}

func TestBrowserWebSocketCryptoAndLiveWatch(t *testing.T) {
	if os.Getenv("AGW_BROWSER_PROBE") != "1" {
		t.Skip("requires interactive real browser")
	}
	reports := make(chan string, 1)
	evidence := make(chan []core.ReplySignalAcknowledgement, 1)
	pollEvidence := make(chan []core.PollAcknowledgement, 1)
	messageEvidence := make(chan []browserMessageEvidence, 1)
	var page atomic.Value
	page.Store([]byte{})
	mux := http.NewServeMux()
	const fixtureCookie = "agw-websocket-fixture"
	mux.HandleFunc("/v1/socket", func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(fixtureCookie)
		_ = json.NewEncoder(w).Encode(err == nil && cookie.Value == "not-an-airc-credential")
	})
	mux.HandleFunc("/clear-cookie", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: fixtureCookie, Value: "", Path: "/v1/socket", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		w.WriteHeader(204)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		http.SetCookie(w, &http.Cookie{Name: fixtureCookie, Value: "not-an-airc-credential", Path: "/v1/socket", MaxAge: 120, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		_, _ = w.Write(page.Load().([]byte))
	})
	for _, name := range []string{"envelopes-fetch.js", "events-fetch.js", "envelopes-websocket.js"} {
		mux.HandleFunc("/"+name, func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, filepath.Join("../..", "examples/gateway", name))
		})
	}
	mux.HandleFunc("/result", func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
		if err != nil {
			http.Error(w, "invalid report", 400)
			return
		}
		select {
		case reports <- string(data):
		default:
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("/evidence", func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
		if err != nil {
			http.Error(w, "invalid evidence", 400)
			return
		}
		var values []core.ReplySignalAcknowledgement
		if err := json.Unmarshal(data, &values); err != nil || len(values) != 2 {
			http.Error(w, "invalid evidence", 400)
			return
		}
		select {
		case evidence <- values:
		default:
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("/poll-evidence", func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 3*core.MaxPollAckBytes))
		if err != nil {
			http.Error(w, "invalid poll evidence", 400)
			return
		}
		var values []core.PollAcknowledgement
		if err := json.Unmarshal(data, &values); err != nil || len(values) != 3 {
			http.Error(w, "invalid poll evidence", 400)
			return
		}
		select {
		case pollEvidence <- values:
		default:
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("/message-evidence", func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
		if err != nil {
			http.Error(w, "invalid message evidence", 400)
			return
		}
		var values []browserMessageEvidence
		if err := json.Unmarshal(data, &values); err != nil || len(values) != 3 {
			http.Error(w, "invalid message evidence", 400)
			return
		}
		select {
		case messageEvidence <- values:
		default:
		}
		w.WriteHeader(204)
	})
	browser := httptest.NewServer(mux)
	defer browser.Close()
	f := websocketGatewayFixture(t, false, "127.0.0.1:0", []string{browser.URL})
	_ = f.conn.CloseNow()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	observer, err := irc.DialContext(ctx, irc.Config{Network: "unix", Addr: f.socket, Nick: "fixture-observer", ReadTimeout: 120 * time.Second, WriteTimeout: time.Second})
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = observer.Close() })
	if err = observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "fixture-observer" })
	cfg, err := json.Marshal(map[string]any{"base": f.base, "audience": f.credentials.realm, "signing": f.credentials.browserSigningKey, "recipient": f.credentials.browserRecipientKey})
	if err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile("testdata/browser-websocket.html")
	if err != nil {
		t.Fatal(err)
	}
	page.Store(append(append([]byte(`<script type="application/json" id="config">`), cfg...), append([]byte(`</script>`), html...)...))
	fmt.Println("BROWSER_FIXTURE_URL=" + browser.URL)
	select {
	case report := <-reports:
		if report != "PASS" {
			t.Fatal(report)
		}
	case <-time.After(90 * time.Second):
		t.Fatal("browser WebSocket probe timed out")
	}
	seen := map[string]*irc.MessageEvent{}
	var replySignals []*irc.SignalEvent
	var pollSignals []*irc.SignalEvent
	var pollMessage *irc.MessageEvent
	var messageMutations []*irc.MessageEvent
	deadline := time.After(8 * time.Second)
	for len(seen) < 8 || len(replySignals) < 2 || len(pollSignals) < 2 || len(messageMutations) < 3 {
		select {
		case event, ok := <-observer.Events():
			if !ok {
				t.Fatal("native observer disconnected")
			}
			switch value := event.(type) {
			case *irc.MessageEvent:
				if value.From != "fixture-author" {
					continue
				}
				if seen[value.Message] != nil {
					t.Fatal("browser retry produced duplicate native message", value.Message)
				}
				seen[value.Message] = value
				if value.Kind == "action" || value.Kind == "correct" || value.Kind == "retract" {
					messageMutations = append(messageMutations, value)
				}
				if value.Kind == "poll" {
					pollMessage = value
				}
			case *irc.SignalEvent:
				if value.From == "fixture-author" && (value.Action == "prepare" || value.Action == "cancel") {
					replySignals = append(replySignals, value)
				}
				if value.From == "fixture-author" && value.Action == "results" {
					pollSignals = append(pollSignals, value)
				}
			}
		case <-deadline:
			t.Fatalf("native observer did not receive browser messages/signals: messages=%d message_mutations=%d reply_signals=%d poll_signals=%d", len(seen), len(messageMutations), len(replySignals), len(pollSignals))
		}
	}
	var saved []core.ReplySignalAcknowledgement
	select {
	case saved = <-evidence:
	case <-time.After(2 * time.Second):
		t.Fatal("browser reply signal evidence missing")
	}
	if len(saved) != 2 || len(replySignals) != 2 {
		t.Fatalf("reply signal evidence count mismatch: saved=%d native=%d", len(saved), len(replySignals))
	}
	for i, ack := range saved {
		signal := replySignals[i]
		if ack.Action != signal.Action || ack.NativeTarget != signal.Target || ack.ParentID != signal.ID || ack.From != signal.From || ack.AccountID != signal.AccountID || ack.Note != signal.Text || !ack.ExpiresAt.Equal(signal.ExpiresAt) {
			t.Fatalf("browser reply signal ACK/native mismatch: ack=%+v signal=%+v", ack, signal)
		}
	}
	var savedPolls []core.PollAcknowledgement
	select {
	case savedPolls = <-pollEvidence:
	case <-time.After(2 * time.Second):
		t.Fatal("browser poll evidence missing")
	}
	if pollMessage == nil || len(savedPolls) != 3 || len(pollSignals) != 2 {
		t.Fatalf("poll evidence count mismatch: message=%v saved=%d native=%d", pollMessage != nil, len(savedPolls), len(pollSignals))
	}
	createPoll := core.PollResult{Target: core.Target{Kind: "room", Name: pollMessage.Target}, ID: pollMessage.ID, From: pollMessage.From, AccountID: pollMessage.AccountID, Question: pollMessage.Message, Options: pollMessage.PollOptions, Votes: make([]int, len(pollMessage.PollOptions)), ExpiresAt: pollMessage.PollClosesAt}
	createRequest := core.Request{Version: 1, RequestID: "browser-websocket-poll-create", Operation: core.PollCreate, Target: &core.Target{Kind: "room", Name: pollMessage.Target}, Poll: &core.PollInput{Question: pollMessage.Message, Options: pollMessage.PollOptions, TTLSeconds: 60}}
	if _, err := core.PollAckBytes(&savedPolls[0], createRequest, "fixture-author"); err != nil {
		t.Fatalf("poll create ACK contract mismatch: %v ack=%+v", err, savedPolls[0])
	}
	receipt := savedPolls[0].Receipt
	if !reflect.DeepEqual(savedPolls[0].Poll, createPoll) || receipt == nil || receipt.Receipt == nil || receipt.ID != pollMessage.ID || receipt.Target != pollMessage.Target || receipt.Message != pollMessage.Message || receipt.From != pollMessage.From || receipt.AccountID != pollMessage.AccountID || receipt.Kind != "poll" || !reflect.DeepEqual(receipt.PollOptions, pollMessage.PollOptions) || !receipt.PollClosesAt.Equal(pollMessage.PollClosesAt) || !receipt.Timestamp.Equal(pollMessage.Timestamp) || !receipt.Receipt.Accepted || !receipt.Receipt.Persisted {
		t.Fatalf("poll create ACK/native mismatch: ack=%+v native=%+v", savedPolls[0], createPoll)
	}
	for i, signal := range pollSignals {
		want := core.PollResult{Target: core.Target{Kind: "room", Name: signal.Target}, ID: signal.ID, From: signal.From, AccountID: signal.AccountID, Question: signal.Text, Options: signal.Options, Votes: signal.Votes, ExpiresAt: signal.ExpiresAt, Closed: signal.Closed}
		op := core.PollVote
		choice := 1
		if i == 1 {
			op = core.PollClose
			choice = 0
		}
		mutationRequest := core.Request{Version: 1, RequestID: []string{"browser-websocket-poll-vote", "browser-websocket-poll-close"}[i], Operation: op, Target: &core.Target{Kind: "room", Name: pollMessage.Target}, PollID: pollMessage.ID, Choice: choice}
		if _, err := core.PollAckBytes(&savedPolls[i+1], mutationRequest, "fixture-author"); err != nil {
			t.Fatalf("poll mutation ACK contract mismatch: %v ack=%+v", err, savedPolls[i+1])
		}
		if !reflect.DeepEqual(savedPolls[i+1].Poll, want) || savedPolls[i+1].Receipt != nil || savedPolls[i+1].Poll.ID != pollMessage.ID {
			t.Fatalf("poll mutation ACK/native mismatch: ack=%+v signal=%+v", savedPolls[i+1], signal)
		}
	}
	var savedMessages []browserMessageEvidence
	select {
	case savedMessages = <-messageEvidence:
	case <-time.After(2 * time.Second):
		t.Fatal("browser message mutation evidence missing")
	}
	if len(savedMessages) != 3 || len(messageMutations) != 3 {
		t.Fatalf("message mutation evidence count mismatch: saved=%d native=%d", len(savedMessages), len(messageMutations))
	}
	wantKinds := map[string]string{"message.action": "action", "message.correct": "correct", "message.retract": "retract"}
	for _, saved := range savedMessages {
		kind, ok := wantKinds[saved.Operation]
		if !ok || !saved.OriginalRetracted {
			t.Fatalf("invalid browser message evidence: %+v", saved)
		}
		var native *irc.MessageEvent
		for _, candidate := range messageMutations {
			if candidate.ID == saved.Result.MessageID {
				native = candidate
				break
			}
		}
		if native == nil || native.Kind != kind || native.Target != "#gateway-fixture" || native.Message != saved.Message || native.Supersedes != saved.ParentID || native.ReplyTo != saved.ParentID || native.ThreadID != saved.ParentID || native.Retracted {
			t.Fatalf("message mutation ACK/native mismatch: saved=%+v native=%+v", saved, native)
		}
		if !saved.Result.Durable || saved.Result.State != core.Accepted || saved.Result.Accepted == nil || !*saved.Result.Accepted || saved.Result.Persisted == nil || !*saved.Result.Persisted || saved.Result.SendAttempts != 1 || saved.Result.LookupAttempts != 0 || saved.Result.RecipientConnected != nil {
			t.Fatalf("message mutation result facts mismatch: %+v", saved.Result)
		}
	}
	for _, kind := range []string{"send", "reply", "live"} {
		if seen["browser websocket "+kind+"\nwhole body 🙂"] == nil {
			t.Fatal("observer missed native browser message", kind)
		}
	}
	parent, reply := seen["browser websocket send\nwhole body 🙂"], seen["browser websocket reply\nwhole body 🙂"]
	if reply.ReplyTo != parent.ID || reply.ThreadID != parent.ID {
		t.Fatal("browser WebSocket lost reply/thread facts")
	}
	reaction := seen["agree"]
	if reaction == nil || reaction.Reaction != "agree" || reaction.ReplyTo != parent.ID || reaction.ThreadID != parent.ID || reaction.AccountID != parent.AccountID || reaction.RequestID != "" {
		t.Fatal("browser WebSocket lost native reaction facts", reaction)
	}
	f.stop()
}
