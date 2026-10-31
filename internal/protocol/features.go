package protocol

import (
	"slices"
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
// client knows every feature up to it. A new feature bumps it and adds its
// row to Features; a feature is never removed from a level.
const Level = 1

// Feature is one row of Features: the AIRC level that introduced the feature,
// and the 005 token a daemon that predates levels advertised it under, so a
// client still reads an older daemon right. A token that names another
// feature (PINS, POLLS and ROOM were part of CHAT=1 from the start) means
// that token implied it; "" means no older daemon is taken to have it.
type Feature struct {
	Level int
	Token string
}

// Features is every version-only feature by name. Supports answers from it
// when the daemon sent AIRC=N, and from the literal tokens otherwise, so one
// table both gates a client's features and tells the user what an older
// daemon lacks.
var Features = map[string]Feature{
	// Level 1: everything a daemon that advertised one token per feature had.
	"BOT_REPLIES":       {Level: 1, Token: "BOT_REPLIES"},
	"CHANNEL_OPERATORS": {Level: 1, Token: "CHANNEL_OPERATORS"},
	"CHANNELS":          {Level: 1, Token: "CHANNELS"},
	"CHAT":              {Level: 1, Token: "CHAT"},
	"CHECK":             {Level: 1, Token: "CHECK"},
	"CONTEXT":           {Level: 1, Token: "CONTEXT"},
	"CONTEXT_BYTES":     {Level: 1, Token: "CONTEXT_BYTES"},
	"CUSTOM_REACTIONS":  {Level: 1, Token: "CUSTOM_REACTIONS"},
	"DECISION":          {Level: 1, Token: "DECISION"},
	"DIRECTORY":         {Level: 1, Token: "DIRECTORY"},
	"DM_AUDIT":          {Level: 1, Token: "DM_AUDIT"},
	"HANDOFF":           {Level: 1, Token: "HANDOFF"},
	"HISTORY_START":     {Level: 1, Token: "HISTORY_START"},
	"MENTIONS":          {Level: 1, Token: "MENTIONS"},
	"MULTILINE":         {Level: 1, Token: "MULTILINE"},
	"PASTE":             {Level: 1, Token: "PASTE"},
	"REACTIONS":         {Level: 1, Token: "REACTIONS"},
	"RECEIPTS":          {Level: 1, Token: "RECEIPTS"},
	"REPLIES":           {Level: 1, Token: "REPLIES"},
	"RESUME":            {Level: 1, Token: "RESUME"},
	"RUN":               {Level: 1, Token: "RUN"},
	"SEARCH":            {Level: 1, Token: "SEARCH"},
	"SEARCH_TERMS":      {Level: 1, Token: "SEARCH_TERMS"},
	"STATUS":            {Level: 1, Token: "STATUS"}, // the STATUS command (770)
	"TOPIC":             {Level: 1, Token: "TOPIC"},
	// Level 1, CHAT actions that had no token of their own: the first CHAT=1
	// daemon served pins, polls and room settings, so that token implies
	// them; statuses, resolutions and charters came later, so an older
	// daemon is taken to lack them.
	"CHARTER":      {Level: 1},
	"PINS":         {Level: 1, Token: "CHAT"},
	"POLLS":        {Level: 1, Token: "CHAT"},
	"RESOLVE":      {Level: 1},
	"ROOM":         {Level: 1, Token: "CHAT"},
	"STATUS_POSTS": {Level: 1}, // the status and statuses CHAT actions
}

// ChatFeature is the feature a CHAT action is advertised under: CHAT itself
// for the actions the first CHAT=1 daemon served without a token of their
// own, and the feature named after each later family.
func ChatFeature(action string) string {
	switch {
	case action == "context":
		return "CONTEXT"
	case action == "pin", action == "unpin", action == "pins":
		return "PINS"
	case action == "room":
		return "ROOM"
	case action == "status", action == "statuses":
		return "STATUS_POSTS"
	case action == "resolve":
		return "RESOLVE"
	case action == "poll", action == "vote", action == "results", action == "close-poll":
		return "POLLS"
	case strings.HasPrefix(action, "handoff"):
		return "HANDOFF"
	case strings.HasPrefix(action, "decision"):
		return "DECISION"
	case strings.HasPrefix(action, "paste"):
		return "PASTE"
	}
	return "CHAT"
}

// LevelOf is the AIRC level a daemon's 005 tokens advertise, 0 when it sent
// none (a daemon that advertised one token per feature).
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
// its level is at most the daemon's, or, from a daemon that sent no level,
// when the token that implied it is there. Names are case-insensitive.
func Supports(features map[string]string, name string) bool {
	name = strings.ToUpper(name)
	if value, ok := features[name]; ok {
		return value != "0"
	}
	feature, known := Features[name]
	if !known {
		return false
	}
	if level := LevelOf(features); level > 0 {
		return feature.Level <= level
	}
	return feature.Token != "" && feature.Token != name && Supports(features, feature.Token)
}

// Missing lists, in name order, the features of Features up to level that
// a daemon's 005 tokens do not grant: what a user of this client gives up
// against that daemon.
func Missing(features map[string]string, level int) []string {
	var missing []string
	for name, feature := range Features {
		if feature.Level <= level && !Supports(features, name) {
			missing = append(missing, name)
		}
	}
	slices.Sort(missing)
	return missing
}
