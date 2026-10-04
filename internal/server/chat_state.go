package server

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

type roomSettings struct {
	SlowSeconds  int64 `json:"slow_seconds,omitempty"`
	HistoryLimit int   `json:"history_limit,omitempty"`
}

type chatState struct {
	Operators map[string][]string     `json:"operators,omitempty"`
	Pins      map[string][]Message    `json:"pins"`
	Rooms     map[string]roomSettings `json:"rooms"`
	Polls     map[string]poll         `json:"polls"`
}

func newChatState() chatState {
	return chatState{Operators: map[string][]string{}, Pins: map[string][]Message{}, Rooms: map[string]roomSettings{}, Polls: map[string]poll{}}
}

func (s *Server) RestoreChat(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil || s.chatAt != "" {
		return errors.New("configure chat state once before Serve")
	}
	loaded := newChatState()
	if err := readState(path, &loaded); err != nil {
		return err
	}
	if loaded.Pins == nil || loaded.Rooms == nil || loaded.Polls == nil || len(loaded.Pins) > 128 || len(loaded.Rooms) > 128 || len(loaded.Polls) > 128 {
		return errors.New("invalid chat snapshot")
	}
	if loaded.Operators == nil {
		loaded.Operators = map[string][]string{}
	}
	if len(loaded.Operators) > 128 {
		return errors.New("too many operator rooms")
	}
	for channel, ids := range loaded.Operators {
		if !validChannel(channel) || len(ids) > 64 {
			return errors.New("invalid operator room")
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if !protocol.ValidMessageID(id) || seen[id] {
				return errors.New("invalid operator account")
			}
			seen[id] = true
		}
	}
	count := 0
	for channel, pins := range loaded.Pins {
		count += len(pins)
		if !validChannel(channel) || len(pins) > 5 {
			return errors.New("invalid pinned room")
		}
		ids := map[string]bool{}
		for _, m := range pins {
			if m.Target != channel || !protocol.ValidMessageID(m.ID) || !validNick(m.From) || len(m.Body) > 4096 || ids[m.ID] {
				return errors.New("invalid pinned message")
			}
			ids[m.ID] = true
		}
	}
	if count > 128 {
		return errors.New("too many pinned messages")
	}
	for channel, r := range loaded.Rooms {
		if !validChannel(channel) || r.SlowSeconds < 0 || r.SlowSeconds > 3600 || r.HistoryLimit < 0 || r.HistoryLimit > s.cfg.HistoryLimit {
			return errors.New("invalid room settings")
		}
	}
	for id, p := range loaded.Polls {
		if err := validPoll(id, p); err != nil {
			return err
		}
	}
	s.chat, s.chatAt = loaded, path
	s.history.quotas = loaded.Rooms
	return nil
}

func (s *Server) saveChatLocked(next chatState) error {
	if err := writeState(s.chatAt, next, s.cfg.Sync); err != nil {
		return err
	}
	s.chat = next
	s.history.quotas = next.Rooms
	return nil
}

func (s *Server) copyChat() chatState {
	next := newChatState()
	maps.Copy(next.Operators, s.chat.Operators)
	maps.Copy(next.Pins, s.chat.Pins)
	maps.Copy(next.Rooms, s.chat.Rooms)
	maps.Copy(next.Polls, s.chat.Polls)
	return next
}

func actorKey(client *session) string {
	if client.accountID != "" {
		return client.accountID
	}
	return "guest:" + nickKey(client.client.Nick)
}

func (s *Server) slowAllowedLocked(client *session, target string) bool {
	r := s.chat.Rooms[target]
	if r.SlowSeconds == 0 {
		return true
	}
	now := s.now()
	key := target + "\n" + actorKey(client)
	until := s.slowPosts[key]
	if now.Before(until) {
		s.numericLocked(client, "486", []string{target}, fmt.Sprintf("Slow mode: retry after %d seconds", int(math.Ceil(until.Sub(now).Seconds()))))
		return false
	}
	for key, deadline := range s.slowPosts {
		if !now.Before(deadline) {
			delete(s.slowPosts, key)
		}
	}
	if len(s.slowPosts) >= 4096 {
		s.numericLocked(client, "437", nil, "Slow-mode state full; retry later")
		return false
	}
	s.slowPosts[key] = now.Add(time.Duration(r.SlowSeconds) * time.Second)
	return true
}

func (s *Server) pinsLocked(client *session, request protocol.ChatRequest) error {
	if request.Action == "pins" {
		if !validChannel(request.Target) {
			return errors.New("pins requires a channel")
		}
		for _, message := range s.chat.Pins[request.Target] {
			m := s.annotated(message)
			metadata := messageMetadata(m)
			s.chatEntryLocked(client, protocol.ChatEntry{Action: "pin", Target: request.Target, ID: m.ID, From: m.From, Message: &metadata})
		}
		return nil
	}
	m, found := s.history.message(request.ID)
	if !found && request.Action == "unpin" {
		for _, pins := range s.chat.Pins {
			for _, p := range pins {
				if p.ID == request.ID {
					m, found = p, true
				}
			}
		}
	}
	if !found || !isChannelName(m.Target) {
		return errors.New("pin/unpin requires a retained room message or existing pin")
	}
	if !s.postAllowedLocked(client, m.Target) {
		return errChatDenied
	}
	next := s.copyChat()
	pins := append([]Message{}, next.Pins[m.Target]...)
	index := -1
	for i, pin := range pins {
		if pin.ID == m.ID {
			index = i
		}
	}
	if request.Action == "pin" && index == -1 {
		count := 0
		for _, pins := range next.Pins {
			count += len(pins)
		}
		if count >= 128 {
			return errors.New("global pin limit reached (128)")
		}
		if len(pins) >= 5 || len(next.Pins) >= 128 && len(pins) == 0 {
			return errors.New("pin limit reached (5 per room, 128 rooms)")
		}
		pins = append(pins, m)
	} else if request.Action == "unpin" && index >= 0 {
		pins = append(pins[:index], pins[index+1:]...)
	}
	if len(pins) == 0 {
		delete(next.Pins, m.Target)
	} else {
		next.Pins[m.Target] = pins
	}
	if err := s.saveChatLocked(next); err != nil {
		return err
	}
	s.chatEntryLocked(client, protocol.ChatEntry{Action: request.Action, Target: m.Target, ID: m.ID})
	return nil
}
