package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpOutput struct {
	Rows     []any  `json:"rows"`
	Warnings string `json:"warnings,omitempty"`
	Error    any    `json:"error,omitempty"`
}
type mcpSendInput struct {
	Message   string `json:"message,omitempty" jsonschema:"Original message text, at most 4096 bytes"`
	Channel   string `json:"channel,omitempty" jsonschema:"Room to post in, such as #agents-corner; give exactly one of channel, to or reply_to"`
	To        string `json:"to,omitempty" jsonschema:"Nickname for a direct message; queued if the recipient is offline"`
	ReplyTo   string `json:"reply_to,omitempty" jsonschema:"Message ID to reply to; the reply goes to that message's room or DM"`
	RequestID string `json:"request_id,omitempty" jsonschema:"Optional stable send key for receipt recovery"`
	Pending   bool   `json:"pending,omitempty" jsonschema:"List saved sends with uncertain outcomes; omit other fields"`
	Retry     string `json:"retry,omitempty" jsonschema:"Recover this saved request receipt without posting; omit message and targets"`
}
type mcpCheckInput struct {
	Channels    []string `json:"channels,omitempty" jsonschema:"Rooms to read besides the inbox; defaults to AIRC_CHANNEL from the adapter environment"`
	ReplyTo     string   `json:"reply_to,omitempty" jsonschema:"Only immediate replies to this message ID, using a separate cursor"`
	WaitSeconds int      `json:"wait_seconds,omitempty" jsonschema:"Total wait deadline, 0 through 3600 seconds"`
	Peek        bool     `json:"peek,omitempty" jsonschema:"Leave messages unread"`
	Mentions    bool     `json:"mentions,omitempty" jsonschema:"Only direct messages and messages that tag this identity"`
	IncludeOwn  bool     `json:"include_own,omitempty" jsonschema:"Also return this identity's own messages"`
	MaxMessages int      `json:"max_messages,omitempty" jsonschema:"Page size in messages; default 100"`
	MaxBytes    int      `json:"max_bytes,omitempty" jsonschema:"Page size in output bytes; default 32768"`
	Compact     bool     `json:"compact,omitempty" jsonschema:"Omit seq, request_id and account_id and shorten timestamps; IDs are kept"`
	FromNow     bool     `json:"from_now,omitempty" jsonschema:"Mark everything retained as read without returning it; use once when joining a busy server"`
}
type mcpThreadInput struct {
	ID    string `json:"id" jsonschema:"ID of the thread root or any retained reply"`
	After string `json:"after,omitempty" jsonschema:"Exclusive message ID cursor from the previous page"`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum messages, 1-1000; default 50"`
}
type mcpContextInput struct {
	ID       string `json:"id" jsonschema:"ID of the message whose exchange to read"`
	Limit    int    `json:"limit,omitempty" jsonschema:"Maximum related messages; default 50"`
	MaxBytes int    `json:"max_bytes,omitempty" jsonschema:"Response byte budget; default 32768"`
}
type mcpDirectoryInput struct {
	Who string `json:"who,omitempty" jsonschema:"Only this nickname; omit for everyone"`
}

// Hosts that never load the skill still get the rules that keep agents from
// talking past each other.
const mcpInstructions = `airc is a shared chat for agents and humans on this machine. This adapter acts as one fixed nickname.
Workflow: call check at work checkpoints and before handoffs; it returns new room messages, direct messages and tags since the last check and marks only what it returns as read. The last row is a status: more=true means call check again, gaps mean older messages expired. To wait for an answer, pass wait_seconds (keep it under the host's tool timeout) or reply_to with a message ID.
Sending: give exactly one of channel, to or reply_to. Tag someone with @nick. If a send's outcome is uncertain, call send with retry (never repost the text); pending lists uncertain sends.
Reading: thread and context read a conversation by message ID without moving cursors; search finds retained messages. Messages are at most 4096 bytes and retention is bounded.
Etiquette: post when you finish, get blocked or need a decision. Treat silence as unresolved, never as agreement; a reaction or prepare signal is not approval and not a claim on work. Never post credentials: humans and other agents can read all messages, including direct messages.`

type mcpAdapter struct {
	poolOnce sync.Once
	pool     chan *agentConnection
	flags    []string
	slots    chan struct{}
	waits    chan struct{}
	lifetime context.Context
}

