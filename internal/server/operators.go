package server

import (
	"slices"
	"strings"

	"github.com/Someblueman/airc/internal/protocol"
)

func (s *Server) channelOperator(c *session, channel string) bool {
	return c.accountID != "" && slices.Contains(s.chat.Operators[channel], c.accountID) && s.accountExistsLocked(c.accountID)
}

// accountExistsLocked is false once an admin has deleted the account, which
// ends any operator grant it held without rewriting the chat snapshot.
func (s *Server) accountExistsLocked(id string) bool {
	for _, a := range s.accounts {
		if a.ID == id {
			return true
		}
	}
	return false
}

func (s *Server) modeLocked(c *session, cmd protocol.Command) {
	channel, _ := cmd.Param(0)
	mode, _ := cmd.Param(1)
	nick, _ := cmd.Param(2)
	if !validChannel(channel) {
		s.numericLocked(c, "403", []string{channel}, "No such channel")
		return
	}
	if mode == "" {
		// AIRC lists durable grants, including offline operators, alongside MODE.
		for _, id := range s.chat.Operators[channel] {
			for _, a := range s.accounts {
				if a.ID == id {
					s.numericLocked(c, "783", []string{channel, a.Nick, a.ID}, "Channel operator")
				}
			}
		}
		s.numericLocked(c, "324", []string{channel, "+"}, "End of channel operators")
		return
	}
	if mode != "+o" && mode != "-o" {
		s.numericLocked(c, "472", []string{mode}, "Only +o and -o are supported")
		return
	}
	if !c.admin && !s.channelOperator(c, channel) {
		s.numericLocked(c, "482", []string{channel}, "Channel operator privileges needed; an admin grants the first operator")
		return
	}
	if !s.postAllowedLocked(c, channel) {
		return
	}
	a, ok := s.accounts[nickKey(nick)]
	if !ok {
		s.numericLocked(c, "401", []string{nick}, "Channel operators need a registered account")
		return
	}
	next := s.copyChat()
	ids := slices.DeleteFunc(slices.Clone(next.Operators[channel]), func(id string) bool { return !s.accountExistsLocked(id) })
	index := slices.Index(ids, a.ID)
	if mode == "+o" && index < 0 {
		if len(ids) >= 64 || len(ids) == 0 && len(next.Operators) >= 128 {
			s.numericLocked(c, "437", nil, "Channel operator limit reached")
			return
		}
		ids = append(ids, a.ID)
	} else if mode == "-o" && index >= 0 {
		ids = slices.Delete(ids, index, index+1)
	}
	if len(ids) == 0 {
		delete(next.Operators, channel)
	} else {
		next.Operators[channel] = ids
	}
	if err := s.saveChatLocked(next); err != nil {
		s.numericLocked(c, "437", nil, "Channel operators were not saved")
		return
	}
	line := protocol.Format(c.client.Nick+"!"+c.client.Username+"@localhost", "MODE", []string{channel, mode, a.Nick}, "")
	s.broadcastChannelLocked(channel, line)
	if _, joined := c.channels[channel]; !joined {
		c.enqueue(line)
	}
}

func (s *Server) kickLocked(c *session, cmd protocol.Command) {
	channel, _ := cmd.Param(0)
	nick, _ := cmd.Param(1)
	if !validChannel(channel) || !validNick(nick) || !protocol.BriefText(cmd.Trailing, 400) {
		s.numericLocked(c, "461", []string{"KICK"}, "KICK requires a channel, nickname and optional reason up to 400 bytes")
		return
	}
	if !c.admin && !s.channelOperator(c, channel) {
		s.numericLocked(c, "482", []string{channel}, "Channel operator privileges needed")
		return
	}
	if !s.postAllowedLocked(c, channel) {
		return
	}
	target := s.liveNickLocked(nick)
	if target == nil {
		s.numericLocked(c, "401", []string{nick}, "No such nick")
		return
	}
	if _, joined := target.channels[channel]; !joined {
		s.numericLocked(c, "441", []string{nick, channel}, "User is not in that channel")
		return
	}
	line := protocol.Format(c.client.Nick+"!"+c.client.Username+"@localhost", "KICK", []string{channel, target.client.Nick}, strings.TrimSpace(cmd.Trailing))
	s.broadcastChannelLocked(channel, line)
	if _, joined := c.channels[channel]; !joined {
		c.enqueue(line)
	}
	delete(target.channels, channel)
	delete(s.channels[channel], target.client.ID)
	if len(s.channels[channel]) == 0 {
		delete(s.channels, channel)
	}
}
