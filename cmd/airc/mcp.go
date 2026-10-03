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
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
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
	Channel   string `json:"channel,omitempty"`
	To        string `json:"to,omitempty"`
	ReplyTo   string `json:"reply_to,omitempty"`
	RequestID string `json:"request_id,omitempty" jsonschema:"Optional stable send key for receipt recovery"`
	Pending   bool   `json:"pending,omitempty" jsonschema:"List saved sends with uncertain outcomes; omit other fields"`
	Retry     string `json:"retry,omitempty" jsonschema:"Recover this saved request receipt without posting; omit message and targets"`
}
type mcpCheckInput struct {
	Channels    []string `json:"channels,omitempty"`
	ReplyTo     string   `json:"reply_to,omitempty"`
	WaitSeconds int      `json:"wait_seconds,omitempty" jsonschema:"Total wait deadline, 0 through 3600 seconds"`
	Peek        bool     `json:"peek,omitempty"`
	Mentions    bool     `json:"mentions,omitempty"`
	IncludeOwn  bool     `json:"include_own,omitempty"`
	MaxMessages int      `json:"max_messages,omitempty"`
	MaxBytes    int      `json:"max_bytes,omitempty"`
}
type mcpThreadInput struct {
	ID    string `json:"id"`
	After string `json:"after,omitempty"`
	Limit int    `json:"limit,omitempty"`
}
type mcpContextInput struct {
	ID       string `json:"id"`
	Limit    int    `json:"limit,omitempty"`
	MaxBytes int    `json:"max_bytes,omitempty"`
}
type mcpDirectoryInput struct {
	Who string `json:"who,omitempty"`
}

type mcpAdapter struct {
	binary   string
	flags    []string
	slots    chan struct{}
	lifetime context.Context
}

// Child commands share all CLI contracts, including durable outbox recovery,
// cursor locks, TLS and saved accounts. No shell or caller-supplied flags run.
func (a *mcpAdapter) call(ctx context.Context, args []string, input string, timeout time.Duration) (*mcp.CallToolResult, mcpOutput, error) {
	select {
	case a.slots <- struct{}{}:
		defer func() { <-a.slots }()
	default:
		return nil, mcpOutput{}, errors.New("adapter busy: four calls already active; retry later")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if a.lifetime != nil {
		stop := context.AfterFunc(a.lifetime, cancel)
		defer stop()
	}
	args = append(args, a.flags...)
	cmd := exec.CommandContext(ctx, a.binary, args...)
	cmd.Stdin = bytes.NewBufferString(input)
	out := boundedCapture{limit: 2 << 20}
	diagnostics := boundedCapture{limit: 128 << 10}
	cmd.Stdout = &out
	cmd.Stderr = &diagnostics
	err := cmd.Run()
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
		detail := diagnostics.String()
		if detail == "" {
			detail = err.Error()
		}
		result.Error = map[string]any{"code": "command_failed", "message": detail}
		if ctx.Err() != nil {
			result.Error = failure(ctx.Err(), "tool")
		}
		for _, line := range strings.Split(strings.TrimSpace(diagnostics.String()), "\n") {
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
	s := mcp.NewServer(&mcp.Implementation{Name: "airc", Version: "1"}, nil)
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
		return a.call(ctx, args, in.Message, 15*time.Second)
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
		return a.call(ctx, args, "", timeout)
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
		return a.call(ctx, args, "", 15*time.Second)
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
		return a.call(ctx, args, "", 15*time.Second)
	})
	mcp.AddTool(s, &mcp.Tool{Name: "directory", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}, Description: "Read self-reported agent profiles and presence."}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpDirectoryInput) (*mcp.CallToolResult, mcpOutput, error) {
		args := []string{"directory"}
		if in.Who != "" {
			args = append(args, "--who", in.Who)
		}
		return a.call(ctx, args, "", 15*time.Second)
	})
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
	binary, err := os.Executable()
	if err != nil {
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
	a := mcpAdapter{binary: binary, flags: flags, slots: make(chan struct{}, 4), lifetime: ctx}
	source, ok := stdin.(io.ReadCloser)
	if !ok {
		source = io.NopCloser(stdin)
	}

	reader := &mcpInputReader{source: source, reader: bufio.NewReader(source), cancel: cancel}
	err = a.server().Run(ctx, &mcp.IOTransport{Reader: reader, Writer: mcpWriter{stdout}})
	if reader.eof.Load() && errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
