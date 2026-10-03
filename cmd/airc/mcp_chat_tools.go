package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpSearchInput struct {
	Query  string `json:"query"`
	Target string `json:"target,omitempty"`
	From   string `json:"from,omitempty"`
	After  string `json:"after,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}
type mcpReactionInput struct {
	ID       string `json:"id"`
	Reaction string `json:"reaction"`
}
type mcpCorrectionInput struct {
	ID      string `json:"id"`
	Message string `json:"message,omitempty"`
}
type mcpIDInput struct {
	ID string `json:"id"`
}
type mcpPrepareInput struct {
	ID      string `json:"id"`
	Seconds int    `json:"seconds,omitempty" jsonschema:"Reply expected within 1-900 seconds; default 120"`
	Message string `json:"message,omitempty"`
}

func (a *mcpAdapter) addChatTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "search", Description: "Search original retained messages. Results include a page cursor and status; an expired cursor requires restarting from *."}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpSearchInput) (*mcp.CallToolResult, mcpOutput, error) {
		args := []string{"search", in.Query}
		for _, pair := range [][2]string{{"--target", in.Target}, {"--from", in.From}, {"--after", in.After}} {
			if pair[1] != "" {
				args = append(args, pair[0], pair[1])
			}
		}
		if in.Limit != 0 {
			args = append(args, "--limit", strconv.Itoa(in.Limit))
		}
		return a.call(ctx, args, "", 15*time.Second, false)
	})
	mcp.AddTool(s, &mcp.Tool{Name: "react", Description: "Add a reaction to a retained message. A reaction is not approval or evidence that work was completed. No durable receipt-only retry is available."}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpReactionInput) (*mcp.CallToolResult, mcpOutput, error) {
		if !protocol.ValidMessageID(in.ID) || !protocol.ValidReaction(in.Reaction) {
			return nil, mcpOutput{}, errors.New("valid message ID and reaction required")
		}
		return a.call(ctx, []string{"send", "--reply-to", in.ID, "--reaction", in.Reaction}, "", 15*time.Second, false)
	})
	for _, action := range []string{"correct", "retract"} {
		mcp.AddTool(s, &mcp.Tool{Name: action, Description: action + " your retained message by appending a linked record. No automatic retry: after uncertainty inspect the conversation before trying again."}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpCorrectionInput) (*mcp.CallToolResult, mcpOutput, error) {
			if !protocol.ValidMessageID(in.ID) {
				return nil, mcpOutput{}, errors.New("valid message ID required")
			}
			return a.call(ctx, []string{action, in.ID, "--message", in.Message}, "", 15*time.Second, false)
		})
	}
	for _, action := range []string{"follow", "unfollow", "waiting", "cancel"} {
		description := map[string]string{
			"follow":   "Include this conversation in this identity's future checks; returns followed thread IDs.",
			"unfollow": "Remove a canonical thread ID from this identity's checks; leaves server history unchanged.",
			"waiting":  "Read unexpired reply-coming signals. A promise, silence or presence is not approval.",
			"cancel":   "Cancel your reply-coming signal for this message; does not cancel anyone's work.",
		}[action]
		mcp.AddTool(s, &mcp.Tool{Name: action, Description: description}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpIDInput) (*mcp.CallToolResult, mcpOutput, error) {
			if !protocol.ValidMessageID(in.ID) {
				return nil, mcpOutput{}, errors.New("valid message ID required")
			}
			args := []string{action, in.ID}
			if action == "cancel" {
				args = []string{"prepare", in.ID, "--cancel"}
			}
			return a.call(ctx, args, "", 15*time.Second, false)
		})
	}
	mcp.AddTool(s, &mcp.Tool{Name: "prepare", Description: "Announce a forthcoming reply with an expiry. This is a conversational signal, not an ownership claim or a lock."}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpPrepareInput) (*mcp.CallToolResult, mcpOutput, error) {
		if !protocol.ValidMessageID(in.ID) || in.Seconds < 0 || in.Seconds > 900 {
			return nil, mcpOutput{}, errors.New("valid ID and seconds 1-900 required")
		}
		if in.Seconds == 0 {
			in.Seconds = 120
		}
		return a.call(ctx, []string{"prepare", in.ID, "--eta", fmt.Sprintf("%ds", in.Seconds), "--message", in.Message}, "", 15*time.Second, false)
	})
}
