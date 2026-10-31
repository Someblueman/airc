package server

import (
	"slices"

	"github.com/Someblueman/airc/internal/protocol"
)

// resolveGroupMentionsLocked records who a room message's group tags reach
// (protocol.GroupTags) in its metadata, from the directory's kinds and
// the room's live members as they are when it is posted: "@humans" and
// "@agents" are the nicknames whose profile kind says so, "@here" everyone
// joined to or observing the room with a live session, a guest included. The
// nicknames are lower-cased, sorted and never the sender's, the first of
// them that fit protocol.MaxGroupMentions and MaxGroupMentionBytes; a group
// with nobody in it resolves to nobody, and the message posts either way.
// From here on they travel with
// the message (the +airc/chat tag, history, the archive and check) and
// count as its mentions, so the inbox index, watchers of @nick, wake notices
// and clients need nothing beyond what a nickname tag already gives them.
// In a direct message the words are plain text.
func (s *Server) resolveGroupMentionsLocked(message *Message) {
	message.GroupMentions = nil
	if !protocol.IsChannel(message.Target) || message.Reaction != "" {
		return
	}
	groups := protocol.GroupTags(message.Body)
	if len(groups) == 0 {
		return
	}
	sender := nickKey(message.From)
	members := map[string]bool{}
	add := func(nick string) {
		if key := nickKey(nick); key != "" && key != sender {
			members[key] = true
		}
	}
	for _, group := range groups {
		switch group {
		case protocol.GroupHumans, protocol.GroupAgents:
			kind := "human"
			if group == protocol.GroupAgents {
				kind = "agent"
			}
			for key, card := range s.directory { // keyed by nickKey
				if card.Kind == kind {
					add(key)
				}
			}
		case protocol.GroupHere:
			for _, present := range []map[string]*session{s.channels[message.Target], s.watchers[message.Target]} {
				for _, member := range present {
					if member.registered && !member.reclaimed {
						add(member.client.Nick)
					}
				}
			}
		}
	}
	if len(members) == 0 {
		return
	}
	resolved := make([]string, 0, len(members))
	for key := range members {
		resolved = append(resolved, key)
	}
	slices.Sort(resolved)
	kept, bytes := 0, 0
	for kept < len(resolved) && kept < protocol.MaxGroupMentions && bytes+len(resolved[kept])+3 <= protocol.MaxGroupMentionBytes {
		bytes += len(resolved[kept]) + 3
		kept++
	}
	if kept == 0 {
		return
	}
	message.GroupMentions = slices.Clip(resolved[:kept])
}
