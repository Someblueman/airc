package server

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/Someblueman/airc/internal/protocol"
)

// Encode only admitted records plus the first rejected candidate in each group.
// Stage the bounded response before enqueueing so a protected-record failure
// cannot leave a partial context on the connection. Zero preserves legacy reads.
func (s *Server) sendContextLocked(client *session, messages, pins []Message, names []string, protected map[string]bool, summary *protocol.ContextSummary, budget int) error {
	nick := client.client.Nick
	if nick == "" {
		nick = "*"
	}
	encode := func(entry protocol.ChatEntry) (string, error) {
		data, err := json.Marshal(entry)
		if err != nil {
			return "", err
		}
		line := protocol.Format("server", "777", []string{nick}, string(data))
		if !strings.HasSuffix(line, "\r\n") {
			return "", errors.New("context record exceeds wire line limit")
		}
		return line, nil
	}
	summaryLine, err := encode(protocol.ChatEntry{Action: "context", Context: summary})
	if err != nil {
		return err
	}
	// chatLocked sends this terminator after this function returns successfully.
	size := len(summaryLine) + len(protocol.Format("server", "778", []string{nick}, "End of chat response"))
	admit := func(entry protocol.ChatEntry, omitted *int) (string, bool, error) {
		line, err := encode(entry)
		if err != nil {
			return "", false, err
		}
		next := size + len(line) + len(strconv.Itoa(*omitted-1)) - len(strconv.Itoa(*omitted))
		if budget != 0 && next > budget {
			return "", false, nil
		}
		size = next
		*omitted--
		return line, true, nil
	}
	lines := make([]string, len(messages))
	for i, m := range messages {
		if !protected[m.ID] {
			continue
		}
		metadata := messageMetadata(m)
		line, fits, err := admit(protocol.ChatEntry{Action: "context-message", Message: &metadata}, &summary.OmittedMessages)
		if err != nil {
			return err
		}
		if !fits {
			return errors.New("trigger/root/correction context exceeds max-bytes; increase the budget")
		}
		lines[i] = line
	}
	trimmed := false
	for i := len(messages) - 1; i >= 0; i-- {
		if protected[messages[i].ID] {
			continue
		}
		metadata := messageMetadata(messages[i])
		line, fits, err := admit(protocol.ChatEntry{Action: "context-message", Message: &metadata}, &summary.OmittedMessages)
		if err != nil {
			return err
		}
		if !fits {
			trimmed = true
			break
		}
		lines[i] = line
	}
	for _, m := range pins {
		if trimmed {
			break
		}
		metadata := messageMetadata(s.annotated(m))
		line, fits, err := admit(protocol.ChatEntry{Action: "context-pin", Message: &metadata}, &summary.OmittedPins)
		if err != nil {
			return err
		}
		trimmed = !fits
		if fits {
			lines = append(lines, line)
		}
	}
	for _, name := range names {
		if trimmed {
			break
		}
		card := s.directoryCard(name, s.now().UTC())
		line, fits, err := admit(protocol.ChatEntry{Action: "context-profile", Profile: &card}, &summary.OmittedProfiles)
		if err != nil {
			return err
		}
		trimmed = !fits
		if fits {
			lines = append(lines, line)
		}
	}
	summaryLine, err = encode(protocol.ChatEntry{Action: "context", Context: summary})
	if err != nil {
		return err
	}
	if budget != 0 && size > budget {
		return errors.New("context summary exceeds max-bytes; increase the budget")
	}
	for _, line := range lines {
		if line != "" {
			client.enqueue(line)
		}
	}
	client.enqueue(summaryLine)
	return nil
}
