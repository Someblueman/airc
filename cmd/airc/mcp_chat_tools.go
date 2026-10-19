package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/mcpserver"
	"github.com/Someblueman/airc/internal/protocol"
)

type mcpSearchInput struct {
	Query  string `json:"query" jsonschema:"Words that must all occur (case-insensitive substrings), \"quoted phrases\", and -word to exclude; at most 256 bytes"`
	Target string `json:"target,omitempty" jsonschema:"#room, @nick, thread:ID or * for everything; defaults to AIRC_CHANNEL, else *"`
	From   string `json:"from,omitempty" jsonschema:"Only this sender"`
	After  string `json:"after,omitempty" jsonschema:"Cursor from the previous page's page row; * restarts"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum matches, 1-1000; default 50"`
	Since  string `json:"since,omitempty" jsonschema:"Only messages at or after: a duration ago (90m, 2h, 3d, 1w), a date (2026-10-17) or RFC 3339"`
	Until  string `json:"until,omitempty" jsonschema:"Only messages before, in the same forms as since"`
	Newest bool   `json:"newest,omitempty" jsonschema:"Newest matches first; status archived means older matches may remain on disk"`
	Exact  bool   `json:"exact,omitempty" jsonschema:"Match the whole query as one literal substring"`
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

type mcpHandoffInput struct {
	Step    string `json:"step,omitempty" jsonschema:"offer (the default), accept, decline, done or withdraw"`
	ID      string `json:"id,omitempty" jsonschema:"The handoff's ID, for every step but offer"`
	Channel string `json:"channel,omitempty" jsonschema:"Offer: the room to hand the work to, or to post it in; defaults to AIRC_CHANNEL"`
	To      string `json:"to,omitempty" jsonschema:"Offer: the nickname the work is for; without channel, a direct handoff"`
	Message string `json:"message,omitempty" jsonschema:"Offer: what to hand over, with branch, commits and what is left. A step: an optional note, such as what done delivered"`
}
type mcpHandoffsInput struct {
	Channel string `json:"channel,omitempty" jsonschema:"Every handoff in this room, finished ones from the last week included"`
	ID      string `json:"id,omitempty" jsonschema:"One handoff"`
	Limit   int    `json:"limit,omitempty" jsonschema:"Newest handoffs to list, 1-1000; default 200"`
}

type mcpUnreadInput struct {
	Channels []string `json:"channels,omitempty" jsonschema:"Rooms to count besides the inbox; defaults to AIRC_CHANNEL"`
	Mentions bool     `json:"mentions,omitempty" jsonschema:"Count only direct messages and tags"`
}
type mcpDigestInput struct {
	Channels []string `json:"channels,omitempty" jsonschema:"Rooms to summarize besides the inbox; defaults to AIRC_CHANNEL"`
	MarkRead bool     `json:"mark_read,omitempty" jsonschema:"Mark everything summarized as read, as check would; read what matters first"`
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
func (a *mcpAdapter) addOverviewTools(s *mcpserver.Server) {
	readOnly := &mcpserver.Annotations{ReadOnlyHint: true}
	mcpserver.AddTool(s, &mcpserver.Tool{Name: "unread", Annotations: readOnly, Description: "Count unread messages per room and inbox without marking anything read. Cheap; use it to decide whether a check is worthwhile."}, func(ctx context.Context, _ *mcpserver.Call, in mcpUnreadInput) (*mcpserver.Result, mcpOutput, error) {
		args := []string{"unread"}
		for _, channel := range in.Channels {
			args = append(args, "--channel", channel)
		}
		if in.Mentions {
			args = append(args, "--mentions")
		}
		return a.call(ctx, args, "", 15*time.Second, false)
	})
	mcpserver.AddTool(s, &mcpserver.Tool{Name: "digest", Description: "Summarize unread traffic without message bodies: counts, speakers and busy threads per room, unanswered messages addressed to you with previews, corrections, open polls, and changed headers and pins. Marks nothing read unless mark_read; use it after time away instead of reading the whole backlog."}, func(ctx context.Context, _ *mcpserver.Call, in mcpDigestInput) (*mcpserver.Result, mcpOutput, error) {
		args := []string{"digest"}
		for _, channel := range in.Channels {
			args = append(args, "--channel", channel)
		}
		if in.MarkRead {
			args = append(args, "--mark-read")
		}
		return a.call(ctx, args, "", 15*time.Second, false)
	})
	mcpserver.AddTool(s, &mcpserver.Tool{Name: "channels", Annotations: readOnly, Description: "List the rooms the server knows, with connected members, retained messages, last activity and header."}, func(ctx context.Context, _ *mcpserver.Call, _ mcpNoInput) (*mcpserver.Result, mcpOutput, error) {
		return a.call(ctx, []string{"channels"}, "", 15*time.Second, false)
	})
	mcpserver.AddTool(s, &mcpserver.Tool{Name: "history", Annotations: readOnly, Description: "Read retained messages of a room or a nickname's direct messages without moving check cursors. Ends with a page row; pass its cursor as after."}, func(ctx context.Context, _ *mcpserver.Call, in mcpHistoryInput) (*mcpserver.Result, mcpOutput, error) {
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
	mcpserver.AddTool(s, &mcpserver.Tool{Name: "presence", Description: "Publish or clear an expiring activity state for this identity. Self-reported; expiry means unknown, not available."}, func(ctx context.Context, _ *mcpserver.Call, in mcpPresenceInput) (*mcpserver.Result, mcpOutput, error) {
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
	mcpserver.AddTool(s, &mcpserver.Tool{Name: "profile", Description: "Update the self-reported profile peers see in the directory. Only supplied fields change."}, func(ctx context.Context, _ *mcpserver.Call, in mcpProfileInput) (*mcpserver.Result, mcpOutput, error) {
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

func (a *mcpAdapter) addChatTools(s *mcpserver.Server) {
	mcpserver.AddTool(s, &mcpserver.Tool{Name: "search", Annotations: &mcpserver.Annotations{ReadOnlyHint: true}, Description: "Search original retained messages: every word must occur, \"quoted phrases\" stay together, -word excludes. Narrow with since/until; newest returns the latest matches first. Results end with a page row whose cursor continues the search; an expired cursor requires restarting from *."}, func(ctx context.Context, _ *mcpserver.Call, in mcpSearchInput) (*mcpserver.Result, mcpOutput, error) {
		args := []string{"search", in.Query}
		for _, pair := range [][2]string{{"--target", in.Target}, {"--from", in.From}, {"--after", in.After}, {"--since", in.Since}, {"--until", in.Until}} {
			if pair[1] != "" {
				args = append(args, pair[0]+"="+pair[1])
			}
		}
		if in.Limit != 0 {
			args = append(args, "--limit", strconv.Itoa(in.Limit))
		}
		if in.Newest {
			args = append(args, "--newest")
		}
		if in.Exact {
			args = append(args, "--exact")
		}
		return a.call(ctx, args, "", 15*time.Second, false)
	})
	mcpserver.AddTool(s, &mcpserver.Tool{Name: "react", Description: "Add a reaction to a retained message. A reaction is not approval or evidence that work was completed. No durable receipt-only retry is available."}, func(ctx context.Context, _ *mcpserver.Call, in mcpReactionInput) (*mcpserver.Result, mcpOutput, error) {
		if !protocol.ValidMessageID(in.ID) || !protocol.ValidReaction(in.Reaction) {
			return nil, mcpOutput{}, errors.New("valid message ID and reaction required")
		}
		return a.call(ctx, []string{"send", "--reply-to", in.ID, "--reaction", in.Reaction}, "", 15*time.Second, false)
	})
	for _, action := range []string{"correct", "retract"} {
		mcpserver.AddTool(s, &mcpserver.Tool{Name: action, Description: action + " your retained message by appending a linked record. No automatic retry: after uncertainty inspect the conversation before trying again."}, func(ctx context.Context, _ *mcpserver.Call, in mcpCorrectionInput) (*mcpserver.Result, mcpOutput, error) {
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
		tool := &mcpserver.Tool{Name: action, Description: description}
		if action == "waiting" {
			tool.Annotations = &mcpserver.Annotations{ReadOnlyHint: true}
		}
		mcpserver.AddTool(s, tool, func(ctx context.Context, _ *mcpserver.Call, in mcpIDInput) (*mcpserver.Result, mcpOutput, error) {
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
	mcpserver.AddTool(s, &mcpserver.Tool{Name: "handoff", Description: "Hand work to a nickname, to anyone in a room, or to a nickname in a room, then take it through its steps: accept (in a room, the first to accept takes it), decline (an accepted room handoff goes back to the room), done (tells everyone it is finished) or withdraw (the author calls it off). Each step is posted in the room or direct conversation, replying to the offer."}, func(ctx context.Context, _ *mcpserver.Call, in mcpHandoffInput) (*mcpserver.Result, mcpOutput, error) {
		args := []string{"handoff"}
		switch in.Step {
		case "", "offer":
			if in.ID != "" {
				return nil, mcpOutput{}, errors.New("an offer takes no id; it creates one")
			}
			for _, pair := range [][2]string{{"--channel", in.Channel}, {"--to", in.To}} {
				if pair[1] != "" {
					args = append(args, pair[0]+"="+pair[1])
				}
			}
		case "accept", "decline", "done", "withdraw":
			if !protocol.ValidMessageID(in.ID) || in.Channel != "" || in.To != "" {
				return nil, mcpOutput{}, errors.New(in.Step + " takes the handoff's id and no channel or to")
			}
			args = append(args, in.Step, in.ID)
		default:
			return nil, mcpOutput{}, errors.New("step is offer, accept, decline, done or withdraw")
		}
		if in.Message != "" {
			args = append(args, "--message="+in.Message)
		}
		return a.call(ctx, args, "", 15*time.Second, false)
	})
	mcpserver.AddTool(s, &mcpserver.Tool{Name: "handoffs", Annotations: &mcpserver.Annotations{ReadOnlyHint: true}, Description: "List open and accepted handoffs that concern you: for you, held by you, offered by you, or open to anyone in your rooms. With channel, every handoff in that room, finished ones from the last week included."}, func(ctx context.Context, _ *mcpserver.Call, in mcpHandoffsInput) (*mcpserver.Result, mcpOutput, error) {
		args := []string{"handoffs"}
		for _, pair := range [][2]string{{"--channel", in.Channel}, {"--id", in.ID}} {
			if pair[1] != "" {
				args = append(args, pair[0]+"="+pair[1])
			}
		}
		if in.Limit != 0 {
			args = append(args, "--limit", strconv.Itoa(in.Limit))
		}
		return a.call(ctx, args, "", 15*time.Second, false)
	})
	mcpserver.AddTool(s, &mcpserver.Tool{Name: "prepare", Description: "Announce a forthcoming reply with an expiry. This is a conversational signal, not an ownership claim or a lock."}, func(ctx context.Context, _ *mcpserver.Call, in mcpPrepareInput) (*mcpserver.Result, mcpOutput, error) {
		if !protocol.ValidMessageID(in.ID) || in.Seconds < 0 || in.Seconds > 900 {
			return nil, mcpOutput{}, errors.New("valid ID and seconds 1-900 required")
		}
		if in.Seconds == 0 {
			in.Seconds = 120
		}
		return a.call(ctx, []string{"prepare", in.ID, "--eta", fmt.Sprintf("%ds", in.Seconds), "--message", in.Message}, "", 15*time.Second, false)
	})
}
