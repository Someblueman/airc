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
