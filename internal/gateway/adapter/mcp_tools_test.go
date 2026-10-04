package adapter

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDecodeMCPRequestUsesStrictGatewayJSON(t *testing.T) {
	valid := `{"version":1,"request_id":"mcp-1","operation":"send","target":{"kind":"room","name":"#room"},"message":"hello"}`
	if _, err := decodeMCPRequest(core.Send, []byte(valid)); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"duplicate request id": `{"version":1,"request_id":"one","request_id":"two","operation":"send","target":{"kind":"room","name":"#room"},"message":"hello"}`,
		"case alias":           `{"version":1,"request_id":"mcp-1","operation":"send","Target":{"kind":"room","name":"#room"},"message":"hello"}`,
		"null target":          `{"version":1,"request_id":"mcp-1","operation":"send","target":null,"message":"hello"}`,
		"operation mismatch":   valid,
		"oversized":            strings.Repeat("x", core.MaxEnvelopeBytes+1),
	} {
		op := core.Send
		if name == "operation mismatch" {
			op = core.Reply
		}
		if _, err := decodeMCPRequest(op, []byte(body)); err == nil {
			t.Fatalf("accepted %s", name)
		}
	}
}

func TestMCPToolResultAndFailureAreBounded(t *testing.T) {
	if _, err := mcpToolResult(core.Send, map[string]string{"ok": "yes"}); err != nil {
		t.Fatal(err)
	}
	if result, err := mcpToolError(core.InvalidRequest); err != nil || !result.IsError {
		t.Fatalf("failure result=%#v err=%v", result, err)
	}
	result, err := mcpToolResult(core.Send, map[string]string{"escaped": strings.Repeat("\"", maxMCPToolBytes)})
	if err != nil || !result.IsError {
		t.Fatalf("oversized structured result accepted: %+v %v", result, err)
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > maxMCPToolBytes {
		t.Fatal("capacity error itself exceeded result budget", len(encoded), err)
	}
}

func TestMCPMutationDispatchClassification(t *testing.T) {
	for _, op := range []core.Operation{core.Send, core.Reply, core.ReactionAdd, core.ProfileUpdate, core.PresenceSet, core.TopicSet, core.RoomUpdate, core.PinsAdd, core.PinsRemove, core.ActivitySet, core.ReplySignalSet, core.ReplySignalCancel, core.PollCreate, core.PollVote, core.PollClose, core.MessageAction, core.MessageCorrect, core.MessageRetract} {
		if !mcpMutation(op) {
			t.Fatalf("mutation operation classified as read: %s", op)
		}
	}
	for _, op := range []core.Operation{core.ContextRead, core.PinsRead, core.RoomRead, core.WaitingRead, core.PollResults, core.TopicRead, core.MembersRead, core.ReceiptGet, core.HistoryRead, core.Search, core.Watch, core.Directory, core.ChannelsRead} {
		if mcpMutation(op) {
			t.Fatalf("read operation classified as mutation: %s", op)
		}
	}
}

func TestMCPWatchRequiresProgressAndBounds(t *testing.T) {
	result, err := mcpWatchTool(t.Context(), &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{}}, nil, "", nil)
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("missing progress token result=%#v err=%v", result, err)
	}
	if maxMCPStreamFrames != MaxStreamFrames || maxMCPStreamBytes != MaxStreamBytes || maxMCPToolBytes >= MaxResponseFrameBytes {
		t.Fatal("MCP stream bounds are not tied to framed adapter budgets")
	}
	for _, token := range []any{"", strings.Repeat("x", 65), float64(-1), 1.5} {
		if validMCPProgressToken(token) {
			t.Fatalf("accepted invalid progress token %#v", token)
		}
	}
	for _, token := range []any{"p", float64(0), float64(12)} {
		if !validMCPProgressToken(token) {
			t.Fatalf("rejected valid progress token %#v", token)
		}
	}
}
