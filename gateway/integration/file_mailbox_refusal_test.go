//go:build darwin || linux

package gateway_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func noFileMailboxJob(t *testing.T, f *fileMailboxFixture, id string) {
	t.Helper()
	u := url.URL{Scheme: "file", Path: filepath.Join(f.config.StateDir, "journal.sqlite"), RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM jobs WHERE request_id=?", id).Scan(&count); err != nil || count != 0 {
		t.Fatal("refused file request reached intake", count, err)
	}
}

func TestFileMailboxCLIModeAndSizeRefusal(t *testing.T) {
	f := newFileMailboxFixture(t)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	for _, kind := range []string{"stream"} {
		request := core.Request{Version: 1, RequestID: "file-refuse-" + kind, Operation: core.Send, Target: room, Message: "must never reach AIRC"}
		frame := frameRequest{Version: 1, CorrelationID: request.RequestID, Profile: "plain"}
		expected := core.InvalidRequest
		switch kind {
		case "stream":
			frame.Request = f.credentials.token(t, request, false)
			frame.Stream = true
			expected = core.InvalidRequest
		}
		data, err := json.Marshal(frame)
		if err != nil {
			t.Fatal(err)
		}
		response := fileMailboxDocument(t, f, data)
		if response.CorrelationID != request.RequestID || response.Error == nil || response.Error.Code != expected {
			t.Fatal("wrong actual CLI refusal", kind, response)
		}
		noFileMailboxJob(t, f, request.RequestID)
	}
	request := core.Request{Version: 1, RequestID: "file-too-large", Operation: core.Send, Target: room, Message: "oversized document must never reach intake"}
	data, err := json.Marshal(frameRequest{Version: 1, CorrelationID: request.RequestID, Profile: "plain", Request: f.credentials.token(t, request, false)})
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte(strings.Repeat(" ", adapter.MaxRequestFrameBytes+1-len(data)))...)
	fileMailboxPublish(t, f, data)
	if diagnostic, err := fileMailboxCommand(t, f).CombinedOutput(); err == nil || !strings.Contains(string(diagnostic), "bounded private regular file") {
		t.Fatal("oversized CLI input not refused", err)
	}
	noFileMailboxJob(t, f, request.RequestID)
	if _, err := os.Stat(filepath.Join(f.directory, "response.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("oversized CLI published result", err)
	}
	if err := os.Remove(filepath.Join(f.directory, "request.json")); err != nil {
		t.Fatal(err)
	}
	native := nativeWebTransportMessages(t, f.config.Upstream.Address, room.Name)
	if len(native) != 0 {
		t.Fatal("refused files changed native history", native)
	}
	request.RequestID, request.Message = "file-healthy-after-refusals", "healthy after file refusal 🙂"
	// Rejoin after the refusal batch so the independent observer's ordinary
	// idle read timeout is not consumed by deliberately quiet command runs.
	_ = f.observer.Close()
	f.observer = dial(t, f.config.Upstream.Address, "file-refusal-observer", "", false, false)
	if err := f.observer.Join(room.Name); err != nil {
		t.Fatal(err)
	}
	next(t, f.observer, func(e *irc.JoinEvent) bool { return e.Agent == "file-refusal-observer" })
	data, err = json.Marshal(frameRequest{Version: 1, CorrelationID: request.RequestID, Profile: "plain", Request: f.credentials.token(t, request, true)})
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte(strings.Repeat(" ", adapter.MaxRequestFrameBytes-len(data)))...)
	admitted := fileMailboxDocument(t, f, data)
	if admitted.Error != nil || admitted.Result == nil || admitted.CorrelationID != request.RequestID {
		t.Fatal("exact-limit input not admitted", admitted)
	}
	result := admitted.Result.Result
	if result == nil || !result.Durable {
		t.Fatal("refusal prevented healthy intake", result)
	}
	receipt := fileMailboxCall(t, f, core.Request{Version: 1, RequestID: "file-health-receipt", Operation: core.ReceiptGet, LookupID: request.RequestID}, false).Result.Result
	if receipt == nil || receipt.State != core.Accepted || receipt.SendAttempts != 1 {
		t.Fatal("healthy recovery did not reach acceptance", receipt)
	}
	event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == receipt.MessageID })
	if event.From != "fixture-author" || event.Target != room.Name || event.Message != request.Message {
		t.Fatal("healthy observer mismatch", event)
	}
	native = nativeWebTransportMessages(t, f.config.Upstream.Address, room.Name)
	if len(native) != 1 || native[0].ID != receipt.MessageID {
		t.Fatal("refusal or recovery changed record count", native)
	}
}
