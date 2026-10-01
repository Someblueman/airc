package irc

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

type AgentCard = protocol.AgentCard
type AgentProfile = protocol.AgentProfile

func (c *Client) Directory(nick string) error {
	if !c.Supports("DIRECTORY") {
		return errors.New("directory needs a daemon with DIRECTORY")
	}
	if nick == "" {
		return c.Raw("DIRECTORY")
	}
	line, err := commandLine("DIRECTORY", []string{nick}, "")
	if err != nil {
		return err
	}
	return c.writeLine(line)
}

// UpdateProfile changes only supplied fields of this nickname's card.
func (c *Client) UpdateProfile(fields map[string]string) error {
	if !c.Supports("DIRECTORY") {
		return errors.New("profiles need a daemon with DIRECTORY")
	}
	data, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	return c.Raw("PROFILE :" + string(data))
}

func (c *Client) SetPresence(state, note string, ttl time.Duration) error {
	if !c.Supports("DIRECTORY") {
		return errors.New("presence needs a daemon with DIRECTORY")
	}
	if state != "clear" && (!protocol.ValidPresence(state) || ttl < time.Second || ttl > time.Hour) || !protocol.BriefText(note, 240) {
		return errors.New("invalid presence state, TTL (1s-1h), or note (240 bytes)")
	}
	seconds := int64((ttl + time.Second - 1) / time.Second)
	line, err := commandLine("PRESENCE", []string{state, fmt.Sprint(seconds)}, note)
	if err != nil {
		return err
	}
	return c.writeLine(line)
}

func (c *Client) React(parent, kind string) error {
	if !c.Supports("REACTIONS") {
		return errors.New("reactions need a daemon with REACTIONS")
	}
	if !protocol.ValidMessageID(parent) || !protocol.ValidReaction(kind) {
		return errors.New("reaction requires a message ID and a single symbol")
	}
	if kind != "seen" && kind != "checking" && kind != "agree" && kind != "disagree" && !c.Supports("CUSTOM_REACTIONS") {
		return errors.New("custom reactions need CUSTOM_REACTIONS")
	}
	return c.Raw("REACT " + parent + " :" + kind)
}

// Search returns original history events and an end-of-history paging marker.
func (c *Client) Search(target, query, from, after string, limit int) error {
	if !c.Supports("SEARCH") {
		return errors.New("search needs a daemon with SEARCH")
	}
	if !protocol.BriefText(query, 256) || query == "" || limit < 1 || limit > 1000 {
		return errors.New("search requires a query of at most 256 bytes and limit 1-1000")
	}
	if after == "" {
		after = "*"
	}
	if from == "" {
		from = "*"
	}
	line, err := commandLine("SEARCH", []string{target, fmt.Sprint(limit), after, from}, query)
	if err != nil {
		return err
	}
	return c.writeLine(line)
}

type DirectoryEvent struct {
	Type string `json:"type"`
	AgentCard
}

func (*DirectoryEvent) ircEvent() {}

type EndOfDirectoryEvent struct {
	Type string `json:"type"`
}

func (*EndOfDirectoryEvent) ircEvent() {}
