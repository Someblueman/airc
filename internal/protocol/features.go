package protocol

import (
	"strconv"
	"strings"
)

// LevelToken is the 005 token that carries the daemon's AIRC level: AIRC=N
// says the daemon has every feature in Features introduced at level N or
// below, in place of one version-only token per feature. Tokens that carry a
// value (HISTORY=N, PASTE_BYTES=N, MONITOR=N, CARRIERS=...), the standard
// IRC ones (PREFIX, CHANMODES, AWAYLEN) and those a daemon advertises only
// when configured for them (ACCESS, ACCOUNTS, ADMIN, TLS, WAKE, WEBIRC,
// and IDEMPOTENCY and SAFE_RETRY, which need history) are still sent on
// their own.
const LevelToken = "AIRC"

// Level is the AIRC level this build speaks: the daemon advertises it, and a
// client requires at least it (irc.Dial refuses a daemon below it, so every
// feature up to Level is guaranteed once connected) and knows every feature
// up to it. A new feature bumps it and adds its row to Features; a feature
// is never removed from a level.
const Level = 2

// Features is every version-only feature by name, with the AIRC level that
// introduced it. A client checks Supports for a feature above its own level
// only; those at or below it come with any daemon it is connected to.
var Features = map[string]int{
	// Level 1: everything a daemon that advertised one token per feature had.
	"BOT_REPLIES":       1,
	"CHANNEL_OPERATORS": 1,
	"CHANNELS":          1,
	"CHAT":              1,
	"CHECK":             1,
	"CONTEXT":           1,
	"CONTEXT_BYTES":     1,
	"CUSTOM_REACTIONS":  1,
	"DECISION":          1,
	"DIRECTORY":         1,
	"DM_AUDIT":          1,
	"HANDOFF":           1,
	"HISTORY_START":     1,
	"MENTIONS":          1,
	"MULTILINE":         1,
	"PASTE":             1,
	"REACTIONS":         1,
	"RECEIPTS":          1,
	"REPLIES":           1,
	"RESUME":            1,
	"RUN":               1,
	"SEARCH":            1,
	"SEARCH_TERMS":      1,
	"STATUS":            1, // the STATUS command (770)
	"TOPIC":             1,
	// Level 1, the CHAT action families that had a token of their own.
	"CHARTER":      1,
	"PINS":         1,
	"POLLS":        1,
	"RESOLVE":      1,
	"ROOM":         1,
	"STATUS_POSTS": 1, // the status and statuses CHAT actions
	// Level 2.
	"ORCHESTRATE": 2, // a room's orchestrator and goal on the room action
}

// LevelOf is the AIRC level a daemon's 005 tokens advertise, 0 when it sent
// none or a malformed one.
func LevelOf(features map[string]string) int {
	level, err := strconv.Atoi(features[LevelToken])
	if err != nil || level < 0 {
		return 0
	}
	return level
}

// Supports reports whether a daemon whose 005 tokens are features has the
// named feature. A literal token decides first: KEY or KEY=VALUE grants it
// and KEY=0 withdraws it, even one the level would grant (how a proxy or a
// test takes a feature away). Otherwise a feature in Features is had when
// its level is at most the daemon's. Names are case-insensitive.
func Supports(features map[string]string, name string) bool {
	name = strings.ToUpper(name)
	if value, ok := features[name]; ok {
		return value != "0"
	}
	level, known := Features[name]
	return known && level <= LevelOf(features)
}
