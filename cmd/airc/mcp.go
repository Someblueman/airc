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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Someblueman/airc/internal/mcpserver"
)

type mcpOutput struct {
	Rows     []any  `json:"rows"`
	Warnings string `json:"warnings,omitempty"`
	Error    any    `json:"error,omitempty"`
}

// Hosts that never load the skill still get the rules that keep agents from
// talking past each other.
const mcpInstructions = `airc is a shared chat for agents and humans on this machine. This adapter acts as one fixed nickname.
Workflow: call check at work checkpoints and before handoffs; it returns new room messages, direct messages and tags since the last check and marks only what it returns as read. The last row is a status: more=true means call check again, gaps mean older messages expired. To wait for an answer, pass wait_seconds (keep it under the host's tool timeout) or reply_to with a message ID; ask posts a question and waits for its first textual reply in one call.
Sending: give exactly one of channel, to or reply_to. Tag someone with @nick. If a send's outcome is uncertain, call send with retry (never repost the text); pending lists uncertain sends.
Catching up: after time away, digest summarizes what is unread (counts per room, busy threads, what is addressed to you and unanswered, open handoffs and decisions, corrections, open polls) in a few hundred bytes; read the parts that matter with context, then check, or digest with mark_read to skip the rest.
Handing over work: handoff passes work to a nickname, or to anyone in a room, where the first to accept takes it; whoever holds it marks it blocked with a reason while it is stuck, accepts it again when it moves, and marks it done, with a link to the result, so everyone knows. handoffs lists what is open for you, what you hold and what you handed over, blocked ones first; with channel, what is open in a room.
Asking a human: decide puts a question, with optional numbered options, to a named decider (from, in a room; or to); only they or an admin answer it, and check with decision and wait_seconds waits for the answer. An unanswered decision is unresolved. decisions lists what waits on you and what you asked.
Progress: status posts where you are (task; working, blocked or done; a link) so cards, agent listings and the digest show it without narration. resolve closes a question that was settled in a DM, by a reaction or in person, so digests stop waiting on it.
Reading: thread and context read a conversation by message ID without moving cursors; search finds retained messages: every word must occur, "quoted phrases" stay together, -word excludes, and newest returns the latest matches first. Messages are at most 4096 bytes and retention is bounded.
Sharing text larger than a message: paste stores a diff, a log or test output (up to 64 KiB) and, with channel or to, posts a message that names it; a message row's paste field names one, and paste_get reads it by id.
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
func (a *mcpAdapter) call(ctx context.Context, args []string, input string, timeout time.Duration, waiting bool) (*mcpserver.Result, mcpOutput, error) {
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
		return &mcpserver.Result{IsError: true, Content: mcpserver.Text(string(data)), StructuredContent: result}, result, nil
	}
	result.Warnings = diagnostics.String()
	return nil, result, nil
}

func (a *mcpAdapter) server() *mcpserver.Server {
	s := mcpserver.NewServer(mcpserver.Implementation{Name: "airc", Version: "1"}, mcpInstructions)
	a.addCoreTools(s)
	a.addChatTools(s)
	a.addPasteTools(s)
	a.addOverviewTools(s)
	return s
}

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
	err := a.server().ServeStdio(ctx, reader, stdout)
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
