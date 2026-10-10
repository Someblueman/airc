package server

import (
	"fmt"

	"github.com/Someblueman/airc/internal/protocol"
)

func (s *Server) joinLocked(client *session, command protocol.Command) {
	if client.ephemeral {
		s.numericLocked(client, "484", nil, "Ephemeral sessions cannot join channels")
		return
	}
	list, ok := command.Param(0)
	if !ok {
		s.numericLocked(client, "461", []string{"JOIN"}, "Not enough parameters")
		return
	}
	if list == "0" {
		for channel := range client.channels {
			s.partOneLocked(client, channel, "")
		}
		return
	}
	requested, ok := s.targetListLocked(client, list)
	if !ok {
		return
	}
	for _, channel := range requested {
		if !validChannel(channel) {
			s.numericLocked(client, "403", []string{channel}, "No such channel")
			continue
		}
		if !s.channelAllowedLocked(client, channel) {
			continue
		}
		if _, joined := client.channels[channel]; joined {
			continue
		}
		if len(client.channels) >= maxChannelsPerClient {
			s.numericLocked(client, "405", []string{channel}, "You have joined too many channels")
			continue
		}
		if s.channels[channel] == nil {
			if len(s.channels) >= maxTotalChannels {
				s.numericLocked(client, "437", []string{channel}, "Server channel limit reached")
				continue
			}
			s.channels[channel] = make(map[string]*session)
		}
		s.channels[channel][client.client.ID] = client
		client.channels[channel] = struct{}{}
		s.broadcastChannelLocked(channel, fmt.Sprintf(":%s!%s@localhost JOIN %s\r\n", client.client.Nick, client.client.Username, channel))
		s.logger.Info("channel_join", "nick", client.client.Nick, "channel", channel)
		s.namesOneLocked(client, channel)
		if _, ok := s.topics[channel]; ok {
			s.topicReplyLocked(client, channel)
		}
	}
}

func (s *Server) partLocked(client *session, command protocol.Command) {
	list, ok := command.Param(0)
	if !ok {
		s.numericLocked(client, "461", []string{"PART"}, "Not enough parameters")
		return
	}
	channels, ok := s.targetListLocked(client, list)
	if !ok {
		return
	}
	for _, channel := range channels {
		s.partOneLocked(client, channel, command.Trailing)
	}
}

func (s *Server) partOneLocked(client *session, channel, reason string) {
	if _, joined := client.channels[channel]; !joined {
		s.numericLocked(client, "442", []string{channel}, "You're not on that channel")
		return
	}
	s.broadcastChannelLocked(channel, fmt.Sprintf(":%s!%s@localhost PART %s :%s\r\n", client.client.Nick, client.client.Username, channel, reason))
	dropMember(s.channels, channel, client.client.ID)
	delete(client.channels, channel)
	s.logger.Info("channel_part", "nick", client.client.Nick, "channel", channel)
}
