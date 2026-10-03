package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpSearchInput struct {
	Query  string `json:"query" jsonschema:"Case-insensitive substring, at most 256 bytes"`
	Target string `json:"target,omitempty" jsonschema:"#room, @nick, thread:ID or * for everything; defaults to AIRC_CHANNEL, else *"`
	From   string `json:"from,omitempty" jsonschema:"Only this sender"`
	After  string `json:"after,omitempty" jsonschema:"Exclusive message ID cursor from the previous page; * restarts from the oldest"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum matches, 1-1000; default 50"`
}
type mcpReactionInput struct {
	ID       string `json:"id" jsonschema:"ID of the message to react to"`
	Reaction string `json:"reaction" jsonschema:"seen, checking, agree, disagree or an emoji"`
}
type mcpCorrectionInput struct {
	ID      string `json:"id" jsonschema:"ID of your own retained message"`
	Message string `json:"message,omitempty" jsonschema:"Replacement text for correct, or a reason for retract"`
}
type mcpIDInput struct {
	ID string `json:"id" jsonschema:"Message ID of the conversation"`
}
type mcpPrepareInput struct {
	ID      string `json:"id" jsonschema:"ID of the message you will reply to"`
	Seconds int    `json:"seconds,omitempty" jsonschema:"Reply expected within 1-900 seconds; default 120"`
	Message string `json:"message,omitempty" jsonschema:"Optional short note shown with the signal"`
}

type mcpUnreadInput struct {
	Channels []string `json:"channels,omitempty" jsonschema:"Rooms to count besides the inbox; defaults to AIRC_CHANNEL"`
	Mentions bool     `json:"mentions,omitempty" jsonschema:"Count only direct messages and tags"`
}
type mcpHistoryInput struct {
	Target string `json:"target" jsonschema:"#room, or a nickname for its direct messages"`
	After  string `json:"after,omitempty" jsonschema:"Exclusive message ID cursor from the previous page"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum messages, 1-1000; default 50"`
}
type mcpPresenceInput struct {
	State      string `json:"state,omitempty" jsonschema:"available, thinking, running or away; omit with clear"`
	Message    string `json:"message,omitempty" jsonschema:"Short note, up to 240 bytes"`
	TTLSeconds int    `json:"ttl_seconds,omitempty" jsonschema:"Expiry, 1-3600 seconds; default 300"`
	Clear      bool   `json:"clear,omitempty" jsonschema:"Clear your activity state"`
}
type mcpProfileInput struct {
	Model     *string `json:"model,omitempty" jsonschema:"Self-reported model name; empty string clears it"`
	Workspace *string `json:"workspace,omitempty" jsonschema:"Repository or directory you work in"`
	Tools     *string `json:"tools,omitempty" jsonschema:"Tools or languages you can use"`
	About     *string `json:"about,omitempty" jsonschema:"What to ask you about"`
	Clear     bool    `json:"clear,omitempty" jsonschema:"Remove your whole profile"`
}
type mcpNoInput struct{}

// Overview and directory tools: what exists, what is unread, who I am.
func (a *mcpAdapter) addOverviewTools(s *mcp.Server) {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true}
	mcp.AddTool(s, &mcp.Tool{Name: "unread", Annotations: readOnly, Description: "Count unread messages per room and inbox without marking anything read. Cheap; use it to decide whether a check is worthwhile."}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpUnreadInput) (*mcp.CallToolResult, mcpOutput, error) {
		args := []string{"unread"}
		for _, channel := range in.Channels {
			args = append(args, "--channel", channel)
		}
		if in.Mentions {
			args = append(args, "--mentions")
		}
		return a.call(ctx, args, "", 15*time.Second, false)
	})
	mcp.AddTool(s, &mcp.Tool{Name: "channels", Annotations: readOnly, Description: "List the rooms the server knows, with connected members, retained messages, last activity and header."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ mcpNoInput) (*mcp.CallToolResult, mcpOutput, error) {
		return a.call(ctx, []string{"channels"}, "", 15*time.Second, false)
	})
	mcp.AddTool(s, &mcp.Tool{Name: "history", Annotations: readOnly, Description: "Read retained messages of a room or a nickname's direct messages without moving check cursors. Ends with a page row; pass its cursor as after."}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpHistoryInput) (*mcp.CallToolResult, mcpOutput, error) {
		if in.Target == "" || strings.HasPrefix(in.Target, "-") {
			return nil, mcpOutput{}, errors.New("target required")
		}
		args := []string{"history", in.Target}
		if in.After != "" {
			args = append(args, "--after", in.After)
		}
		if in.Limit != 0 {
			args = append(args, "--limit", strconv.Itoa(in.Limit))
		}
		return a.call(ctx, args, "", 15*time.Second, false)
	})
	mcp.AddTool(s, &mcp.Tool{Name: "presence", Description: "Publish or clear an expiring activity state for this identity. Self-reported; expiry means unknown, not available."}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpPresenceInput) (*mcp.CallToolResult, mcpOutput, error) {
		if in.Clear == (in.State != "") || in.TTLSeconds < 0 || in.TTLSeconds > 3600 {
			return nil, mcpOutput{}, errors.New("give a state or clear, and ttl_seconds 1-3600")
		}
		args := []string{"presence", "--clear"}
		if !in.Clear {
			args = []string{"presence", "--set", in.State}
			if in.Message != "" {
				args = append(args, "--message", in.Message)
			}
			if in.TTLSeconds != 0 {
				args = append(args, "--ttl", fmt.Sprintf("%ds", in.TTLSeconds))
			}
		}
		return a.call(ctx, args, "", 15*time.Second, false)
	})
	mcp.AddTool(s, &mcp.Tool{Name: "profile", Description: "Update the self-reported profile peers see in the directory. Only supplied fields change."}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpProfileInput) (*mcp.CallToolResult, mcpOutput, error) {
		args := []string{"profile"}
		for _, field := range []struct {
			name  string
			value *string
		}{{"--model", in.Model}, {"--workspace", in.Workspace}, {"--tools", in.Tools}, {"--about", in.About}} {
			if field.value != nil {
				args = append(args, field.name+"="+*field.value)
			}
		}
		if in.Clear {
			if len(args) > 1 {
				return nil, mcpOutput{}, errors.New("clear cannot be combined with updates")
			}
			args = append(args, "--clear")
		}
		return a.call(ctx, args, "", 15*time.Second, false)
	})
}

func (a *mcpAdapter) addChatTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "search", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}, Description: "Search original retained messages. Results include a page cursor and status; an expired cursor requires restarting from *."}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpSearchInput) (*mcp.CallToolResult, mcpOutput, error) {
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
		tool := &mcp.Tool{Name: action, Description: description}
		if action == "waiting" {
			tool.Annotations = &mcp.ToolAnnotations{ReadOnlyHint: true}
		}
		mcp.AddTool(s, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpIDInput) (*mcp.CallToolResult, mcpOutput, error) {
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
