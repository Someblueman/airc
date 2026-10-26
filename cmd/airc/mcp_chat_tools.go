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
	Run    string `json:"run,omitempty" jsonschema:"Only messages posted under this run label"`
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
	Step    string `json:"step,omitempty" jsonschema:"offer (the default), accept, blocked, decline, done or withdraw"`
	ID      string `json:"id,omitempty" jsonschema:"The handoff's ID, for every step but offer"`
	Channel string `json:"channel,omitempty" jsonschema:"Offer: the room to hand the work to, or to post it in; defaults to AIRC_CHANNEL"`
	To      string `json:"to,omitempty" jsonschema:"Offer: the nickname the work is for; without channel, a direct handoff"`
	Message string `json:"message,omitempty" jsonschema:"Offer: what to hand over, with branch, commits and what is left. A step: an optional note, such as what done delivered; blocked: why it is stuck, required"`
	Link    string `json:"link,omitempty" jsonschema:"Done: where the result is, an absolute http(s) URL of a PR, a commit or a document, up to 400 bytes"`
	Run     string `json:"run,omitempty" jsonschema:"Run label kept on the post; defaults to AIRC_RUN"`
}
type mcpHandoffsInput struct {
	Channel string `json:"channel,omitempty" jsonschema:"Every handoff in this room, finished ones from the last week included"`
	ID      string `json:"id,omitempty" jsonschema:"One handoff"`
	Limit   int    `json:"limit,omitempty" jsonschema:"Newest handoffs to list, 1-1000; default 200"`
}

