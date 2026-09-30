package server

import "testing"

func TestNicknameValidation(t *testing.T) {
	for _, nick := range []string{"alice", "researcher-2", "_observer", "[agent]"} {
		if !validNick(nick) {
			t.Errorf("validNick(%q) = false", nick)
		}
	}
	for _, nick := range []string{"", "2alice", "bad nick", "bad!nick", "toolongtoolongtoolongtoolongtoolong"} {
		if validNick(nick) {
			t.Errorf("validNick(%q) = true", nick)
		}
	}
}

func TestChannelValidation(t *testing.T) {
	for _, channel := range []string{"#research", "&local"} {
		if !validChannel(channel) {
			t.Errorf("validChannel(%q) = false", channel)
		}
	}
	for _, channel := range []string{"", "research", "#bad name", "#bad,channel", "#bad:channel", "#", string([]byte{'#', 0xff})} {
		if validChannel(channel) {
			t.Errorf("validChannel(%q) = true", channel)
		}
	}
}

func TestHistoryRingKeepsLatestMatchingMessages(t *testing.T) {
	history := newHistory(3)
	for i, target := range []string{"#one", "#two", "#one", "#one"} {
		history.add(Message{ID: string(rune('a' + i)), Target: target})
	}
	got := history.recent("#one", 10)
	if len(got) != 2 || got[0].ID != "c" || got[1].ID != "d" {
		t.Fatalf("recent history = %#v, want [c d]", got)
	}
	got = history.recent("#one", 1)
	if len(got) != 1 || got[0].ID != "d" {
		t.Fatalf("limited history = %#v, want [d]", got)
	}
}
