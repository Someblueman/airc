package main

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// A structured chat action (a handoff, a decision, a status, a resolution, a
// poll, a pin) has one input type, which every surface fills: the CLI from
// its flags, the UI from its slash command, and the MCP tool from its
// arguments, whose schema the type is. request checks the input once, with
// one wording, and shapes the CHAT request; args is the CLI argv that makes
// the same request, which the MCP adapter runs in process. Each surface keeps
// only its own syntax: how a flag, a slash command or a tool argument names
// the field.

// chatInput is one structured chat action's input.
type chatInput interface {
	request() (irc.ChatRequest, error)
	args() []string
}

// handoffInput offers a handoff, or takes one through a step.
type handoffInput struct {
	Step    string `json:"step,omitempty" jsonschema:"offer (the default), accept, blocked, decline, done or withdraw"`
	ID      string `json:"id,omitempty" jsonschema:"The handoff's ID, for every step but offer"`
	Channel string `json:"channel,omitempty" jsonschema:"Offer: the room to hand the work to, or to post it in; defaults to AIRC_CHANNEL"`
	To      string `json:"to,omitempty" jsonschema:"Offer: the nickname the work is for; without channel, a direct handoff"`
	Message string `json:"message,omitempty" jsonschema:"Offer: what to hand over, with branch, commits and what is left. A step: an optional note, such as what done delivered; blocked: why it is stuck, required"`
	Link    string `json:"link,omitempty" jsonschema:"Done: where the result is, an absolute http(s) URL of a PR, a commit or a document, up to 400 bytes"`
	Run     string `json:"run,omitempty" jsonschema:"Run label kept on the post; defaults to AIRC_RUN"`
}

// handoffSteps maps the step words to their CHAT actions.
var handoffSteps = map[string]string{
	"accept":   "handoff-accept",
	"blocked":  "handoff-blocked",
	"decline":  "handoff-decline",
	"done":     "handoff-done",
	"withdraw": "handoff-withdraw",
}

func (in handoffInput) request() (irc.ChatRequest, error) {
	step := cmp.Or(in.Step, "offer")
	if step != "offer" && handoffSteps[step] == "" {
		return irc.ChatRequest{}, fmt.Errorf("unknown handoff step %q: use accept, blocked, decline, done or withdraw ID", step)
	}
	if in.Link != "" && step != "done" {
		return irc.ChatRequest{}, errors.New("--link applies to done")
	}
	if err := validLink(in.Link); err != nil {
		return irc.ChatRequest{}, err
	}
	run, err := runLabel(in.Run)
	if err != nil {
		return irc.ChatRequest{}, err
	}
	r := irc.ChatRequest{Action: "handoff", Link: in.Link, Run: run}
	if step == "offer" {
		if in.ID != "" {
			return irc.ChatRequest{}, errors.New("an offer takes no ID; it creates one")
		}
		channel := in.Channel
		if channel == "" && in.To == "" {
			channel = os.Getenv("AIRC_CHANNEL")
		}
		if channel == "" && in.To == "" {
			return irc.ChatRequest{}, errors.New("hand off to a room (--channel), a nickname (--to), or a nickname in a room (both)")
		}
		if r.Text, err = messageText(in.Message, true); err != nil {
			return irc.ChatRequest{}, err
		}
		r.For = in.To
		if r.Target = channelName(channel); r.Target == "" {
			r.Target = in.To
		}
		return r, nil
	}
	if !protocol.ValidMessageID(in.ID) {
		return irc.ChatRequest{}, fmt.Errorf("handoff %s needs the handoff's ID", step)
	}
	if in.Channel != "" || in.To != "" {
		return irc.ChatRequest{}, errors.New("a handoff step names only the handoff's ID; it goes where the handoff is")
	}
	if step == "blocked" && strings.TrimSpace(in.Message) == "" {
		return irc.ChatRequest{}, errors.New("handoff blocked needs --message saying why it is stuck")
	}
	if r.Text, err = messageText(in.Message, false); err != nil {
		return irc.ChatRequest{}, err
	}
	r.Action, r.ID = handoffSteps[step], in.ID
	return r, nil
}

func (in handoffInput) args() []string {
	args := []string{"handoff"}
	if in.Step != "" && in.Step != "offer" {
		args = append(args, in.Step, in.ID)
	}
	return appendFlags(args, [][2]string{{"channel", in.Channel}, {"to", in.To}, {"message", in.Message}, {"link", in.Link}, {"run", in.Run}})
}

