package protocol

import (
	"slices"
	"testing"
)

func TestFeaturesTableIsConsistent(t *testing.T) {
	for name, feature := range Features {
		if feature.Level < 1 || feature.Level > Level {
			t.Errorf("%s is at level %d; this build speaks levels 1-%d", name, feature.Level, Level)
		}
		if feature.Token != "" && feature.Token != name {
			if implied, ok := Features[feature.Token]; !ok || implied.Token != feature.Token {
				t.Errorf("%s is implied by %s, which is not a feature advertised under its own token", name, feature.Token)
			}
		}
	}
	for _, action := range []string{"context", "pin", "unpin", "pins", "room", "status", "statuses", "resolve", "poll", "vote", "results", "close-poll", "handoff", "handoff-accept", "handoffs", "decision", "decision-answer", "decisions", "paste-begin", "paste-get", "pastes", "action", "correct", "retract", "prepare", "cancel", "waiting", "typing", "thinking"} {
		if _, ok := Features[ChatFeature(action)]; !ok {
			t.Errorf("CHAT action %s is advertised under %s, which is not in the table", action, ChatFeature(action))
		}
	}
}

func TestSupportsByLevelAndByToken(t *testing.T) {
	current := map[string]string{"AIRC": "1", "HISTORY": "1000", "PASTE_BYTES": "65536", "MONITOR": "128", "IDEMPOTENCY": "1"}
	for _, name := range []string{"CHAT", "chat", "PINS", "STATUS_POSTS", "RESOLVE", "CHARTER", "HISTORY_START", "IDEMPOTENCY", "HISTORY", "MONITOR"} {
		if !Supports(current, name) {
			t.Errorf("a level-1 daemon lacks %s", name)
		}
	}
	for _, name := range []string{"ADMIN", "WAKE", "NOPE", "SAFE_RETRY"} {
		if Supports(current, name) {
			t.Errorf("a level-1 daemon without the %s token has it", name)
		}
	}
	if LevelOf(current) != 1 || len(Missing(current, Level)) != 0 {
		t.Errorf("level %d, missing %v", LevelOf(current), Missing(current, Level))
	}

	older := map[string]string{"CHAT": "1", "CONTEXT": "1", "MULTILINE": "1", "HISTORY": "0", "SEARCH": "0"}
	for _, name := range []string{"CHAT", "CONTEXT", "MULTILINE", "PINS", "POLLS", "ROOM"} {
		if !Supports(older, name) {
			t.Errorf("an older CHAT=1 daemon lacks %s", name)
		}
	}
	for _, name := range []string{"STATUS_POSTS", "RESOLVE", "CHARTER", "CONTEXT_BYTES", "HANDOFF", "SEARCH", "HISTORY", "AIRC"} {
		if Supports(older, name) {
			t.Errorf("an older CHAT=1 daemon has %s", name)
		}
	}
	if LevelOf(older) != 0 {
		t.Errorf("level %d for a daemon without AIRC", LevelOf(older))
	}
	missing := Missing(older, Level)
	if !slices.IsSorted(missing) || slices.Contains(missing, "CHAT") || slices.Contains(missing, "PINS") || !slices.Contains(missing, "STATUS_POSTS") || !slices.Contains(missing, "HANDOFF") {
		t.Errorf("missing = %v", missing)
	}

	newer := map[string]string{"AIRC": "7"}
	if !Supports(newer, "RESOLVE") || Supports(newer, "FUTURE") || LevelOf(newer) != 7 {
		t.Errorf("a level-7 daemon: RESOLVE %v, FUTURE %v, level %d", Supports(newer, "RESOLVE"), Supports(newer, "FUTURE"), LevelOf(newer))
	}
	if LevelOf(map[string]string{"AIRC": "x"}) != 0 || LevelOf(map[string]string{"AIRC": "-1"}) != 0 || LevelOf(nil) != 0 {
		t.Error("a malformed level is not 0")
	}
}