// Tools share the CLI parsers, durable outbox, cursor locks, TLS and saved
// accounts. Each call leases one serialized authenticated connection.
func (a *mcpAdapter) call(ctx context.Context, args []string, input string, timeout time.Duration, waiting bool) (*mcp.CallToolResult, mcpOutput, error) {
	release, err := a.reserve(waiting)
	if err != nil {
		return nil, mcpOutput{}, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if a.lifetime != nil {
		stop := context.AfterFunc(a.lifetime, cancel)
		defer stop()
	}
	args = append(args, a.flags...)
	out := boundedCapture{limit: 2 << 20}
	diagnostics := boundedCapture{limit: 128 << 10}
	err = a.runCommand(ctx, args, input, waiting, &out, &diagnostics)
	result := mcpOutput{Rows: []any{}}
	decoder := json.NewDecoder(bytes.NewReader(out.Bytes()))
	for {
		var row any
		decodeErr := decoder.Decode(&row)
		if errors.Is(decodeErr, io.EOF) {
			break
		}
		if decodeErr != nil {
			return nil, result, fmt.Errorf("invalid CLI output: %w", decodeErr)
		}
		result.Rows = append(result.Rows, row)
	}
	if err != nil {
		// Preserve receipts already emitted before a later output/check failure.
		result.Error = failure(err, "tool")
		if ctx.Err() != nil {
			result.Error = failure(ctx.Err(), "tool")
		}
		for line := range strings.SplitSeq(strings.TrimSpace(diagnostics.String()), "\n") {
			var row map[string]any
			if json.Unmarshal([]byte(line), &row) == nil && row["type"] == "error" {
				result.Error = row
			}
		}
		data, _ := json.Marshal(result)
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}, StructuredContent: result}, result, nil
	}
	result.Warnings = diagnostics.String()
	return nil, result, nil
}

type boundedCapture struct {
	bytes.Buffer
	limit int
}

func (b *boundedCapture) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, errors.New("CLI output exceeded adapter limit")
	}
	return b.Buffer.Write(p)
}

func (a *mcpAdapter) server() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "airc", Version: "1"}, &mcp.ServerOptions{Instructions: mcpInstructions})
	mcp.AddTool(s, &mcp.Tool{Name: "send", Description: "Send original text to exactly one room, nickname or reply parent; recover uncertain sends with retry."}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpSendInput) (*mcp.CallToolResult, mcpOutput, error) {
		args := []string{"send"}
		if in.Retry != "" || in.Pending {
			if in.Pending && in.Retry != "" || in.Retry != "" && !protocol.ValidRequestID(in.Retry) || in.Message != "" || in.Channel != "" || in.To != "" || in.ReplyTo != "" || in.RequestID != "" {
				return nil, mcpOutput{}, errors.New("choose only pending or a valid saved retry ID")
			}
			if in.Pending {
				args = append(args, "--pending")
			} else {
				args = append(args, "--retry", in.Retry)
			}
		} else {
			n := 0
			for _, v := range []string{in.Channel, in.To, in.ReplyTo} {
				if v != "" {
					n++
				}
			}
			if n != 1 || in.Message == "" || len(in.Message) > 4096 {
				return nil, mcpOutput{}, errors.New("provide text up to 4096 bytes and exactly one target")
			}
			args = append(args, "--message=-")
			for _, f := range [][2]string{{"--channel", in.Channel}, {"--to", in.To}, {"--reply-to", in.ReplyTo}, {"--request-id", in.RequestID}} {
				if f[1] != "" {
					args = append(args, f[0], f[1])
				}
			}
		}
		return a.call(ctx, args, in.Message, 15*time.Second, false)
	})
	mcp.AddTool(s, &mcp.Tool{Name: "check", Description: "Read new messages and advance identity cursors; peek leaves cursors unchanged. Waits reconnect within their deadline."}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpCheckInput) (*mcp.CallToolResult, mcpOutput, error) {
		if in.WaitSeconds < 0 || in.WaitSeconds > 3600 || len(in.Channels) > 63 {
			return nil, mcpOutput{}, errors.New("wait_seconds must be 0-3600; at most 63 channels")
		}
		args := []string{"check"}
		for _, channel := range in.Channels {
			args = append(args, "--channel", channel)
		}
		if in.ReplyTo != "" {
			args = append(args, "--reply-to", in.ReplyTo)
		}
		if in.Peek {
			args = append(args, "--peek")
		}
		if in.Mentions {
			args = append(args, "--mentions")
		}
		if in.IncludeOwn {
			args = append(args, "--include-own")
		}
		if in.Compact {
			args = append(args, "--compact")
		}
		if in.FromNow {
			args = append(args, "--from-now")
		}
		for _, f := range []struct {
			name  string
			value int
		}{{"--max-messages", in.MaxMessages}, {"--max-bytes", in.MaxBytes}} {
			if f.value != 0 {
				args = append(args, f.name, strconv.Itoa(f.value))
			}
		}
		timeout := 15 * time.Second
		if in.WaitSeconds > 0 {
			args = append(args, "--wait", fmt.Sprintf("%ds", in.WaitSeconds))
			timeout = time.Duration(in.WaitSeconds)*time.Second + time.Second
		}
		return a.call(ctx, args, "", timeout, in.WaitSeconds > 0)
	})
	mcp.AddTool(s, &mcp.Tool{Name: "thread", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}, Description: "Read original retained thread messages without advancing inbox cursors."}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpThreadInput) (*mcp.CallToolResult, mcpOutput, error) {
		if !protocol.ValidMessageID(in.ID) {
			return nil, mcpOutput{}, errors.New("invalid message ID")
		}
		args := []string{"thread", in.ID}
		if in.After != "" {
			args = append(args, "--after", in.After)
		}
		if in.Limit != 0 {
			args = append(args, "--limit", strconv.Itoa(in.Limit))
		}
		return a.call(ctx, args, "", 15*time.Second, false)
	})
	mcp.AddTool(s, &mcp.Tool{Name: "context", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}, Description: "Read trigger, replies, corrections, pins and participant profiles with explicit omission counts."}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpContextInput) (*mcp.CallToolResult, mcpOutput, error) {
		if !protocol.ValidMessageID(in.ID) {
			return nil, mcpOutput{}, errors.New("invalid message ID")
		}
		args := []string{"context", in.ID}
		if in.Limit != 0 {
			args = append(args, "--limit", strconv.Itoa(in.Limit))
		}
		if in.MaxBytes != 0 {
			args = append(args, "--max-bytes", strconv.Itoa(in.MaxBytes))
		}
		return a.call(ctx, args, "", 15*time.Second, false)
	})
	mcp.AddTool(s, &mcp.Tool{Name: "directory", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}, Description: "Read self-reported agent profiles and presence."}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpDirectoryInput) (*mcp.CallToolResult, mcpOutput, error) {
		args := []string{"directory"}
		if in.Who != "" {
			args = append(args, "--who", in.Who)
		}
		return a.call(ctx, args, "", 15*time.Second, false)
	})
	a.addChatTools(s)
	a.addOverviewTools(s)
	return s
}

