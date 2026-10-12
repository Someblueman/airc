// Package mcpserver is the small Model Context Protocol server that airc and
// airc-gateway need: tools only, over stdio or Streamable HTTP. It replaces the
// MCP Go SDK's server in the shipped binaries; the SDK's client remains the
// interoperability check in tests.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// Protocol versions this server negotiates, newest first. A client asking for
// another version is offered the newest.
var supportedVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

// JSON-RPC error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
)

// Implementation names a server in its initialize result.
type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Content is one text content block of a tool result.
type Content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Text returns a single text content block.
func Text(text string) []Content { return []Content{{Type: "text", Text: text}} }

// Result is a tools/call result.
type Result struct {
	Content           []Content `json:"content"`
	StructuredContent any       `json:"structuredContent,omitempty"`
	IsError           bool      `json:"isError,omitempty"`
}

// Annotations are a tool's behaviour hints.
type Annotations struct {
	ReadOnlyHint bool `json:"readOnlyHint,omitempty"`
}

// Tool is one tool and its handler. InputSchema and OutputSchema are JSON
// Schema objects; the handler validates its own arguments unless the tool was
// added with AddTool.
type Tool struct {
	Name         string       `json:"name"`
	Description  string       `json:"description,omitempty"`
	Annotations  *Annotations `json:"annotations,omitempty"`
	InputSchema  any          `json:"inputSchema"`
	OutputSchema any          `json:"outputSchema,omitempty"`

	Handler func(context.Context, *Call) *Result `json:"-"`
}

// ProgressParams are the parameters of a notifications/progress message.
type ProgressParams struct {
	Meta          map[string]any `json:"_meta,omitempty"`
	Message       string         `json:"message,omitempty"`
	ProgressToken any            `json:"progressToken"`
	Progress      float64        `json:"progress"`
	Total         float64        `json:"total,omitempty"`
}

// Call is one tools/call request.
type Call struct {
	Name      string
	Arguments json.RawMessage
	Meta      map[string]any
	notify    func(context.Context, []byte) error
}

// ProgressToken is the request's _meta.progressToken, or nil. JSON numbers are
// float64.
func (c *Call) ProgressToken() any { return c.Meta["progressToken"] }

// NotifyProgress sends a progress notification on this request's stream.
func (c *Call) NotifyProgress(ctx context.Context, params *ProgressParams) error {
	if c.notify == nil {
		return errors.New("mcp: request has no notification stream")
	}
	data, err := json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		Method  string          `json:"method"`
		Params  *ProgressParams `json:"params"`
	}{"2.0", "notifications/progress", params})
	if err != nil {
		return err
	}
	return c.notify(ctx, data)
}

// Server is a set of tools with its identity.
type Server struct {
	info         Implementation
	instructions string
	mu           sync.Mutex
	tools        map[string]*Tool
}

// NewServer returns a server with no tools.
func NewServer(info Implementation, instructions string) *Server {
	return &Server{info: info, instructions: instructions, tools: map[string]*Tool{}}
}

// AddTool adds or replaces a tool whose handler decodes its own arguments.
func (s *Server) AddTool(t *Tool) {
	if t.Handler == nil || t.InputSchema == nil {
		panic(fmt.Sprintf("mcpserver: tool %q needs a handler and an input schema", t.Name))
	}
	s.mu.Lock()
	s.tools[t.Name] = t
	s.mu.Unlock()
}

func (s *Server) tool(name string) *Tool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tools[name]
}

func (s *Server) listTools() []*Tool {
	s.mu.Lock()
	defer s.mu.Unlock()
	tools := make([]*Tool, 0, len(s.tools))
	for _, t := range s.tools {
		tools = append(tools, t)
	}
	slices.SortFunc(tools, func(a, b *Tool) int { return strings.Compare(a.Name, b.Name) })
	return tools
}

// Message is one decoded JSON-RPC message.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

// ParseMessage decodes one JSON-RPC 2.0 message. A batch is an error.
func ParseMessage(data []byte) (*Message, error) {
	var m Message
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m.JSONRPC != "2.0" {
		return nil, errors.New("invalid message version tag")
	}
	if m.Method == "" && m.Result == nil && m.Error == nil {
		return nil, errors.New("message is neither a request nor a response")
	}
	return &m, nil
}

