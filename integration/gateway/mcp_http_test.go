//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/testwait"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func strptr(v string) *string { return &v }

func TestMCPHTTPWatchRenewsAndResumesSealedCursor(t *testing.T) {
	lease := shortGatewayTimer(t, readLeaseTimer)
	progress := make(chan *mcp.ProgressNotificationClientRequest, 64)
	f := newMCPHTTPFixture(t, &mcp.ClientOptions{ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
		select {
		case progress <- req:
		default:
		}
	}})
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	seed := dial(t, f.config.Upstream.Address, "mcp-renew-seed", "", false, true)
	for i, body := range []string{"MCP renew seed one", "MCP renew seed two"} {
		requestID := "mcp-renew-seed-" + strconv.Itoa(i)
		if err := seed.SendWithID(room.Name, body, requestID); err != nil {
			t.Fatal(err)
		}
		next(t, seed, func(e *irc.SendReceiptEvent) bool {
			return e.RequestID == requestID && e.Receipt != nil && e.Receipt.Accepted
		})
	}
	watch := core.Request{Version: 1, RequestID: "mcp-renew-watch", Operation: core.Watch, Target: room, Limit: 128, Start: "retained"}
	params := mcpCallParams(t, watch)
	params.SetProgressToken("mcp-renew-progress")
	renewCtx, cancel := context.WithTimeout(t.Context(), lease+8*time.Second)
	defer cancel()
	renewed, err := f.client.CallTool(renewCtx, params)
	if err != nil {
		t.Fatal(err)
	}
	if renewed.IsError {
		t.Fatalf("renewal returned tool error: %#v", renewed.StructuredContent)
	}
	var renewal struct {
		Reason string              `json:"reason"`
		Page   backend.HistoryPage `json:"page"`
	}
	if data := mcpStructured(t, renewed); json.Unmarshal(data, &renewal) != nil || renewal.Reason != "stream_renew" || renewal.Page.Cursor == "" {
		t.Fatalf("watch did not return sealed renewal: %#v", renewed.StructuredContent)
	}
	for len(progress) > 0 {
		<-progress
	}
	peer := dial(t, f.config.Upstream.Address, "mcp-renew-peer", "", false, true)
	liveBody := "MCP sealed cursor resume 🙂"
	resume := watch
	resume.RequestID = "mcp-renew-resume"
	resume.Cursor = renewal.Page.Cursor
	resume.Start = ""
	resumeParams := mcpCallParams(t, resume)
	resumeParams.SetProgressToken("mcp-resume-progress")
	resumeCtx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	resumeDone := make(chan error, 1)
	go func() {
		_, callErr := f.client.CallTool(resumeCtx, resumeParams)
		resumeDone <- callErr
	}()
	select {
	case notification := <-progress:
		if notification.Params.ProgressToken != "mcp-resume-progress" {
			t.Fatalf("resumed watch progress token=%#v", notification)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("resumed sealed cursor emitted no initial page")
	}
	if err := peer.SendWithID(room.Name, liveBody, "mcp-renew-live"); err != nil {
		t.Fatal(err)
	}
	next(t, peer, func(e *irc.SendReceiptEvent) bool {
		return e.RequestID == "mcp-renew-live" && e.Receipt != nil && e.Receipt.Accepted
	})
	encoded := base64.RawURLEncoding.EncodeToString([]byte(liveBody))
	seen := false
	deadline := time.After(5 * time.Second)
	for !seen {
		select {
		case notification := <-progress:
			if strings.Contains(notification.Params.Message, encoded) {
				seen = true
			}
		case <-deadline:
			t.Fatal("resumed sealed cursor omitted live message")
		}
	}
	stop()
	select {
	case <-resumeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("resumed watch did not cancel")
	}
}

// TestMCPHTTPCheck reads a room and the registered author's inbox with the
// check tool, waiting for the direct message that arrives during the call.
func TestMCPHTTPCheck(t *testing.T) {
	f := newMCPHTTPFixture(t, nil)
	peer := dial(t, f.config.Upstream.Address, "mcp-check-peer", "", false, true)
	request := core.Request{Version: 1, RequestID: "mcp-check", Operation: core.Check, Limit: 10, WaitSeconds: 10, Rooms: []core.CheckRoom{{Name: "#gateway-fixture"}}, Inbox: &core.CheckInbox{}}
	// Start after everything retained, so only the message sent during the
	// call is news.
	first, err := f.client.CallTool(t.Context(), mcpCallParams(t, core.Request{Version: 1, RequestID: "mcp-check-first", Operation: core.Check, Limit: 1, Rooms: request.Rooms, Inbox: request.Inbox}))
	if err != nil || first.IsError {
		t.Fatal("first check", err, first)
	}
	var page core.CheckResult
	if err := json.Unmarshal(mcpStructured(t, first), &page); err != nil || page.Inbox == nil {
		t.Fatalf("first check: %s %v", mcpStructured(t, first), err)
	}
	request.Rooms[0].After, request.Inbox.After = page.Rooms[0].Cursor, page.Inbox.Cursor
	status := nativeStatusClient(t, f.config.Upstream.Address)
	observers := nativeObserverCount(t, status)
	params := mcpCallParams(t, request)
	type called struct {
		result *mcp.CallToolResult
		err    error
	}
	done := make(chan called, 1)
	start := time.Now()
	go func() {
		result, err := f.client.CallTool(t.Context(), params)
		done <- called{result, err}
	}()
	// The message is sent once the waiting check observes its rooms
	// natively, so it is news during the call.
	testwait.Every(t, 5*time.Second, 10*time.Millisecond, "the waiting check to observe natively", func() bool { return nativeObserverCount(t, status) > observers })
	_ = peer.Send(fixtureAuthor, "MCP inbox news")
	var returned called
	select {
	case returned = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("waiting check did not return")
	}
	result, err := returned.result, returned.err
	if err != nil || result.IsError {
		t.Fatal("waiting check", err, result)
	}
	if err := json.Unmarshal(mcpStructured(t, result), &page); err != nil || page.Inbox == nil || len(page.Inbox.Messages) != 1 || page.Inbox.Messages[0].Message != "MCP inbox news" || time.Since(start) > 3*time.Second {
		t.Fatalf("waiting check: %s %v after %v", mcpStructured(t, result), err, time.Since(start))
	}
}
