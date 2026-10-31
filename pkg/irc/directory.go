package irc

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

func (c *Client) Directory(nick string) error {
	if nick == "" {
		return c.Raw("DIRECTORY")
	}
	return c.command("DIRECTORY", []string{nick}, "")
}

// UpdateProfile changes only supplied fields of this nickname's card.
func (c *Client) UpdateProfile(fields map[string]string) error {
	data, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	return c.Raw("PROFILE :" + string(data))
}

func (c *Client) SetPresence(state, note string, ttl time.Duration) error {
	if state != "clear" && (!protocol.ValidPresence(state) || ttl < time.Second || ttl > time.Hour) || !protocol.BriefText(note, 240) {
		return errors.New("invalid presence state, TTL (1s-1h), or note (240 bytes)")
	}
	seconds := int64((ttl + time.Second - 1) / time.Second)
	return c.command("PRESENCE", []string{state, fmt.Sprint(seconds)}, note)
}

func (c *Client) React(parent, kind string) error {
	if !protocol.ValidIDArgument(parent) || !protocol.ValidReaction(kind) {
		return errors.New("reaction requires a message ID and a single symbol")
	}
	return c.Raw("REACT " + parent + " :" + kind)
}

// Search returns original history events and an end-of-history paging marker.
func (c *Client) Search(target, query, from, after string, limit int) error {
	return c.SearchWith(target, query, from, after, limit, SearchOptions{})
}

// SearchOptions refine a search. The zero value is a plain SEARCH: one case-insensitive substring, oldest first.
type SearchOptions struct {
	// Terms reads the query as terms that must all occur: words, "quoted
	// phrases", and -word or -"phrase" for what must not.
	Terms bool
	// Newest returns the newest matches first. The cursor is then the oldest
	// match returned so far, and a page continues before it. It reads only
	// the memory window; the end-of-history status "archived" says older
	// matches may remain on disk, where an oldest-first search reaches them.
	Newest bool
	// Since and Until bound message times: Since inclusive, Until exclusive.
	// The daemon compares whole seconds.
	Since, Until time.Time
	// Run keeps only the messages posted under this run label; it needs a
	// daemon with RUN.
	Run string
}

func (o SearchOptions) encode() string {
	var parts []string
	if o.Terms {
		parts = append(parts, "terms")
	}
	if o.Newest {
		parts = append(parts, "newest")
	}
	if !o.Since.IsZero() {
		parts = append(parts, fmt.Sprintf("since=%d", o.Since.Unix()))
	}
	if !o.Until.IsZero() {
		parts = append(parts, fmt.Sprintf("until=%d", o.Until.Unix()))
	}
	if o.Run != "" {
		parts = append(parts, "run="+o.Run)
	}
	return strings.Join(parts, ",")
}

// SearchWith is Search with options.
func (c *Client) SearchWith(target, query, from, after string, limit int, options SearchOptions) error {
	if !protocol.BriefText(query, 256) || query == "" || limit < 1 || limit > 1000 {
		return errors.New("search requires a query of at most 256 bytes and limit 1-1000")
	}
	if after == "" {
		after = "*"
	}
	if from == "" {
		from = "*"
	}
	params := []string{target, fmt.Sprint(limit), after, from}
	if options.Run != "" && !protocol.ValidRunID(options.Run) {
		return errors.New("a run label is 1-64 letters, digits, '.', '_' or '-'")
	}
	if encoded := options.encode(); encoded != "" {
		params = append(params, encoded)
	}
	return c.command("SEARCH", params, query)
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