// decisionInput asks a decider to decide something, or answers or withdraws
// a decision.
type decisionInput struct {
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

// decisionSteps maps the step words to their CHAT actions.
var decisionSteps = map[string]string{
	"answer":   "decision-answer",
	"withdraw": "decision-withdraw",
}

func (in decisionInput) request() (irc.ChatRequest, error) {
	step := cmp.Or(in.Step, "ask")
	if step != "ask" && decisionSteps[step] == "" {
		return irc.ChatRequest{}, fmt.Errorf("unknown decision step %q: use answer or withdraw ID", step)
	}
	if in.Choice != 0 && step != "answer" {
		return irc.ChatRequest{}, errors.New("--choice applies to answer")
	}
	run, err := runLabel(in.Run)
	if err != nil {
		return irc.ChatRequest{}, err
	}
	r := irc.ChatRequest{Action: "decision", Run: run}
	if step == "ask" {
		if in.ID != "" {
			return irc.ChatRequest{}, errors.New("ask takes no ID; it creates a decision")
		}
		channel := in.Channel
		if channel == "" && in.To == "" {
			channel = os.Getenv("AIRC_CHANNEL")
		}
		if in.To != "" && (channel != "" || in.From != "" && !strings.EqualFold(in.From, in.To)) {
			return irc.ChatRequest{}, errors.New("--to names the decider of a direct decision; use --from in a room")
		}
		if channel == "" && in.To == "" || channel != "" && in.From == "" {
			return irc.ChatRequest{}, errors.New("ask a room with --channel --from, or a nickname with --to")
		}
		if r.Text, err = messageText(in.Message, true); err != nil {
			return irc.ChatRequest{}, err
		}
		r.For, r.Options = in.From, in.Options
		if r.Target = channelName(channel); r.Target == "" {
			r.Target, r.For = in.To, in.To
		}
		return r, nil
	}
	if !protocol.ValidMessageID(in.ID) {
		return irc.ChatRequest{}, fmt.Errorf("decide %s needs the decision's ID", step)
	}
	if in.Channel != "" || in.From != "" || in.To != "" || len(in.Options) > 0 {
		return irc.ChatRequest{}, errors.New("a decision step names only the decision's ID; it goes where the decision is")
	}
	if in.Choice < 0 {
		return irc.ChatRequest{}, errors.New("--choice numbers an option from 1")
	}
	if r.Text, err = messageText(in.Message, false); err != nil {
		return irc.ChatRequest{}, err
	}
	r.Action, r.ID, r.Choice = decisionSteps[step], in.ID, in.Choice
	return r, nil
}

func (in decisionInput) args() []string {
	args := []string{"decide"}
	if in.Step != "" && in.Step != "ask" {
		args = append(args, in.Step, in.ID)
	}
	args = appendFlags(args, [][2]string{{"channel", in.Channel}, {"from", in.From}, {"to", in.To}, {"message", in.Message}, {"run", in.Run}})
	for _, option := range in.Options {
		args = append(args, "--option="+option)
	}
	if in.Choice != 0 {
		args = append(args, "--choice="+strconv.Itoa(in.Choice))
	}
	return args
}

// statusInput posts where you are in a room, or without a task lists the
// room's latest status per nickname.
type statusInput struct {
	Task    string `json:"task" jsonschema:"What you are on, one line of at most 200 bytes"`
	State   string `json:"state" jsonschema:"working, blocked or done"`
	Channel string `json:"channel,omitempty" jsonschema:"The room the status is for; defaults to AIRC_CHANNEL"`
	Link    string `json:"link,omitempty" jsonschema:"Where the work is: an absolute http(s) URL of a PR, a branch or a document, up to 400 bytes"`
	Message string `json:"message,omitempty" jsonschema:"A note on the status, such as what is blocking you"`
	Run     string `json:"run,omitempty" jsonschema:"Run label kept on the post; defaults to AIRC_RUN"`
}

func (in statusInput) request() (irc.ChatRequest, error) {
	run, err := runLabel(in.Run)
	if err != nil {
		return irc.ChatRequest{}, err
	}
	channel := cmp.Or(in.Channel, os.Getenv("AIRC_CHANNEL"))
	room := channelName(channel)
	if !protocol.IsChannel(room) {
		return irc.ChatRequest{}, errors.New("a status is for a room: --channel ROOM")
	}
	if in.Task == "" {
		if in.State != "" || in.Link != "" || in.Message != "" {
			return irc.ChatRequest{}, errors.New("--state, --link and --message go with --task; without --task, status lists the room's statuses")
		}
		return irc.ChatRequest{Action: "statuses", Target: room}, nil
	}
	if !protocol.ValidStatusState(in.State) {
		return irc.ChatRequest{}, errors.New("--state is working, blocked or done")
	}
	if err := validLink(in.Link); err != nil {
		return irc.ChatRequest{}, err
	}
	note, err := messageText(in.Message, false)
	if err != nil {
		return irc.ChatRequest{}, err
	}
	return irc.ChatRequest{Action: "status", Target: room, Task: in.Task, State: in.State, Link: in.Link, Text: note, Run: run}, nil
}

func (in statusInput) args() []string {
	return appendFlags([]string{"status"}, [][2]string{{"channel", in.Channel}, {"task", in.Task}, {"state", in.State}, {"link", in.Link}, {"message", in.Message}, {"run", in.Run}})
}

// resolveInput closes a settled question.
type resolveInput struct {
	ID      string `json:"id" jsonschema:"ID of the question, or of any message in its thread"`
	Message string `json:"message,omitempty" jsonschema:"How it was settled; default \"Resolved\""`
}

func (in resolveInput) request() (irc.ChatRequest, error) {
	if !protocol.ValidMessageID(in.ID) {
		return irc.ChatRequest{}, errors.New("resolve needs a valid message ID")
	}
	note, err := messageText(in.Message, false)
	if err != nil {
		return irc.ChatRequest{}, err
	}
	return irc.ChatRequest{Action: "resolve", ID: in.ID, Text: note}, nil
}

func (in resolveInput) args() []string {
	return appendFlags([]string{"resolve", in.ID}, [][2]string{{"message", in.Message}})
}

// pollInput opens a poll in a room, votes in one, reads its results or
// closes it.
type pollInput struct {
	Step     string   `json:"step,omitempty" jsonschema:"open (the default), vote, results or close"`
	ID       string   `json:"id,omitempty" jsonschema:"The poll's ID, for every step but open"`
	Channel  string   `json:"channel,omitempty" jsonschema:"Open: the room to poll; defaults to AIRC_CHANNEL"`
	Question string   `json:"question,omitempty" jsonschema:"Open: the question"`
	Options  []string `json:"options,omitempty" jsonschema:"Open: 2-8 choices, numbered from 1"`
	Choice   int      `json:"choice,omitempty" jsonschema:"Vote: the chosen option's number"`
	Seconds  int      `json:"seconds,omitempty" jsonschema:"Open: how long the poll stays open; default 120"`
}

// pollSteps maps the step words to their CHAT actions.
var pollSteps = map[string]string{
	"vote":    "vote",
	"results": "results",
	"close":   "close-poll",
}

func (in pollInput) request() (irc.ChatRequest, error) {
	step := cmp.Or(in.Step, "open")
	if step != "open" && pollSteps[step] == "" {
		return irc.ChatRequest{}, fmt.Errorf("unknown poll step %q: use vote, results or close ID", step)
	}
	if step == "open" {
		if in.ID != "" || in.Choice != 0 {
			return irc.ChatRequest{}, errors.New("opening a poll takes no ID or choice; it creates one")
		}
		room := channelName(cmp.Or(in.Channel, os.Getenv("AIRC_CHANNEL")))
		if !protocol.IsChannel(room) {
			return irc.ChatRequest{}, errors.New("a poll is for a room: --channel ROOM")
		}
		if strings.TrimSpace(in.Question) == "" {
			return irc.ChatRequest{}, errors.New("--question is required")
		}
		if in.Seconds < 0 {
			return irc.ChatRequest{}, errors.New("a poll stays open for a positive number of seconds")
		}
		return irc.ChatRequest{Action: "poll", Target: room, Text: in.Question, Options: in.Options, Seconds: int64(cmp.Or(in.Seconds, 120))}, nil
	}
	if !protocol.ValidMessageID(in.ID) {
		return irc.ChatRequest{}, fmt.Errorf("poll %s needs the poll's ID", step)
	}
	if in.Channel != "" || in.Question != "" || len(in.Options) > 0 || in.Seconds != 0 {
		return irc.ChatRequest{}, errors.New("a poll step names only the poll's ID; it goes where the poll is")
	}
	if step == "vote" && in.Choice < 1 {
		return irc.ChatRequest{}, errors.New("vote needs --choice, an option's number from 1")
	}
	if step != "vote" && in.Choice != 0 {
		return irc.ChatRequest{}, errors.New("--choice applies to vote")
	}
	return irc.ChatRequest{Action: pollSteps[step], ID: in.ID, Choice: in.Choice}, nil
}

func (in pollInput) args() []string {
	switch in.Step {
	case "vote":
		return []string{"vote", in.ID, strconv.Itoa(in.Choice)}
	case "results":
		return []string{"poll-results", in.ID}
	case "close":
		return []string{"poll-close", in.ID}
	}
	args := appendFlags([]string{"poll"}, [][2]string{{"channel", in.Channel}, {"question", in.Question}})
	for _, option := range in.Options {
		args = append(args, "--option="+option)
	}
	if in.Seconds != 0 {
		args = append(args, "--for="+strconv.Itoa(in.Seconds)+"s")
	}
	return args
}

// pinInput shares a message as a room pin, takes it down, or lists a room's
// pins.
type pinInput struct {
	Step    string `json:"step,omitempty" jsonschema:"pin (the default), unpin or list"`
	ID      string `json:"id,omitempty" jsonschema:"Pin and unpin: the message's ID"`
	Channel string `json:"channel,omitempty" jsonschema:"List: the room whose pins to read; defaults to AIRC_CHANNEL"`
}

func (in pinInput) request() (irc.ChatRequest, error) {
	step := cmp.Or(in.Step, "pin")
	switch step {
	case "pin", "unpin":
		if !protocol.ValidMessageID(in.ID) {
			return irc.ChatRequest{}, fmt.Errorf("%s needs the message's ID", step)
		}
		if in.Channel != "" {
			return irc.ChatRequest{}, fmt.Errorf("%s names only the message's ID; it goes where the message is", step)
		}
		return irc.ChatRequest{Action: step, ID: in.ID}, nil
	case "list":
		if in.ID != "" {
			return irc.ChatRequest{}, errors.New("listing pins names a room, not a message")
		}
		room := channelName(cmp.Or(in.Channel, os.Getenv("AIRC_CHANNEL")))
		if !protocol.IsChannel(room) {
			return irc.ChatRequest{}, errors.New("pins are a room's: --channel ROOM")
		}
		return irc.ChatRequest{Action: "pins", Target: room}, nil
	}
	return irc.ChatRequest{}, fmt.Errorf("unknown pin step %q: use pin, unpin or list", step)
}

func (in pinInput) args() []string {
	if in.Step == "list" {
		return []string{"pins", cmp.Or(in.Channel, os.Getenv("AIRC_CHANNEL"))}
	}
	return []string{cmp.Or(in.Step, "pin"), in.ID}
}

// messageText is a post's text as the wire takes it: normalised and within
// the message limit; a blank one is an error when the action needs text.
func messageText(text string, required bool) (string, error) {
	text = irc.NormalizeMessage(text)
	if len(text) > 4096 {
		return "", errors.New("message exceeds 4096 bytes")
	}
	if strings.TrimSpace(text) == "" {
		if required {
			return "", errors.New("--message is required")
		}
		return "", nil
	}
	return text, nil
}

// flagText resolves a --message flag's value: - reads stdin.
func flagText(value string, stdin io.Reader) (string, error) {
	if value != "-" {
		return value, nil
	}
	return sendBody("", "", "-", stdin)
}

// validLink accepts no link or an absolute http(s) URL within the limit.
func validLink(link string) error {
	if link != "" && !protocol.ValidLink(link) {
		return fmt.Errorf("--link needs an absolute http(s) URL of at most %d bytes", protocol.MaxLinkBytes)
	}
	return nil
}

// appendFlags appends --name=value for each pair that has a value.
func appendFlags(args []string, pairs [][2]string) []string {
	for _, pair := range pairs {
		if pair[1] != "" {
			args = append(args, "--"+pair[0]+"="+pair[1])
		}
	}
	return args
}

// splitPipes splits a slash command's "a | b | c" into its trimmed parts.
func splitPipes(text string) []string {
	parts := strings.Split(text, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}
