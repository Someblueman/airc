package pgnotifywire

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

func notificationBody(channel, payload string) []byte {
	body := binary.BigEndian.AppendUint32(nil, 42)
	body = append(append(body, channel...), 0)
	return append(append(body, payload...), 0)
}

func TestNotificationAcceptsUTF8JSONPayloads(t *testing.T) {
	s := &Session{channel: "airc_requests"}
	payload := "{\n\t\"message\": \"hello 世界 🙂\"\r\n}"
	got, err := s.notification(notificationBody("airc_requests", payload))
	if err != nil || string(got) != payload {
		t.Fatalf("payload=%q err=%v", got, err)
	}
	for name, body := range map[string][]byte{
		"channel": notificationBody("other", "{}"),
		"empty":   notificationBody("airc_requests", ""),
		"utf8":    notificationBody("airc_requests", "\xff"),
		"trailer": append(notificationBody("airc_requests", "{}"), 'x'),
		"pid":     append([]byte{0, 0, 0, 0}, notificationBody("airc_requests", "{}")[4:]...),
	} {
		if _, err := s.notification(body); !errors.Is(err, ErrProtocol) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := s.notification(notificationBody("airc_requests", strings.Repeat("a", 8000))); !errors.Is(err, ErrProtocol) {
		t.Fatal("oversized payload accepted", err)
	}
}
