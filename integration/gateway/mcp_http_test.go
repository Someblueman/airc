//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPHTTPListsAuthorToolsAndSends(t *testing.T) {
	f := newMCPHTTPFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	tools, err := f.client.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, tool := range tools.Tools {
		seen[tool.Name] = true
	}
	if len(seen) != 6 || !seen["send"] || !seen["read"] || !seen["wait"] || !seen["status"] || !seen["identify"] || !seen["register"] {
		t.Fatalf("MCP tools=%d names=%v", len(seen), seen)
	}
	send := core.Request{Version: 1, RequestID: "mcp-http-send", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "MCP HTTP native send"}
	admitted := mcpResult(t, mcpCallRequest(t, f.client, send))
	if !admitted.Durable {
		t.Fatalf("MCP send was not a durable admission: %#v", admitted)
	}
	job := mcpAwaitAccepted(t, f, send)
	observed := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == job.MessageID })
	result := job
	if result.State != core.Accepted || result.MessageID == "" || observed.Message != send.Message || observed.Target != send.Target.Name || observed.From != "fixture-author" {
		t.Fatalf("MCP send result=%#v", result)
	}
	capRequest := core.Request{Version: 1, RequestID: "mcp-capabilities", Operation: core.CapabilitiesGet}
	capCall := mcpCallRequest(t, f.client, capRequest)
	if capCall.IsError {
		t.Fatalf("capabilities tool error=%#v", capCall.StructuredContent)
	}
	// status without lookup_id is the listener's capabilities.
	var discovery dispatch.Capabilities
	if err := json.Unmarshal(mcpStructured(t, capCall), &discovery); err != nil || discovery.Version != 1 || !slices.Contains(discovery.Operations, core.Send) || !slices.Contains(discovery.Operations, core.HistoryRead) {
		t.Fatalf("listener discovery=%+v error=%v", discovery, err)
	}
}

func TestMCPHTTPReadsCompleteBoundedPages(t *testing.T) {
	f := newMCPHTTPFixture(t)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	ids := []string{}
	var native []*irc.MessageEvent
	for i := 0; i < 3; i++ {
		request := core.Request{Version: 1, RequestID: "mcp-page-" + strconv.Itoa(i), Operation: core.Send, Target: room, Message: "MCP page " + strconv.Itoa(i) + "\nwhole body 🙂"}
		if result := mcpResult(t, mcpCallRequest(t, f.client, request)); !result.Durable {
			t.Fatal("send admission not durable", result)
		}
		result := mcpAwaitAccepted(t, f, request)
		observed := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == result.MessageID })
		if observed.From != "fixture-author" || observed.Target != room.Name || observed.Message != request.Message {
			t.Fatal("native send mismatch", observed)
		}
		ids = append(ids, result.MessageID)
		native = append(native, observed)
	}
	request := core.Request{Version: 1, RequestID: "mcp-read-page", Operation: core.HistoryRead, Target: room, Start: "retained", Limit: 2}
	var page backend.HistoryPage
	if err := json.Unmarshal(mcpStructured(t, mcpCallRequest(t, f.client, request)), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 2 || page.Status != "more" || page.Gap || page.Cursor == "" || page.Messages[0].ID != ids[0] || page.Messages[1].ID != ids[1] {
		t.Fatalf("first bounded page=%+v", page)
	}
	for i, message := range page.Messages {
		if message.Message != native[i].Message || message.From != native[i].From || message.Target != native[i].Target {
			t.Fatal("MCP bounded page changed native metadata", message, native[i])
		}
	}
	request.RequestID, request.Cursor, request.Start = "mcp-read-resume", page.Cursor, ""
	var resumed backend.HistoryPage
	if err := json.Unmarshal(mcpStructured(t, mcpCallRequest(t, f.client, request)), &resumed); err != nil {
		t.Fatal(err)
	}
	if len(resumed.Messages) != 1 || resumed.Status != "ok" || resumed.Gap || resumed.Messages[0].ID != ids[2] || resumed.Cursor == "" {
		t.Fatalf("resumed bounded page=%+v", resumed)
	}
	if message := resumed.Messages[0]; message.Message != native[2].Message || message.From != native[2].From || message.Target != native[2].Target {
		t.Fatal("MCP resumed page changed native metadata", message, native[2])
	}
	request.RequestID, request.Cursor = "mcp-read-boundary", resumed.Cursor
	var boundary backend.HistoryPage
	if err := json.Unmarshal(mcpStructured(t, mcpCallRequest(t, f.client, request)), &boundary); err != nil {
		t.Fatal(err)
	}
	if len(boundary.Messages) != 0 || boundary.Status != "ok" || boundary.Gap || boundary.Cursor == "" {
		t.Fatalf("read past page boundary=%+v", boundary)
	}
	request.RequestID, request.Operation, request.Cursor, request.Start = "mcp-wait-page", core.Watch, "", "retained"
	var waited backend.HistoryPage
	if err := json.Unmarshal(mcpStructured(t, mcpCallRequest(t, f.client, request)), &waited); err != nil {
		t.Fatal(err)
	}
	if len(waited.Messages) != 2 || waited.Messages[0].ID != ids[0] || waited.Messages[1].ID != ids[1] || waited.Cursor == "" {
		t.Fatalf("ordinary wait page=%+v", waited)
	}
	// Read cursors cannot be promoted into watch cursors by renaming the tool.
	request.RequestID, request.Cursor, request.Start = "mcp-wait-wrong-cursor", resumed.Cursor, ""
	if call := mcpCallRequest(t, f.client, request); !call.IsError {
		t.Fatal("read cursor accepted by wait", call.StructuredContent)
	}

}

