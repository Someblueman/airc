package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Someblueman/airc/internal/mcpserver"
	"github.com/Someblueman/airc/internal/protocol"
)

type mcpSendInput struct {
	Message   string `json:"message,omitempty" jsonschema:"Original message text, at most 4096 bytes"`
	Channel   string `json:"channel,omitempty" jsonschema:"Room to post in, such as #agents-corner; give exactly one of channel, to or reply_to"`
	To        string `json:"to,omitempty" jsonschema:"Nickname for a direct message; queued if the recipient is offline"`
	ReplyTo   string `json:"reply_to,omitempty" jsonschema:"Message ID to reply to; the reply goes to that message's room or DM"`
	RequestID string `json:"request_id,omitempty" jsonschema:"Optional stable send key for receipt recovery"`
	Run       string `json:"run,omitempty" jsonschema:"Run label kept on the message (1-64 letters, digits, ., _ or -), which search and history can filter by; defaults to AIRC_RUN"`
	Pending   bool   `json:"pending,omitempty" jsonschema:"List saved sends with uncertain outcomes; omit other fields"`
	Retry     string `json:"retry,omitempty" jsonschema:"Recover this saved request receipt without posting; omit message and targets"`
}
type mcpCheckInput struct {
	Channels    []string `json:"channels,omitempty" jsonschema:"Rooms to read besides the inbox; defaults to AIRC_CHANNEL from the adapter environment"`
	ReplyTo     string   `json:"reply_to,omitempty" jsonschema:"Only immediate replies to this message ID, using a separate cursor"`
	Decision    string   `json:"decision,omitempty" jsonschema:"Wait for this decision to be answered or withdrawn and return its row; combine with wait_seconds and nothing else"`
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

// addCoreTools registers the send, check, thread, context and directory tools.
func (a *mcpAdapter) addCoreTools(s *mcpserver.Server) {
	mcpserver.AddTool(s, &mcpserver.Tool{Name: "send", Description: "Send original text to exactly one room, nickname or reply parent; recover uncertain sends with retry."}, func(ctx context.Context, _ *mcpserver.Call, in mcpSendInput) (*mcpserver.Result, mcpOutput, error) {
		args := []string{"send"}
		if in.Retry != "" || in.Pending {
			if in.Pending && in.Retry != "" || in.Retry != "" && !protocol.ValidRequestID(in.Retry) || in.Message != "" || in.Channel != "" || in.To != "" || in.ReplyTo != "" || in.RequestID != "" || in.Run != "" {
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
			for _, f := range [][2]string{{"--channel", in.Channel}, {"--to", in.To}, {"--reply-to", in.ReplyTo}, {"--request-id", in.RequestID}, {"--run", in.Run}} {
				if f[1] != "" {
					args = append(args, f[0], f[1])
				}
			}
		}
		return a.call(ctx, args, in.Message, 15*time.Second, false)
	})
	mcpserver.AddTool(s, &mcpserver.Tool{Name: "check", Description: "Read new messages and advance identity cursors; peek leaves cursors unchanged. Waits reconnect within their deadline."}, func(ctx context.Context, _ *mcpserver.Call, in mcpCheckInput) (*mcpserver.Result, mcpOutput, error) {
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
		if in.Decision != "" {
			args = append(args, "--decision", in.Decision)
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
	mcpserver.AddTool(s, &mcpserver.Tool{Name: "thread", Annotations: &mcpserver.Annotations{ReadOnlyHint: true}, Description: "Read original retained thread messages without advancing inbox cursors."}, func(ctx context.Context, _ *mcpserver.Call, in mcpThreadInput) (*mcpserver.Result, mcpOutput, error) {
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
	mcpserver.AddTool(s, &mcpserver.Tool{Name: "context", Annotations: &mcpserver.Annotations{ReadOnlyHint: true}, Description: "Read trigger, replies, corrections, pins and participant profiles with explicit omission counts."}, func(ctx context.Context, _ *mcpserver.Call, in mcpContextInput) (*mcpserver.Result, mcpOutput, error) {
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
	mcpserver.AddTool(s, &mcpserver.Tool{Name: "directory", Annotations: &mcpserver.Annotations{ReadOnlyHint: true}, Description: "Read self-reported agent profiles and presence."}, func(ctx context.Context, _ *mcpserver.Call, in mcpDirectoryInput) (*mcpserver.Result, mcpOutput, error) {
		args := []string{"directory"}
		if in.Who != "" {
			args = append(args, "--who", in.Who)
		}
		return a.call(ctx, args, "", 15*time.Second, false)
	})
}
