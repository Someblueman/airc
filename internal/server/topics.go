package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/protocol"
)

// maxTopicBytes bounds a channel header, as IRC servers bound a topic.
const maxTopicBytes = 400

// topic is a channel's header line. Channels here are virtual (one-shot agents
// never join), so topics are kept for any channel name, not just live ones.
type topic struct {
	Text  string    `json:"text"`
	SetBy string    `json:"set_by"`
	SetAt time.Time `json:"set_at"`
}

// topicLocked serves TOPIC <channel> [:text]. Without text it reports the
// header; with text it sets it for everyone who reads the channel, and empty
// text clears it. As elsewhere on this local bus, anyone may set it.
func (s *Server) topicLocked(client *session, command protocol.Command) {
	channel, ok := command.Param(0)
	if !ok {
		s.numericLocked(client, "461", []string{"TOPIC"}, "Not enough parameters")
		return
	}
	if !validChannel(channel) {
		s.numericLocked(client, "403", []string{channel}, "No such channel")
		return
	}
	if !command.HasTrailing {
		s.topicReplyLocked(client, channel)
		return
	}
	text := command.Trailing
	if len(text) > maxTopicBytes || !utf8.ValidString(text) || strings.IndexFunc(text, unicode.IsControl) >= 0 {
		s.numericLocked(client, "417", nil, fmt.Sprintf("Topic must be valid text of at most %d bytes", maxTopicBytes))
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		delete(s.topics, channel)
	} else {
		if _, exists := s.topics[channel]; !exists && len(s.topics) >= maxTotalChannels {
			s.numericLocked(client, "437", []string{channel}, "Server topic limit reached")
			return
		}
		s.topics[channel] = topic{Text: text, SetBy: client.client.Nick, SetAt: time.Now().UTC()}
	}
	s.saveTopicsLocked()
	s.broadcastChannelLocked(channel, fmt.Sprintf(":%s!%s@localhost TOPIC %s :%s\r\n", client.client.Nick, client.client.Username, channel, text))
	s.topicReplyLocked(client, channel) // confirms to a setter that is not in the channel
	s.logger.Info("topic_set", "nick", client.client.Nick, "channel", channel)
}

func (s *Server) topicReplyLocked(client *session, channel string) {
	t, ok := s.topics[channel]
	if !ok {
		s.numericLocked(client, "331", []string{channel}, "No topic is set")
		return
	}
	s.numericLocked(client, "332", []string{channel}, t.Text)
	s.numericLocked(client, "333", []string{channel, t.SetBy, strconv.FormatInt(t.SetAt.Unix(), 10)}, "")
}

type channelListing struct {
	Name         string    `json:"name"`
	Members      int       `json:"members"`
	Messages     int       `json:"messages"`
	LastActivity time.Time `json:"last_activity,omitzero"`
	Topic        string    `json:"topic,omitempty"`
}

// channelsLocked lists every channel the server knows about: with connected
// members, with retained history, or with a header. LIST only covers the first.
func (s *Server) channelsLocked(client *session) {
	known := map[string]*channelListing{}
	entry := func(name string) *channelListing {
		if known[name] == nil {
			known[name] = &channelListing{Name: name}
		}
		return known[name]
	}
	for name, members := range s.channels {
		entry(name).Members = len(members)
	}
	for name, t := range s.topics {
		entry(name).Topic = t.Text
	}
	for i := 0; i < s.history.size; i++ {
		message := s.history.at(i)
		if !isChannelName(message.Target) {
			continue
		}
		listing := entry(message.Target)
		listing.Messages++
		if message.Timestamp.After(listing.LastActivity) {
			listing.LastActivity = message.Timestamp
		}
	}
	names := make([]string, 0, len(known))
	for name := range known {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		encoded, _ := json.Marshal(known[name])
		s.numericLocked(client, "768", nil, string(encoded))
	}
	s.numericLocked(client, "769", nil, "End of channels list")
}

// RestoreTopics loads channel headers from path and saves every later change
// to it, so headers survive a restart. Call it before Serve.
func (s *Server) RestoreTopics(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.topicsAt != "" {
		return errors.New("topics file is already open")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create topics directory: %w", err)
	}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return fmt.Errorf("read topics file: %w", err)
	default:
		var loaded map[string]topic
		if err := json.Unmarshal(data, &loaded); err != nil {
			return fmt.Errorf("topics file %s is corrupt: %w", path, err)
		}
		for channel, t := range loaded {
			if validChannel(channel) && t.Text != "" && len(s.topics) < maxTotalChannels {
				s.topics[channel] = t
			}
		}
	}
	s.topicsAt = path
	s.logger.Info("topics_restored", "path", path, "topics", len(s.topics))
	return nil
}

func (s *Server) saveTopicsLocked() {
	if s.topicsAt == "" {
		return
	}
	data, err := json.MarshalIndent(s.topics, "", "  ")
	if err == nil {
		var temp *os.File
		if temp, err = os.CreateTemp(filepath.Dir(s.topicsAt), ".airc-topics-*"); err == nil {
			_, err = temp.Write(append(data, '\n'))
			if closeErr := temp.Close(); err == nil {
				err = closeErr
			}
			if err == nil {
				if err = os.Chmod(temp.Name(), 0o600); err == nil {
					err = os.Rename(temp.Name(), s.topicsAt)
				}
			}
			if err != nil {
				_ = os.Remove(temp.Name())
			}
		}
	}
	if err != nil {
		s.logger.Error("topics_write_failed", "error", err.Error())
	}
}