// IsCall reports whether the message is a request that expects a response.
func (m *Message) IsCall() bool { return m.Method != "" && len(m.ID) > 0 && string(m.ID) != "null" }

type methodFlags uint8

const (
	notification methodFlags = 1 << iota
	paramsOptional
)

// methods are the client-to-server methods the MCP SDK's server accepted.
var methods = map[string]methodFlags{
	"initialize":                       0,
	"ping":                             paramsOptional,
	"tools/list":                       paramsOptional,
	"tools/call":                       0,
	"prompts/list":                     paramsOptional,
	"prompts/get":                      0,
	"resources/list":                   paramsOptional,
	"resources/templates/list":         paramsOptional,
	"resources/read":                   0,
	"resources/subscribe":              0,
	"resources/unsubscribe":            0,
	"completion/complete":              0,
	"logging/setLevel":                 0,
	"notifications/cancelled":          notification | paramsOptional,
	"notifications/initialized":        notification | paramsOptional,
	"notifications/roots/list_changed": notification | paramsOptional,
	"notifications/progress":           notification,
}

// Check rejects a request this server cannot route: an unknown method, a
// notification with an ID, a call without one, or missing required params.
// Responses from the client pass.
func (m *Message) Check() (int, error) {
	if m.Method == "" {
		return 0, nil
	}
	flags, ok := methods[m.Method]
	switch {
	case !ok:
		return CodeMethodNotFound, fmt.Errorf("JSON RPC not handled: %q unsupported", m.Method)
	case flags&notification != 0 && m.IsCall():
		return CodeInvalidRequest, fmt.Errorf("JSON RPC invalid request: unexpected id for %q", m.Method)
	case flags&notification == 0 && !m.IsCall():
		return CodeInvalidRequest, fmt.Errorf("JSON RPC invalid request: missing id for %q", m.Method)
	case flags&paramsOptional == 0 && (len(m.Params) == 0 || string(m.Params) == "null"):
		return CodeInvalidRequest, errors.New(`JSON RPC invalid request: missing required "params"`)
	}
	return 0, nil
}

type rpcError struct {
	code    int
	message string
}

func (e *rpcError) Error() string { return e.message }

// Session is one client's protocol state. Its methods are safe for
// concurrent use; each call may run in its own goroutine.
type Session struct {
	server      *Server
	mu          sync.Mutex
	initialized bool
	ready       bool
	calls       map[string]*inflight
}

type inflight struct {
	cancel    context.CancelFunc
	cancelled bool
}

// NewSession starts a session that has not yet been initialized.
func (s *Server) NewSession() *Session {
	return &Session{server: s, calls: map[string]*inflight{}}
}

// Initialized reports whether the client has sent initialize.
func (ss *Session) Initialized() bool {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return ss.initialized
}

// Handle processes one message. It returns the encoded response to a call, or
// nil for a notification, a client response, or a call the client cancelled.
// notify writes a notification related to the call.
func (ss *Session) Handle(ctx context.Context, m *Message, notify func(context.Context, []byte) error) []byte {
	if code, err := m.Check(); err != nil {
		if m.IsCall() {
			return encodeError(m.ID, code, err.Error())
		}
		return nil
	}
	if m.Method == "" {
		return nil // a response to a server request; this server sends none
	}
	if !m.IsCall() {
		ss.notification(m)
		return nil
	}
	key := idKey(m.ID)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	call := &inflight{cancel: cancel}
	ss.mu.Lock()
	if key != "" {
		ss.calls[key] = call
	}
	initialized := ss.initialized
	ss.mu.Unlock()
	defer func() {
		ss.mu.Lock()
		if ss.calls[key] == call {
			delete(ss.calls, key)
		}
		ss.mu.Unlock()
	}()
	var result any
	var err error
	switch {
	case !initialized && m.Method != "initialize" && m.Method != "ping":
		err = &rpcError{CodeInvalidRequest, fmt.Sprintf("method %q is invalid during session initialization", m.Method)}
	default:
		result, err = ss.dispatch(ctx, m, notify)
	}
	ss.mu.Lock()
	cancelled := call.cancelled
	ss.mu.Unlock()
	if cancelled {
		return nil
	}
	if err != nil {
		code := 0
		var rpcErr *rpcError
		if errors.As(err, &rpcErr) {
			code = rpcErr.code
		}
		return encodeError(m.ID, code, err.Error())
	}
	data, err := json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  any             `json:"result"`
	}{"2.0", m.ID, result})
	if err != nil {
		return encodeError(m.ID, 0, "marshaling result: "+err.Error())
	}
	return data
}

