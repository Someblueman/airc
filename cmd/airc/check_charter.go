package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// roomCharter is what a charter header shows of a room: its charter and,
// while the room is orchestrated, its orchestrator and goal.
type roomCharter struct {
	Charter, Orchestrator, Goal string
}

// charterFingerprint is what the cursor store keeps of a shown charter and
// orchestrator mode: a hash, never the text, so either changing shows the
// header again. A room with neither has none; one that is not orchestrated
// hashes its charter alone.
func charterFingerprint(c roomCharter) string {
	if c == (roomCharter{}) {
		return ""
	}
	text := c.Charter
	if c.Orchestrator != "" {
		text += "\x00orchestrator\x00" + c.Orchestrator + "\x00" + c.Goal
	}
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:16])
}

// charterHeader is the header row for room's charter when it, or the room's
// orchestrator mode, differs from the one last shown: what to print, or an
// empty row that only forgets a cleared one.
func charterHeader(room string, c roomCharter, store *cursorStore) (checkTopic, bool) {
	fingerprint := charterFingerprint(c)
	if fingerprint == store.Charters[room] {
		return checkTopic{}, false
	}
	return checkTopic{Type: "charter", Target: room, Charter: c.Charter, Orchestrator: c.Orchestrator, Goal: c.Goal, Fingerprint: fingerprint}, true
}

// orchestratedLine says who orchestrates room and toward what goal.
func orchestratedLine(room, orchestrator, goal string) string {
	if goal == "" {
		return room + " orchestrated by " + orchestrator
	}
	return room + " orchestrated by " + orchestrator + ": " + goal
}

// checkCharters reads each followed room's charter when a check is too large
// to combine, as checkPins reads its pins.
func checkCharters(ctx context.Context, c *irc.Client, rooms []checkTarget, store *cursorStore, other func(irc.Event)) ([]checkTopic, error) {
	var headers []checkTopic
	for _, room := range rooms {
		if !protocol.IsChannel(room.name) {
			continue
		}
		entries, err := irc.RequestChat(ctx, c, irc.ChatRequest{Action: "room", Target: room.name, Seconds: -1, Limit: -1}, other)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if header, changed := charterHeader(room.name, roomCharter{e.Charter, e.Orchestrator, e.Goal}, store); changed {
				headers = append(headers, header)
			}
		}
	}
	return headers, nil
}