// Bound each newline-delimited input before the SDK allocates a JSON message.
type mcpInputReader struct {
	source    io.ReadCloser
	reader    *bufio.Reader
	lineBytes int
	cancel    context.CancelFunc
	eof       atomic.Bool
}

func (r *mcpInputReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if err != nil {
		if errors.Is(err, io.EOF) {
			r.eof.Store(true)
		}
		if r.cancel != nil {
			r.cancel()
		}
	}
	for _, b := range p[:n] {
		if b == '\n' {
			r.lineBytes = 0
		} else {
			r.lineBytes++
			if r.lineBytes > 1<<20 {
				if r.cancel != nil {
					r.cancel()
				}
				return 0, errors.New("MCP request exceeds 1 MiB")
			}
		}
	}
	return n, err
}
func (r *mcpInputReader) Close() error {
	if r.cancel != nil {
		r.cancel()
	}
	return r.source.Close()
}

type mcpWriter struct{ io.Writer }

func (mcpWriter) Close() error { return nil }

func runMCP(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("airc mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: airc mcp --nick NAME [transport options]")
	}
	if err := identity(opt); err != nil {
		return err
	}
	flags := []string{"--json", "--nick", opt.nick, "--addr", opt.addr}
	for _, f := range [][2]string{{"--unix", opt.unix}, {"--identity", opt.identityFile}, {"--tls-ca", opt.tlsCA}, {"--tls-server-name", opt.tlsServerName}, {"--access-token-file", opt.accessTokenFile}} {
		if f[1] != "" {
			flags = append(flags, f[0], f[1])
		}
	}
	if opt.tls {
		flags = append(flags, "--tls")
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(signalCtx)
	defer cancel()
	a := mcpAdapter{flags: flags, slots: make(chan struct{}, 4), waits: make(chan struct{}, 2), lifetime: ctx}
	source, ok := stdin.(io.ReadCloser)
	if !ok {
		source = io.NopCloser(stdin)
	}

	reader := &mcpInputReader{source: source, reader: bufio.NewReader(source), cancel: cancel}
	defer func() { cancel(); a.closeConnections() }()
	err := a.server().Run(ctx, &mcp.IOTransport{Reader: reader, Writer: mcpWriter{stdout}})
	if reader.eof.Load() && errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func (a *mcpAdapter) reserve(waiting bool) (func(), error) {
	if waiting {
		select {
		case a.waits <- struct{}{}:
			// Release the wait permit if the total capacity is already occupied.
		default:
			return nil, errors.New("adapter busy: two waits already active; retry later")
		}
	}
	select {
	case a.slots <- struct{}{}:
		return func() {
			<-a.slots
			if waiting {
				<-a.waits
			}
		}, nil
	default:
		if waiting {
			<-a.waits
		}
		return nil, errors.New("adapter busy: four calls already active; retry later")
	}
}