func strptr(v string) *string { return &v }

func TestMCPHTTPWatchDeliversProgressPagesAndCancellation(t *testing.T) {
	progress := make(chan *mcp.ProgressNotificationClientRequest, 32)
	f := newMCPHTTPFixture(t, &mcp.ClientOptions{ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
		select {
		case progress <- req:
		default:
		}
	}})
	request := core.Request{Version: 1, RequestID: "mcp-watch", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Limit: 10, Start: "recent"}
	params := mcpCallParams(t, request)
	params.SetProgressToken("mcp-watch-progress")
	watchCtx, cancel := context.WithCancel(t.Context())
	done := make(chan struct {
		result *mcp.CallToolResult
		err    error
	}, 1)
	go func() {
		result, err := f.client.CallTool(watchCtx, params)
		done <- struct {
			result *mcp.CallToolResult
			err    error
		}{result, err}
	}()
	select {
	case notification := <-progress:
		if notification.Params.ProgressToken != "mcp-watch-progress" || notification.Params.Message == "" || notification.Params.Progress <= 0 {
			t.Fatalf("progress=%#v", notification)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch emitted no initial progress page")
	}
	author := dial(t, f.config.Upstream.Address, "mcp-watch-peer", "", false, true)
	liveBody := "MCP live watch 🙂"
	if err := author.SendWithID("#gateway-fixture", liveBody, "mcp-watch-live"); err != nil {
		t.Fatal(err)
	}
	next(t, author, func(e *irc.SendReceiptEvent) bool {
		return e.RequestID == "mcp-watch-live" && e.Receipt != nil && e.Receipt.Accepted
	})
	progressSeen, live := false, false
	encodedLive := base64.RawURLEncoding.EncodeToString([]byte(liveBody))
	deadline := time.After(5 * time.Second)
	for !progressSeen || !live {
		select {
		case notification := <-progress:
			if notification.Params.ProgressToken != "mcp-watch-progress" || notification.Params.Message == "" || notification.Params.Progress <= 0 {
				t.Fatalf("live progress token=%#v", notification)
			}
			progressSeen = true
			live = strings.Contains(notification.Params.Message, encodedLive)
		case <-deadline:
			t.Fatal("watch emitted no live progress page")
		}
	}
	cancel()
	select {
	case outcome := <-done:
		if outcome.err == nil && (outcome.result == nil || !outcome.result.IsError) {
			t.Fatalf("watch cancellation returned success: %#v", outcome.result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch cancellation did not join")
	}
}

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
