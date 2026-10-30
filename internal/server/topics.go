package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/atomicfile"
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
	if !s.postAllowedLocked(client, channel) {
		return
	}
	text := command.Trailing
	if !protocol.BriefText(text, maxTopicBytes) {
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
		s.topics[channel] = topic{Text: text, SetBy: client.client.Nick, SetAt: s.now().UTC()}
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
	listingFor := func(name string) *channelListing {
		if known[name] == nil {
			known[name] = &channelListing{Name: name}
		}
		return known[name]
	}
	for name, members := range s.channels {
		listingFor(name).Members = len(members)
	}
	for name, t := range s.topics {
		listingFor(name).Topic = t.Text
	}
	for target, count := range s.history.counts {
		if !protocol.IsChannel(target) {
			continue
		}
		listing := listingFor(target)
		listing.Messages = count
		s.history.backward(s.history.selectTarget(target), func(_ uint64, e *entry) bool {
			listing.LastActivity = e.Timestamp
			return false
		})
	}
	names := slices.Sorted(maps.Keys(known))
	for _, name := range names {
		encoded, _ := json.Marshal(known[name])
		s.numericLocked(client, "768", nil, string(encoded))
	}
	s.numericLocked(client, "769", nil, "End of channels list")
}

// topicState is the channel headers and the file they are saved to. Every
// field is read and written with s.mu held; saves run under s.mu with
// env.soft, and a failed save is logged, not returned.
type topicState struct {
	topics   map[string]topic // channel headers; independent of who is connected
	topicsAt string           // file the topics are saved to, if any
	env      *storeEnv
}

// topicsSnapshot reads the headers bounded and writes them indented, as
// they always were; a file that does not decode is set aside (see load).
var topicsSnapshot = snapshot{limit: 4 << 20, tooLarge: "topics file exceeds 4 MiB", indent: true}

// RestoreTopics loads channel headers from path and saves every later change
// to it, so headers survive a restart. Call it before Serve.
func (s *Server) RestoreTopics(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.topicsAt != "" {
		return errors.New("topics file is already open")
	}
	return s.topicState.load(path)
}

func (st *topicState) load(path string) error {
	data, found, err := topicsSnapshot.bytes(path)
	if err != nil {
		return fmt.Errorf("read topics file: %w", err)
	}
	if found {
		var loaded map[string]topic
		if err := json.Unmarshal(data, &loaded); err != nil {
			// Headers are not worth refusing to start over: keep the evidence, start empty.
			bad, moveErr := atomicfile.SetAside(path)
			if moveErr != nil {
				return fmt.Errorf("topics file %s is corrupt: %w", path, err)
			}
			st.env.logger.Error("topics_file_corrupt", "path", path, "moved_to", bad, "error", err.Error())
		}
		for channel, t := range loaded {
			if validChannel(channel) && t.Text != "" && len(st.topics) < maxTotalChannels {
				st.topics[channel] = t
			}
		}
	}
	st.topicsAt = path
	st.env.logger.Info("topics_restored", "path", path, "topics", len(st.topics))
	return nil
}

func (st *topicState) close() error { return nil }

func (st *topicState) saveTopicsLocked() {
	if err := topicsSnapshot.write(st.topicsAt, st.topics, st.env.soft); err != nil {
		st.env.logger.Error("topics_write_failed", "error", err.Error())
	}
}
