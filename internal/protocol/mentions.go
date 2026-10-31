package protocol

import (
	"slices"
	"strings"
	"unicode/utf8"
)

// A mention is "@nick" anywhere in a message, or "nick:" at the start of a line,
// which is how agents already address one another. Nicknames here are letters,
// digits, underscore and hyphen so that punctuation around a tag ("(@anvil)",
// "@anvil,") never becomes part of the name.
//
// In regular-expression terms a tag is (?:^|[^A-Za-z0-9_@])@([A-Za-z_][A-Za-z0-9_-]{0,29})
// and an address is ^([A-Za-z_][A-Za-z0-9_-]{0,29}):(?:\s|$); the scanners
// below find exactly those matches without linking a regexp engine.

func nameStart(c byte) bool { return c == '_' || c|0x20 >= 'a' && c|0x20 <= 'z' }
func nameByte(c byte) bool  { return nameStart(c) || c == '-' || c >= '0' && c <= '9' }

// mentionName returns the name at the start of s, at most 30 bytes.
func mentionName(s string) string {
	if s == "" || !nameStart(s[0]) {
		return ""
	}
	n := 1
	for n < len(s) && n < 30 && nameByte(s[n]) {
		n++
	}
	return s[:n]
}

// lineTags returns each tag in a line, as the tag pattern's successive
// non-overlapping matches would: the character before "@", when there is
// one, belongs to the match, so it cannot also end the previous one.
func lineTags(line string, add func(string)) {
	next := 0 // where the next match may begin
	for at := strings.IndexByte(line, '@'); at >= 0; {
		start := at
		if at > 0 {
			_, width := utf8.DecodeLastRuneInString(line[:at])
			start = at - width
			if c := line[start]; width == 1 && (nameByte(c) && c != '-' || c == '@') {
				start = -1
			}
		}
		if start >= next {
			if name := mentionName(line[at+1:]); name != "" {
				add(name)
				next = at + 1 + len(name)
			}
		}
		following := strings.IndexByte(line[at+1:], '@')
		if following < 0 {
			return
		}
		at += 1 + following
	}
}

// lineAddress returns the name a line starts with, followed by ":" and then
// whitespace or the end of the line, or "".
func lineAddress(line string) string {
	name := mentionName(line)
	if name == "" || len(name) == len(line) || line[len(name)] != ':' {
		return ""
	}
	if rest := line[len(name)+1:]; rest != "" && !strings.ContainsRune("\t\n\f\r ", rune(rest[0])) {
		return ""
	}
	return name
}

// Group tags: "@humans" tags every nickname whose profile kind is human,
// "@agents" every agent, and "@here" everyone with a live session in the
// room. The server resolves them when a room message is posted and records
// the nicknames in the message's metadata (ChatMetadata.GroupMentions), at most
// MaxGroupMentions of them; in a direct message they are plain text.
const (
	GroupHumans = "humans"
	GroupAgents = "agents"
	GroupHere   = "here"

	// MaxGroupMentions and MaxGroupMentionBytes bound the nicknames a
	// message's group tags resolve to, by count and by their total length
	// (each name plus its JSON quotes and comma): a group tag is one room
	// message, not a fan-out, and its live line must still fit
	// MaxLineLength with the largest body and the rest of its metadata
	// (TestLargestGroupMentionLinesFit). The first names in sorted order
	// that fit are recorded; the rest are not tagged.
	MaxGroupMentions     = 32
	MaxGroupMentionBytes = 384
)

// IsGroupTag reports whether a tagged name is a group tag.
func IsGroupTag(name string) bool {
	switch strings.ToLower(name) {
	case GroupHumans, GroupAgents, GroupHere:
		return true
	}
	return false
}

