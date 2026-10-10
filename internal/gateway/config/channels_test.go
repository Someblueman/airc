package config

import (
	"strings"
	"testing"
)

func TestChannelsReadRequiresSessionReserveWithoutCursorKey(t *testing.T) {
	input := strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"channels","protocol":"http","enabled":true,"operations":["channels.read"]}]`, 1)
	if _, err := Parse([]byte(input)); err != nil {
		t.Fatal("channels read must not need cursor key", err)
	}
	input = strings.Replace(input, `"version":1`, `"version":1,"limits":{"sessions_per_principal":1,"watches_per_principal":0}`, 1)
	if _, err := Parse([]byte(input)); err == nil {
		t.Fatal("channels read admitted without session reserve")
	}
}
