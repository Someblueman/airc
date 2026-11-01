package protocol

import (
	"fmt"
	"testing"
)

func TestFeaturesTableIsConsistent(t *testing.T) {
	for name, level := range Features {
		if level < 1 || level > Level {
			t.Errorf("%s is at level %d; this build speaks levels 1-%d", name, level, Level)
		}
	}
}

func TestSupportsByLevelAndByToken(t *testing.T) {
	current := map[string]string{"AIRC": fmt.Sprint(Level), "HISTORY": "1000", "PASTE_BYTES": "65536", "MONITOR": "128", "IDEMPOTENCY": "1"}
	for _, name := range []string{"CHAT", "chat", "PINS", "STATUS_POSTS", "RESOLVE", "CHARTER", "ORCHESTRATE", "HISTORY_START", "IDEMPOTENCY", "HISTORY", "MONITOR"} {
		if !Supports(current, name) {
			t.Errorf("a level-%d daemon lacks %s", Level, name)
		}
	}
	for _, name := range []string{"ADMIN", "WAKE", "NOPE", "SAFE_RETRY"} {
		if Supports(current, name) {
			t.Errorf("a level-%d daemon without the %s token has it", Level, name)
		}
	}
	if LevelOf(current) != Level {
		t.Errorf("level %d", LevelOf(current))
	}

	// A literal token decides first: a proxy or a test withdraws a feature
	// the level grants with NAME=0, and a daemon from before levels, with
	// one token per feature, has only what its tokens name.
	withdrawn := map[string]string{"AIRC": fmt.Sprint(Level), "SEARCH": "0", "HISTORY": "0"}
	if Supports(withdrawn, "SEARCH") || Supports(withdrawn, "HISTORY") || !Supports(withdrawn, "SEARCH_TERMS") {
		t.Errorf("withdrawn: SEARCH %v, HISTORY %v, SEARCH_TERMS %v", Supports(withdrawn, "SEARCH"), Supports(withdrawn, "HISTORY"), Supports(withdrawn, "SEARCH_TERMS"))
	}
	older := map[string]string{"CHAT": "1", "MULTILINE": "1"}
	if !Supports(older, "CHAT") || !Supports(older, "MULTILINE") || Supports(older, "PINS") || Supports(older, "CONTEXT") || LevelOf(older) != 0 {
		t.Errorf("a daemon without a level: CHAT %v, MULTILINE %v, PINS %v, CONTEXT %v, level %d", Supports(older, "CHAT"), Supports(older, "MULTILINE"), Supports(older, "PINS"), Supports(older, "CONTEXT"), LevelOf(older))
	}

	levelOne := map[string]string{"AIRC": "1"}
	if !Supports(levelOne, "CHARTER") || Supports(levelOne, "ORCHESTRATE") {
		t.Errorf("a level-1 daemon: CHARTER %v, ORCHESTRATE %v", Supports(levelOne, "CHARTER"), Supports(levelOne, "ORCHESTRATE"))
	}

	newer := map[string]string{"AIRC": "7"}
	if !Supports(newer, "RESOLVE") || Supports(newer, "FUTURE") || LevelOf(newer) != 7 {
		t.Errorf("a level-7 daemon: RESOLVE %v, FUTURE %v, level %d", Supports(newer, "RESOLVE"), Supports(newer, "FUTURE"), LevelOf(newer))
	}
	if LevelOf(map[string]string{"AIRC": "x"}) != 0 || LevelOf(map[string]string{"AIRC": "-1"}) != 0 || LevelOf(nil) != 0 {
		t.Error("a malformed level is not 0")
	}
}
