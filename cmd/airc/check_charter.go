package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// charterFingerprint is what the cursor store keeps of a shown charter: a
// hash, never the text. An empty charter has none.
func charterFingerprint(text string) string {
	if text == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:16])
}

// charterHeader is the header row for room's charter when it differs from
// the one last shown: the text to print, or an empty row that only forgets a
// cleared charter.
func charterHeader(room, text string, store *cursorStore) (checkTopic, bool) {
	fingerprint := charterFingerprint(text)
	if fingerprint == store.Charters[room] {
		return checkTopic{}, false
	}
	return checkTopic{Type: "charter", Target: room, Charter: text, Fingerprint: fingerprint}, true
}

// checkCharters reads each followed room's charter on a daemon without
// CHECK, as checkPins reads its pins.
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
			if header, changed := charterHeader(room.name, e.Charter, store); changed {
				headers = append(headers, header)
			}
		}
	}
	return headers, nil
}
