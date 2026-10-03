package server

import (
	"errors"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

type poll struct {
	Message Message        `json:"message"`
	Votes   map[string]int `json:"votes"`
	Closed  bool           `json:"closed"`
}

func validPoll(id string, p poll) error {
	if id != p.Message.ID || !protocol.ValidMessageID(id) || !validChannel(p.Message.Target) || !validNick(p.Message.From) || p.Message.Kind != "poll" || !protocol.BriefText(p.Message.Body, 1000) || len(p.Message.PollOptions) < 2 || len(p.Message.PollOptions) > 8 || p.Votes == nil || len(p.Votes) > 256 || p.Message.PollClosesAt.IsZero() {
		return errors.New("invalid saved poll")
	}
	for _, option := range p.Message.PollOptions {
		if option == "" || !protocol.BriefText(option, 80) {
			return errors.New("invalid poll option")
		}
	}
	for who, choice := range p.Votes {
		if len(who) > 64 || choice < 1 || choice > len(p.Message.PollOptions) {
			return errors.New("invalid saved vote")
		}
	}
	return nil
}

func pollEntry(p poll, now time.Time) protocol.ChatEntry {
	votes := make([]int, len(p.Message.PollOptions))
	for _, choice := range p.Votes {
		votes[choice-1]++
	}
	return protocol.ChatEntry{Action: "results", Target: p.Message.Target, ID: p.Message.ID, Text: p.Message.Body, From: p.Message.From, AccountID: p.Message.AccountID, Options: p.Message.PollOptions, Votes: votes, Closed: p.Closed || !now.Before(p.Message.PollClosesAt), ExpiresAt: p.Message.PollClosesAt}
}

func (s *Server) pollEntryLocked(client *session, p poll) {
	s.chatEntryLocked(client, pollEntry(p, s.now()))
}

func (s *Server) pollLocked(client *session, r protocol.ChatRequest) error {
	next := s.copyChat()
	for id, old := range next.Polls {
		if s.now().After(old.Message.PollClosesAt.Add(7 * 24 * time.Hour)) {
			delete(next.Polls, id)
		}
	}
	if r.Action == "poll" {
		if !validChannel(r.Target) || !protocol.BriefText(r.Text, 1000) || len(r.Text) > s.cfg.MaxMessageSize || strings.TrimSpace(r.Text) == "" || len(r.Options) < 2 || len(r.Options) > 8 || r.Seconds < 1 || r.Seconds > 7*24*3600 {
			return errors.New("poll needs a room, question up to 1000 bytes, 2-8 options and duration 1s-168h")
		}
		seen := map[string]bool{}
		for _, option := range r.Options {
			if option == "" || !protocol.BriefText(option, 80) || seen[option] {
				return errors.New("poll choices must be distinct nonempty text up to 80 bytes")
			}
			seen[option] = true
		}
		if !client.ephemeral {
			if _, joined := client.channels[r.Target]; !joined {
				return errors.New("join the room before posting")
			}
		}
		if !s.postAllowedLocked(client, r.Target) || !s.slowAllowedLocked(client, r.Target) {
			return errChatDenied
		}
		if len(next.Polls) >= 128 {
			return errors.New("poll limit reached; closed polls are retained for seven days after expiry")
		}
		m := s.newMessage(client.client.Nick, r.Target, r.Text, nil)
		m.Kind, m.AccountID, m.PollOptions, m.PollClosesAt = "poll", client.accountID, r.Options, s.now().UTC().Add(time.Duration(r.Seconds)*time.Second)
		p := poll{Message: m, Votes: map[string]int{}}
		next.Polls[m.ID] = p
		if err := s.saveChatLocked(next); err != nil {
			return err
		}
		mentions := s.recordLocked(&m)
		s.broadcastMessageLocked(m, client.client.Username, mentions)
		s.receiptLocked(client, m, false)
		s.pollEntryLocked(client, p)
		return nil
	}
	p, found := next.Polls[r.ID]
	if !found {
		return errors.New("poll is not retained")
	}
	if r.Action == "results" {
		s.pollEntryLocked(client, p)
		return nil
	}
	if !s.postAllowedLocked(client, p.Message.Target) {
		return errChatDenied
	}
	if r.Action == "close-poll" {
		if !ownsMessage(client, p.Message) {
			return errors.New("only the author or an admin may close this poll")
		}
		p.Closed = true
	} else {
		if p.Closed || !s.now().Before(p.Message.PollClosesAt) {
			return errors.New("poll is closed")
		}
		if r.Choice < 1 || r.Choice > len(p.Message.PollOptions) {
			return errors.New("vote requires a numbered poll choice")
		}
		key := actorKey(client)
		if _, exists := p.Votes[key]; !exists && len(p.Votes) >= 256 {
			return errors.New("poll voter limit reached")
		}
		votes := map[string]int{}
		for who, choice := range p.Votes {
			votes[who] = choice
		}
		votes[key] = r.Choice
		p.Votes = votes
	}
	next.Polls[r.ID] = p
	for id, old := range next.Polls {
		if s.now().After(old.Message.PollClosesAt.Add(7 * 24 * time.Hour)) {
			delete(next.Polls, id)
		}
	}
	if err := s.saveChatLocked(next); err != nil {
		return err
	}
	s.pollEntryLocked(client, p)
	s.broadcastSignalLocked(client, pollEntry(p, s.now()), p.Message)
	return nil
}
