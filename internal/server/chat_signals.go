package server

import (
	"errors"
	"maps"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

func (s *Server) signalLocked(client *session, r protocol.ChatRequest) error {
	now := s.now().UTC()
	for key, entry := range s.signals {
		if !now.Before(entry.ExpiresAt) {
			delete(s.signals, key)
			delete(s.signalTimes, key)
		}
	}
	target := r.Target
	var parent Message
	if r.Action == "prepare" || r.Action == "cancel" || r.Action == "waiting" {
		var found bool
		parent, found = s.history.message(r.ID)
		if !found {
			return errors.New("question is no longer retained")
		}
		target = parent.Target
		if r.Action == "waiting" {
			for _, entry := range s.signals {
				if entry.ID == r.ID {
					s.chatEntryLocked(client, entry)
				}
			}
			return nil
		}
		if !isChannelName(target) && !strings.EqualFold(client.client.Nick, parent.From) && !strings.EqualFold(client.client.Nick, parent.Target) {
			return errors.New("only DM participants can signal a reply")
		}
	} else if !validChannel(target) && !validNick(target) {
		return errors.New("typing/thinking requires a room or nickname")
	}
	if !s.postAllowedLocked(client, target) {
		return errChatDenied
	}
	key := target + ":" + r.ID + ":" + actorKey(client)
	if r.Action == "cancel" {
		delete(s.signals, key)
		delete(s.signalTimes, key)
		entry := protocol.ChatEntry{Action: "cancel", Target: target, ID: r.ID, From: client.client.Nick, ExpiresAt: now}
		s.chatEntryLocked(client, entry)
		s.broadcastSignalLocked(client, entry, parent)
		return nil
	}
	limit := int64(15)
	if r.Action == "prepare" {
		limit = 900
	}
	if r.Seconds < 1 || r.Seconds > limit || !protocol.BriefText(r.Text, 240) {
		return errors.New("reply-coming TTL is 1-900s; typing/thinking TTL 1-15s; note at most 240 bytes")
	}
	if _, exists := s.signals[key]; !exists && len(s.signals) >= 1024 {
		return errors.New("activity signal limit reached")
	}
	entry := protocol.ChatEntry{Action: r.Action, Target: target, ID: r.ID, From: client.client.Nick, AccountID: client.accountID, Text: r.Text, ExpiresAt: now.Add(time.Duration(r.Seconds) * time.Second)}
	if last := s.signalTimes[key]; !last.IsZero() && now.Sub(last) < 2*time.Second {
		s.chatEntryLocked(client, s.signals[key])
		return nil
	}
	s.signals[key], s.signalTimes[key] = entry, now
	s.chatEntryLocked(client, entry)
	s.broadcastSignalLocked(client, entry, parent)
	return nil
}

func (s *Server) broadcastSignalLocked(client *session, entry protocol.ChatEntry, parent Message) {
	target := entry.Target
	readers := map[string]*session{}
	add := func(group map[string]*session) {
		maps.Copy(readers, group)
	}
	if isChannelName(target) {
		add(s.channels[target])
		add(s.watchers[target])
	} else {
		add(s.watchers["@"+nickKey(target)])
		if c := s.liveNickLocked(target); c != nil {
			readers[c.client.ID] = c
		}
	}
	if parent.ID != "" {
		add(s.watchers["@"+nickKey(parent.From)])
		if c := s.liveNickLocked(parent.From); c != nil {
			readers[c.client.ID] = c
		}
		root := parent.ThreadID
		if root == "" {
			root = parent.ID
		}
		add(s.watchers["thread:"+root])
		add(s.watchers["replies:"+parent.ID])
	}
	for _, reader := range readers {
		if reader == client {
			continue
		}
		if isChannelName(target) {
			if _, banned := s.restrictionLocked("ban", reader.client.Nick, target); banned {
				continue
			}
		}
		data := chatJSON(entry)
		s.numericLocked(reader, "780", nil, data)
	}
}
