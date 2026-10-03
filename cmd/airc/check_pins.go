package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/Someblueman/airc/pkg/irc"
)

func checkPins(ctx context.Context, c *irc.Client, rooms []checkTarget, store *cursorStore, other func(irc.Event)) ([]checkTopic, error) {
	var headers []checkTopic
	for _, room := range rooms {
		if !isChannel(room.name) {
			continue
		}
		entries, err := irc.RequestChat(ctx, c, irc.ChatRequest{Action: "pins", Target: room.name}, other)
		if err != nil {
			return nil, err
		}
		headers = append(headers, pinHeaders(room.name, entries, store)...)
	}
	return headers, nil
}

func pinHeaders(room string, entries []irc.ChatEntry, store *cursorStore) []checkTopic {
	var headers []checkTopic
	present := map[string]bool{}
	for _, e := range entries {
		present[room+"\n"+e.ID] = true
	}
	for key := range store.Pins {
		if strings.HasPrefix(key, room+"\n") && !present[key] {
			delete(store.Pins, key)
		}
	}
	for _, e := range entries {
		if e.Message == nil {
			continue
		}
		data, _ := json.Marshal(e.Message)
		hash := sha256.Sum256(data)
		fingerprint := hex.EncodeToString(hash[:])
		key := room + "\n" + e.ID
		if store.Pins[key] == fingerprint {
			continue
		}
		preview := irc.NormalizeMessage(e.Message.Message)
		// Pin context is explicitly a preview; `pins` retrieves the full body.
		chars := []rune(preview)
		if len(chars) > 160 {
			preview = string(chars[:160]) + "…"
		}
		if e.Message.Retracted {
			preview = "[retracted] " + preview
		} else if e.Message.SupersededBy != "" {
			preview = "[superseded by " + e.Message.SupersededBy + "] " + preview
		}
		headers = append(headers, checkTopic{Type: "pin", Target: room, Topic: preview, ID: e.ID, From: e.From, PinKey: key, Fingerprint: fingerprint})
	}
	return headers
}
