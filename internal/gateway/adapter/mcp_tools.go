package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	maxMCPToolBytes    = MaxResponseFrameBytes - 1024
	maxMCPStreamBytes  = MaxStreamBytes
	maxMCPStreamFrames = MaxStreamFrames
	maxMCPCall         = 20 * time.Second
)

// newMCPServer creates one registry for one authenticated MCP session.
func newMCPServer(lifetime context.Context, d *dispatch.Dispatcher, token string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "airc-gateway", Version: "2"}, nil)
	for _, name := range []string{"send", "read", "wait", "status"} {
		addMCPGatewayTool(lifetime, s, d, token, name)
	}
	s.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != "tools/list" {
				return next(ctx, method, req)
			}
			discovery, err := mcpDiscoveryFor(ctx, d, token)
			if err != nil {
				return nil, err
			}
			result, err := next(ctx, method, req)
			if err != nil {
				return nil, err
			}
			listed := result.(*mcp.ListToolsResult)
			filtered := make([]*mcp.Tool, 0, len(listed.Tools))
			for _, tool := range listed.Tools {
				if slices.Contains(discovery.Tools, tool.Name) {
					filtered = append(filtered, tool)
				}
			}
			listed.Tools = filtered
			return listed, nil
		}
	})
	return s
}

func addMCPGatewayTool(lifetime context.Context, s *mcp.Server, d *dispatch.Dispatcher, token, name string) {
	descriptions := map[string]string{
		"send":   "Submit a bounded message with a stable durable request ID.",
		"read":   "Read a complete bounded room history page and preserve its opaque cursor.",
		"wait":   "Wait for bounded room message pages; a progress token enables bounded streaming.",
		"status": "Look up a durable receipt, or discover currently permitted tools and targets.",
	}
	s.AddTool(&mcp.Tool{Name: name, Description: descriptions[name], InputSchema: mcpRequestSchema(name)}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if req == nil || req.Params == nil {
			return mcpToolError(core.InvalidRequest)
		}
		request, err := decodeMCPRequest(name, []byte(req.Params.Arguments))
		if err != nil {
			return mcpToolError(core.InvalidRequest)
		}
		body, err := json.Marshal(request)
		if err != nil {
			return mcpToolError(core.InvalidRequest)
		}
		callCtx, cancel := context.WithTimeout(ctx, maxMCPCall)
		defer cancel()
		joined := make(chan struct{})
		stop := context.AfterFunc(lifetime, func() { cancel(); close(joined) })
		defer func() {
			if !stop() {
				<-joined
			}
		}()
		if request.Operation == core.CapabilitiesGet {
			value, err := mcpDiscoveryFor(callCtx, d, token)
			if err != nil {
				return mcpToolErrorFor(err)
			}
			return mcpToolResult(request.Operation, value)
		}
		if request.Operation.IsWatch() {
			return mcpWatchTool(callCtx, req, d, token, body, request.Operation)
		}
		value, err := bearerCall(callCtx, d, token, request.Operation, body)
		if err != nil {
			return mcpToolErrorFor(err)
		}
		return mcpToolResult(request.Operation, value)
	})
}

func mcpWatchTool(ctx context.Context, req *mcp.CallToolRequest, d *dispatch.Dispatcher, token string, body []byte, operation core.Operation) (*mcp.CallToolResult, error) {
	if req == nil || req.Params == nil || req.Session == nil || d == nil {
		return mcpToolError(core.InvalidRequest)
	}
	progressToken := req.Params.GetProgressToken()
	if progressToken == nil {
		page, err := d.ReadPage(ctx, token, body, "", time.Now(), nil)
		if err != nil {
			return mcpToolErrorFor(err)
		}
		return mcpToolResult(operation, page)
	}
	if !validMCPProgressToken(progressToken) {
		return mcpToolError(core.InvalidRequest)
	}
	frames, bytes := 0, 0
	var last backend.HistoryPage
	page, err := d.ReadPage(ctx, token, body, "", time.Now(), func(next backend.HistoryPage) error {
		if frames >= maxMCPStreamFrames {
			return store.ErrCapacity
		}
		encoded, marshalErr := json.Marshal(next)
		notification := &mcp.ProgressNotificationParams{ProgressToken: progressToken, Progress: float64(frames + 1), Message: string(encoded)}
		wrapped, wrapperErr := json.Marshal(notification)
		if marshalErr != nil || wrapperErr != nil || len(wrapped) > maxMCPToolBytes || bytes+len(wrapped) > maxMCPStreamBytes {
			return store.ErrCapacity
		}
		if notifyErr := req.Session.NotifyProgress(ctx, notification); notifyErr != nil {
			return notifyErr
		}
		frames++
		bytes += len(wrapped)
		last = next
		return nil
	})
	if err != nil {
		if errors.Is(err, backend.ErrStreamRenew) {
			return mcpToolResult(operation, struct {
				Version   int                 `json:"version"`
				Operation core.Operation      `json:"operation"`
				Page      backend.HistoryPage `json:"page"`
				Reason    string              `json:"reason"`
			}{Version: 1, Operation: operation, Page: last, Reason: "stream_renew"})
		}
		return mcpToolErrorFor(err)
	}
	if frames == 0 {
		last = page
	}
	return mcpToolResult(operation, last)
}

func validMCPProgressToken(value any) bool {
	if value == nil {
		return false
	}
	if text, ok := value.(string); ok {
		return len(text) > 0 && len(text) <= 64
	}
	number, ok := value.(float64)
	return ok && !math.IsNaN(number) && !math.IsInf(number, 0) && number >= 0 && number <= math.MaxInt64 && number == float64(int64(number))
}

func mcpToolResult(operation core.Operation, value any) (*mcp.CallToolResult, error) {
	result := &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("%s completed", operation)}},
		StructuredContent: value,
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > maxMCPToolBytes {
		return mcpToolError(core.CapacityExceeded)
	}
	return result, nil
}

func mcpToolError(code core.ErrorCode) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: string(code)}}, StructuredContent: core.Failure{Code: code, Transient: code == core.CapacityExceeded || code == core.UpstreamUnavailable}}, nil
}

func mcpToolErrorFor(err error) (*mcp.CallToolResult, error) {
	if err == nil {
		return mcpToolError(core.UpstreamUnavailable)
	}
	_, code := httpError(err)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		code = core.UpstreamUnavailable
	}
	return mcpToolError(code)
}
