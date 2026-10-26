package config

import (
	"strings"
	"testing"
)

func TestMembersReadRequiresSessionReserveWithoutCursorKey(t *testing.T) {
	input := strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"members","protocol":"http","enabled":true,"operations":["members.read"]}]`, 1)
	if _, err := Parse([]byte(input)); err != nil {
		t.Fatal("members read must not need a cursor key", err)
	}
	input = strings.Replace(input, `"version":1`, `"version":1,"limits":{"sessions_per_author":1,"watches_per_author":0}`, 1)
	if _, err := Parse([]byte(input)); err == nil {
		t.Fatal("members read admitted without session reserve")
	}
}
