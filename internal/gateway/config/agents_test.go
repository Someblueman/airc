package config

import (
	"strings"
	"testing"
)

func TestAgentsReadRequiresSessionReserveWithoutCursorKey(t *testing.T) {
	input := strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"agents","protocol":"http","enabled":true,"operations":["agents.read"]}]`, 1)
	if _, err := Parse([]byte(input)); err != nil {
		t.Fatal("agents read must not need cursor key", err)
	}
	input = strings.Replace(input, `"version":1`, `"version":1,"limits":{"sessions_per_author":1,"watches_per_author":0}`, 1)
	if _, err := Parse([]byte(input)); err == nil {
		t.Fatal("agents read admitted without session reserve")
	}
}