// scanMentions calls tag for each "@name" and address for each leading
// "name:" in body, skipping fenced code blocks.
func scanMentions(body string, tag, address func(string)) {
	var fence byte
	length := 0
	for line := range strings.SplitSeq(body, "\n") {
		mark, count, suffix := CodeFence(line)
		if fence == 0 && count >= 3 {
			fence, length = mark, count
			continue
		}
		if fence != 0 {
			if mark == fence && count >= length && strings.TrimSpace(suffix) == "" {
				fence = 0
			}
			continue
		}
		lineTags(line, tag)
		if name := lineAddress(line); name != "" {
			address(name)
		}
	}
}

// collector lower-cases names and keeps each once, in order of first appearance.
type collector struct {
	found []string
	seen  map[string]bool
}

func (c *collector) add(name string) {
	name = strings.ToLower(strings.TrimRight(name, "-"))
	if name != "" && !c.seen[name] {
		if c.seen == nil {
			c.seen = map[string]bool{}
		}
		c.seen[name] = true
		c.found = append(c.found, name)
	}
}

// Mentions returns the lower-cased nicknames a message tags or addresses, in order
// of first appearance, without duplicates. A name that merely looks like a tag
// ("user@example.com") is not a mention. A group tag's word is returned like
// any name; GroupMentions picks the groups out.
func Mentions(body string) []string {
	if !strings.ContainsAny(body, "@:") {
		return nil // both forms need one of these; skip the scans for plain text
	}
	var c collector
	scanMentions(body, c.add, c.add)
	return c.found
}

// GroupMentions returns the group tags ("humans", "agents", "here") a body
// tags with @, lower-cased, in order of first appearance, under the same
// word-boundary and code-fence rules as a nickname tag. The address form
// ("here: ...") is not a group tag.
func GroupTags(body string) []string {
	if !strings.Contains(body, "@") {
		return nil
	}
	var c collector
	scanMentions(body, func(name string) {
		if IsGroupTag(strings.TrimRight(name, "-")) { // as a nickname tag is trimmed
			c.add(name)
		}
	}, func(string) {})
	return c.found
}

// MessageMentions is Mentions plus the nick a handoff step is for, which
// counts as a mention without being written in the body, and the nicknames
// the message's group tags resolved to when it was posted (its metadata's
// mentions), each once.
func MessageMentions(body, forNick string, resolved []string) []string {
	found := Mentions(body)
	if name := strings.ToLower(forNick); name != "" && !slices.Contains(found, name) {
		found = append(found, name)
	}
	for _, nick := range resolved {
		if name := strings.ToLower(nick); name != "" && !slices.Contains(found, name) {
			found = append(found, name)
		}
	}
	return found
}

// MentionsNick reports whether body tags or addresses nick (case-insensitive)
// by name. A group tag is nobody's mention here: the server resolves groups
// when a room message is posted, and MentionedBy reads its answer.
func MentionsNick(body, nick string) bool {
	nick = strings.ToLower(nick)
	return slices.Contains(Mentions(body), nick)
}

// MentionedBy reports whether a room message is for nick: its body tags or
// addresses it, the message is for it (a handoff or decision step), or one
// of its group tags resolved to it (the message's metadata mentions). A
// client needs no directory or room membership of its own, because the
// server resolved the groups when the message was posted; against an older
// daemon that records none, only the names in the body count.
func MentionedBy(body, forNick string, resolved []string, nick string) bool {
	if strings.EqualFold(forNick, nick) || MentionsNick(body, nick) {
		return true
	}
	return slices.ContainsFunc(resolved, func(name string) bool { return strings.EqualFold(name, nick) })
}

// CodeFence recognizes Markdown backtick/tilde fences, with up to three leading
// spaces. The suffix is a language label on an opening fence, empty on a close.
func CodeFence(line string) (mark byte, count int, suffix string) {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 || trimmed == "" || trimmed[0] != '`' && trimmed[0] != '~' {
		return 0, 0, ""
	}
	mark = trimmed[0]
	for count < len(trimmed) && trimmed[count] == mark {
		count++
	}
	return mark, count, trimmed[count:]
}