type mcpDecideInput struct {
	Step    string   `json:"step,omitempty" jsonschema:"ask (the default), answer or withdraw"`
	ID      string   `json:"id,omitempty" jsonschema:"The decision's ID, for answer and withdraw"`
	Channel string   `json:"channel,omitempty" jsonschema:"Ask: the room to ask in; defaults to AIRC_CHANNEL; needs from"`
	From    string   `json:"from,omitempty" jsonschema:"Ask: the nickname who decides, usually a human; required in a room"`
	To      string   `json:"to,omitempty" jsonschema:"Ask: the decider of a direct decision, without channel"`
	Message string   `json:"message,omitempty" jsonschema:"Ask: what to decide and why it matters. Answer: a note; withdraw: an optional reason"`
	Options []string `json:"options,omitempty" jsonschema:"Ask: 2-8 choices, each up to 80 bytes; omit for a free-text decision"`
	Choice  int      `json:"choice,omitempty" jsonschema:"Answer: the chosen option, numbered from 1"`
	Run     string   `json:"run,omitempty" jsonschema:"Run label kept on the post; defaults to AIRC_RUN"`
}
type mcpStatusInput struct {
	Task    string `json:"task" jsonschema:"What you are on, one line of at most 200 bytes"`
	State   string `json:"state" jsonschema:"working, blocked or done"`
	Channel string `json:"channel,omitempty" jsonschema:"The room the status is for; defaults to AIRC_CHANNEL"`
	Link    string `json:"link,omitempty" jsonschema:"Where the work is: an absolute http(s) URL of a PR, a branch or a document, up to 400 bytes"`
	Message string `json:"message,omitempty" jsonschema:"A note on the status, such as what is blocking you"`
	Run     string `json:"run,omitempty" jsonschema:"Run label kept on the post; defaults to AIRC_RUN"`
}
type mcpResolveInput struct {
	ID      string `json:"id" jsonschema:"ID of the question, or of any message in its thread"`
	Message string `json:"message,omitempty" jsonschema:"How it was settled; default \"Resolved\""`
}
type mcpDecisionsInput struct {
	Channel string `json:"channel,omitempty" jsonschema:"Every decision asked in this room, answered ones from the last week included"`
	ID      string `json:"id,omitempty" jsonschema:"One decision"`
	Limit   int    `json:"limit,omitempty" jsonschema:"Newest decisions to list, 1-1000; default 200"`
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
	Run    string `json:"run,omitempty" jsonschema:"Only messages posted under this run label"`
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
	Kind      *string `json:"kind,omitempty" jsonschema:"human or agent; empty string clears it"`
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
		if in.Run != "" {
			args = append(args, "--run", in.Run)
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
		if in.Kind != nil {
			switch *in.Kind {
			case "human":
				args = append(args, "--human")
			case "agent":
				args = append(args, "--agent")
			default:
				return nil, mcpOutput{}, errors.New("kind is human or agent")
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
		for _, pair := range [][2]string{{"--target", in.Target}, {"--from", in.From}, {"--after", in.After}, {"--since", in.Since}, {"--until", in.Until}, {"--run", in.Run}} {
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
	mcpserver.AddTool(s, &mcpserver.Tool{Name: "handoff", Description: "Hand work to a nickname, to anyone in a room, or to a nickname in a room, then take it through its steps: accept (in a room, the first to accept takes it), blocked (the holder says why it is stuck; accept again when it moves), decline (an accepted room handoff goes back to the room), done (tells everyone it is finished, with a link to the result) or withdraw (the author calls it off). Each step is posted in the room or direct conversation, replying to the offer."}, func(ctx context.Context, _ *mcpserver.Call, in mcpHandoffInput) (*mcpserver.Result, mcpOutput, error) {
		args := []string{"handoff"}
		if in.Link != "" && in.Step != "done" {
			return nil, mcpOutput{}, errors.New("link applies to done")
		}
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
		case "accept", "blocked", "decline", "done", "withdraw":
			if !protocol.ValidMessageID(in.ID) || in.Channel != "" || in.To != "" {
				return nil, mcpOutput{}, errors.New(in.Step + " takes the handoff's id and no channel or to")
			}
			if in.Step == "blocked" && in.Message == "" {
				return nil, mcpOutput{}, errors.New("blocked takes a message saying why")
			}
			args = append(args, in.Step, in.ID)
			if in.Link != "" {
				args = append(args, "--link="+in.Link)
			}
		default:
			return nil, mcpOutput{}, errors.New("step is offer, accept, blocked, decline, done or withdraw")
		}
		if in.Message != "" {
			args = append(args, "--message="+in.Message)
		}
		if in.Run != "" {
			args = append(args, "--run="+in.Run)
		}
		return a.call(ctx, args, "", 15*time.Second, false)
	})
	mcpserver.AddTool(s, &mcpserver.Tool{Name: "handoffs", Annotations: &mcpserver.Annotations{ReadOnlyHint: true}, Description: "List open, accepted and blocked handoffs that concern you, blocked ones first: for you, held by you, offered by you, or open to anyone in a room you checked, waited or posted in within ten minutes. Rows carry blocked_reason and blocked_at, and link once done. With channel, every handoff in that room, open ones anyone may take and finished ones from the last week included."}, func(ctx context.Context, _ *mcpserver.Call, in mcpHandoffsInput) (*mcpserver.Result, mcpOutput, error) {
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
	mcpserver.AddTool(s, &mcpserver.Tool{Name: "decide", Description: "Ask a named decider, usually a human, to decide something: in a room (channel and from) or directly (to), with optional numbered options. Then answer (choice and/or message) a decision asked of you, or withdraw one you asked. Each step is posted replying to the question; wait for an answer with check and its decision field."}, func(ctx context.Context, _ *mcpserver.Call, in mcpDecideInput) (*mcpserver.Result, mcpOutput, error) {
		args := []string{"decide"}
		switch in.Step {
		case "", "ask":
			if in.ID != "" || in.Choice != 0 {
				return nil, mcpOutput{}, errors.New("ask takes no id or choice; it creates a decision")
			}
			for _, pair := range [][2]string{{"--channel", in.Channel}, {"--from", in.From}, {"--to", in.To}} {
				if pair[1] != "" {
					args = append(args, pair[0]+"="+pair[1])
				}
			}
			for _, option := range in.Options {
				args = append(args, "--option="+option)
			}
		case "answer", "withdraw":
			if !protocol.ValidMessageID(in.ID) || in.Channel != "" || in.From != "" || in.To != "" || len(in.Options) > 0 {
				return nil, mcpOutput{}, errors.New(in.Step + " takes the decision's id and no channel, from, to or options")
			}
			if in.Choice != 0 && in.Step != "answer" {
				return nil, mcpOutput{}, errors.New("choice applies to answer")
			}
			args = append(args, in.Step, in.ID)
			if in.Choice != 0 {
				args = append(args, "--choice", strconv.Itoa(in.Choice))
			}
		default:
			return nil, mcpOutput{}, errors.New("step is ask, answer or withdraw")
		}
		if in.Message != "" {
			args = append(args, "--message="+in.Message)
		}
		if in.Run != "" {
			args = append(args, "--run="+in.Run)
		}
		return a.call(ctx, args, "", 15*time.Second, false)
	})
	mcpserver.AddTool(s, &mcpserver.Tool{Name: "decisions", Annotations: &mcpserver.Annotations{ReadOnlyHint: true}, Description: "List open decisions that concern you: for you to make, asked by you, or asked in a room you checked, waited or posted in within ten minutes. With channel, every decision asked in that room, answered and withdrawn ones from the last week included; with id, one."}, func(ctx context.Context, _ *mcpserver.Call, in mcpDecisionsInput) (*mcpserver.Result, mcpOutput, error) {
		args := []string{"decisions"}
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
	mcpserver.AddTool(s, &mcpserver.Tool{Name: "status", Description: "Post where you are in a room: the task, whether you are working, blocked or done with it, and optionally where the work is. The room reads it as \"Status [state] task: note\"; the daemon keeps your latest for directory cards, agent listings and the digest's \"where everyone is\", so post one at a change, not every step."}, func(ctx context.Context, _ *mcpserver.Call, in mcpStatusInput) (*mcpserver.Result, mcpOutput, error) {
		if strings.TrimSpace(in.Task) == "" || !protocol.ValidStatusState(in.State) {
			return nil, mcpOutput{}, errors.New("task and a state of working, blocked or done required")
		}
		args := []string{"status", "--task=" + in.Task, "--state=" + in.State}
		for _, pair := range [][2]string{{"--channel", in.Channel}, {"--link", in.Link}, {"--message", in.Message}, {"--run", in.Run}} {
			if pair[1] != "" {
				args = append(args, pair[0]+"="+pair[1])
			}
		}
		return a.call(ctx, args, "", 15*time.Second, false)
	})
	mcpserver.AddTool(s, &mcpserver.Tool{Name: "resolve", Description: "Close a question that is settled, in a direct message, by a reaction or in person: posts a resolution replying to its thread's root, tagging nobody, so digests stop waiting on it. Allowed for the question's author, whoever it addresses, or an admin."}, func(ctx context.Context, _ *mcpserver.Call, in mcpResolveInput) (*mcpserver.Result, mcpOutput, error) {
		if !protocol.ValidMessageID(in.ID) {
			return nil, mcpOutput{}, errors.New("valid message ID required")
		}
		args := []string{"resolve", in.ID}
		if in.Message != "" {
			args = append(args, "--message="+in.Message)
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