func (ss *Session) notification(m *Message) {
	switch m.Method {
	case "notifications/initialized":
		ss.mu.Lock()
		if ss.initialized {
			ss.ready = true
		}
		ss.mu.Unlock()
	case "notifications/cancelled":
		var params struct {
			RequestID json.RawMessage `json:"requestId"`
		}
		if json.Unmarshal(m.Params, &params) != nil {
			return
		}
		key := idKey(params.RequestID)
		ss.mu.Lock()
		if call := ss.calls[key]; call != nil && key != "" {
			call.cancelled = true
			call.cancel()
		}
		ss.mu.Unlock()
	}
}

type emptyResult struct{}

func (ss *Session) dispatch(ctx context.Context, m *Message, notify func(context.Context, []byte) error) (any, error) {
	s := ss.server
	switch m.Method {
	case "initialize":
		var params *struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if err := json.Unmarshal(m.Params, &params); err != nil || params == nil {
			return nil, &rpcError{CodeInvalidParams, `JSON RPC invalid params: "params" must be be provided`}
		}
		ss.mu.Lock()
		ss.initialized = true
		ss.mu.Unlock()
		version := supportedVersions[0]
		if slices.Contains(supportedVersions, params.ProtocolVersion) {
			version = params.ProtocolVersion
		}
		return struct {
			Capabilities    any            `json:"capabilities"`
			Instructions    string         `json:"instructions,omitempty"`
			ProtocolVersion string         `json:"protocolVersion"`
			ServerInfo      Implementation `json:"serverInfo"`
		}{map[string]any{"logging": emptyResult{}, "tools": map[string]bool{"listChanged": true}}, s.instructions, version, s.info}, nil
	case "ping", "logging/setLevel":
		return emptyResult{}, nil
	case "tools/list":
		return struct {
			Tools []*Tool `json:"tools"`
		}{s.listTools()}, nil
	case "prompts/list":
		return map[string][]any{"prompts": {}}, nil
	case "resources/list":
		return map[string][]any{"resources": {}}, nil
	case "resources/templates/list":
		return map[string][]any{"resourceTemplates": {}}, nil
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
			Meta      map[string]any  `json:"_meta"`
		}
		if err := json.Unmarshal(m.Params, &params); err != nil {
			return nil, &rpcError{CodeInvalidParams, "JSON RPC invalid params: " + err.Error()}
		}
		t := s.tool(params.Name)
		if t == nil {
			return nil, &rpcError{CodeInvalidParams, fmt.Sprintf("unknown tool %q", params.Name)}
		}
		var related func(context.Context, []byte) error
		if notify != nil {
			related = notify
		}
		result := t.Handler(ctx, &Call{Name: params.Name, Arguments: params.Arguments, Meta: params.Meta, notify: related})
		if result == nil {
			result = &Result{}
		}
		if result.Content == nil {
			copied := *result
			copied.Content = []Content{}
			result = &copied
		}
		return result, nil
	default:
		return nil, &rpcError{CodeMethodNotFound, fmt.Sprintf("method %q is not supported by this server", m.Method)}
	}
}

func encodeError(id json.RawMessage, code int, message string) []byte {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	data, _ := json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{JSONRPC: "2.0", ID: id, Error: struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}{code, message}})
	return data
}

// idKey canonicalizes a request ID so that a cancellation matches its call
// however the client spelled the number or string.
func idKey(raw json.RawMessage) string {
	var v any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if decoder.Decode(&v) != nil {
		return ""
	}
	switch v := v.(type) {
	case string:
		return "s:" + v
	case json.Number:
		return "n:" + v.String()
	}
	return ""
}
