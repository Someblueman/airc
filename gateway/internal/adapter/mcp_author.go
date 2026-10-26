package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/Someblueman/airc/gateway/internal/jsoninput"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/internal/mcpserver"
)

// mcpAuthorTools are the tools of an MCP session. Each takes an optional
// "author" ({nick, token}) and "password"; "identify" makes its author the
// session's default for later calls.
var mcpAuthorTools = []string{"send", "read", "wait", "check", "status", "identify", "register"}

// newMCPAuthorServer creates one registry for one MCP session.
// in.Session lives exactly as long as the MCP session.
func newMCPAuthorServer(lifetime context.Context, d *dispatch.Dispatcher, in dispatch.Ingress) *mcpserver.Server {
	s := mcpserver.NewServer(mcpserver.Implementation{Name: "airc-gateway", Version: "2"}, "")
	descriptions := map[string]string{
		"send":     "Submit a bounded message with a stable durable request ID, as the author.",
		"read":     "Read a complete bounded room history page as the author and preserve its opaque cursor.",
		"wait":     "Wait for bounded room message pages; a progress token enables bounded streaming.",
		"check":    "Read many rooms, followed threads and the author's inbox after message IDs in one call; wait_seconds waits for news.",
		"status":   "Look up a durable receipt (a registered author's needs its token), or discover this listener's access.",
		"identify": "Check an author with AIRC and make it this session's default author.",
		"register": "Register the author's nickname as an AIRC account with its own token.",
	}
	for _, name := range mcpAuthorTools {
		schema := mcpRequestSchema(name)
		properties := schema["properties"].(map[string]any)
		properties["author"] = map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
			"nick":  map[string]any{"type": "string", "minLength": 1, "maxLength": 30},
			"token": map[string]any{"type": "string", "pattern": `^[0-9a-f]{64}$`},
		}, "required": []string{"nick"}}
		properties["password"] = map[string]any{"type": "string", "minLength": 1, "maxLength": core.MaxPasswordBytes}
		s.AddTool(&mcpserver.Tool{Name: name, Description: descriptions[name], InputSchema: schema, Handler: func(ctx context.Context, call *mcpserver.Call) *mcpserver.Result {
			result, _ := mcpAuthorCall(ctx, lifetime, call, d, in, name)
			return result
		}})
	}
	return s
}

func mcpAuthorCall(ctx, lifetime context.Context, call *mcpserver.Call, d *dispatch.Dispatcher, in dispatch.Ingress, name string) (*mcpserver.Result, error) {
	sub, err := decodeMCPAuthorRequest(name, []byte(call.Arguments))
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
	if sub.Request.Operation.IsWatch() && call.ProgressToken() != nil {
		return mcpAuthorWatch(callCtx, call, d, in, sub)
	}
	result, err := d.SubmitDecoded(callCtx, in, sub, time.Now())
	if err != nil {
		return mcpAuthorError(err)
	}
	return mcpToolResult(sub.Request.Operation, detachedValue(result))
}

func decodeMCPAuthorRequest(name string, body []byte) (core.Submission, error) {
	if len(body) == 0 {
		body = []byte("{}")
	}
	rest, sub, err := core.SplitSecrets(body)
	if err != nil {
		return core.Submission{}, err
	}
	switch name {
	case "identify", "register":
		var args struct {
			RequestID string `json:"request_id"`
		}
		if err := jsoninput.Decode(rest, &args, core.MaxRequestBytes); err != nil {
			return core.Submission{}, err
		}
		sub.Request = core.Request{Version: 1, RequestID: args.RequestID, Operation: core.AccountIdentify}
		if name == "register" {
			sub.Request.Operation = core.AccountRegister
		}
		err = sub.Request.Normalize()
	default:
		sub.Request, err = decodeMCPRequest(name, rest)
	}
	return sub, err
}

func mcpAuthorWatch(ctx context.Context, call *mcpserver.Call, d *dispatch.Dispatcher, in dispatch.Ingress, sub core.Submission) (*mcpserver.Result, error) {
	progressToken := call.ProgressToken()
	if !validMCPProgressToken(progressToken) {
		return mcpToolError(core.InvalidRequest)
	}
	frames, bytes := 0, 0
	var last backend.HistoryPage
	err := d.SubmitDecodedWatch(ctx, in, sub, time.Now(), func(next backend.HistoryPage) error {
		if frames >= maxMCPStreamFrames {
			return store.ErrCapacity
		}
		encoded, marshalErr := json.Marshal(next)
		notification := &mcpserver.ProgressParams{ProgressToken: progressToken, Progress: float64(frames + 1), Message: string(encoded)}
		wrapped, wrapperErr := json.Marshal(notification)
		if marshalErr != nil || wrapperErr != nil || len(wrapped) > maxMCPToolBytes || bytes+len(wrapped) > maxMCPStreamBytes {
			return store.ErrCapacity
		}
		if notifyErr := call.NotifyProgress(ctx, notification); notifyErr != nil {
			return notifyErr
		}
		frames++
		bytes += len(wrapped)
		last = next
		return nil
	})
	if err != nil && !errors.Is(err, backend.ErrStreamRenew) && !(frames > 0 && errors.Is(err, context.DeadlineExceeded)) {
		return mcpAuthorError(err)
	}
	return mcpToolResult(sub.Request.Operation, struct {
		Version   int                 `json:"version"`
		Operation core.Operation      `json:"operation"`
		Page      backend.HistoryPage `json:"page"`
		Reason    string              `json:"reason"`
	}{Version: 1, Operation: sub.Request.Operation, Page: last, Reason: "stream_renew"})
}

// mcpAuthorError keeps AIRC's own refusal, with its numeric, as the tool error.
func mcpAuthorError(err error) (*mcpserver.Result, error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return mcpToolError(core.UpstreamUnavailable)
	}
	_, failure := errorFailure(err)
	return &mcpserver.Result{IsError: true, Content: mcpserver.Text(string(failure.Code)), StructuredContent: failure}, nil
}
