package server

import (
	"strings"

	"github.com/Someblueman/airc/internal/protocol"
)

func (s *Server) observeLocked(client *session, command protocol.Command) {
	list, ok := command.Param(0)
	if !ok {
		s.numericLocked(client, "461", []string{"OBSERVE"}, "Not enough parameters")
		return
	}
	if len(client.channels) > 0 {
		s.numericLocked(client, "484", nil, "Leave joined channels before observing")
		return
	}
	requested, ok := s.targetListLocked(client, list)
	if !ok {
		return
	}
	if client.ephemeral {
		// A one-shot agent's wait; a persistent session observing becomes
		// an observer, a watching human rather than an agent at work.
		s.noteActivityLocked(client.client.Nick, requested...)
	}
	for _, target := range requested {
		// "@nick" observes the direct messages addressed to a nickname.
		key := target
		switch {
		case target == protocol.AllDirectMessages:
		case strings.HasPrefix(target, "thread:") || strings.HasPrefix(target, "replies:"):
			var err error
			key, err = s.conversationLocked(target)
			if err != nil {
				s.conversationErrorLocked(client, []string{target}, err)
				continue
			}
		case strings.HasPrefix(target, "@") && protocol.ValidNick(target[1:]):
			key = "@" + nickKey(target[1:])
		case !validChannel(target):
			s.numericLocked(client, "403", []string{target}, "No such channel")
			continue
		}
		if channel := s.conversationChannelLocked(key); protocol.IsChannel(channel) && !s.channelAllowedLocked(client, channel) {
			continue
		}
		if _, watching := client.watching[key]; watching {
			s.numericLocked(client, "765", []string{target}, "Now observing")
			continue
		}
		if len(client.watching) >= maxChannelsPerClient {
			s.numericLocked(client, "405", []string{target}, "You are observing too many channels")
			continue
		}
		if s.watchers[key] == nil {
			if len(s.watchers) >= maxTotalChannels {
				s.numericLocked(client, "405", []string{target}, "Server observation limit reached")
				continue
			}
			s.watchers[key] = make(map[string]*session)
		}
		if !client.ephemeral {
			// Ephemeral sessions stay fully usable (history, send) while observing.
			if !client.observer {
				s.monitorChangedLocked(client, false)
			}
			client.observer = true
		}
		client.watching[key] = struct{}{}
		s.watchers[key][client.client.ID] = client
		s.logger.Info("channel_observed", "nick", client.client.Nick, "channel", key)
		s.numericLocked(client, "765", []string{target}, "Now observing")
	}
}

func (s *Server) unobserveLocked(client *session, command protocol.Command) {
	list, ok := command.Param(0)
	targets := strings.Split(list, ",")
	if !ok || len(targets) > maxTargets {
		s.numericLocked(client, "461", nil, "UNOBSERVE requires up to 16 targets")
		return
	}
	for _, target := range targets {
		key := target
		if strings.HasPrefix(target, "@") {
			key = "@" + nickKey(target[1:])
		}
		// Thread subscriptions are stored under the canonical root, which remains
		// removable even after the thread has left history.
		delete(client.watching, key)
		dropMember(s.watchers, key, client.client.ID)
		s.numericLocked(client, "781", []string{target}, "No longer observing")
	}
}
