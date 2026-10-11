package adapter

import (
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	maxMCPToolBytes    = MaxResponseFrameBytes - 1024
	maxMCPStreamBytes  = MaxStreamBytes
	maxMCPStreamFrames = MaxStreamFrames
	maxMCPCall         = 20 * time.Second
)

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
