package config

import (
	"strings"
	"testing"
)

func TestEventsWatchRequiresCursorAndWatchReserve(t *testing.T) {
	input := strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"live","protocol":"http","enabled":true,"operations":["events.watch"]}]`, 1)
	if _, err := Parse([]byte(input)); err == nil {
		t.Fatal("live watch lacks cursor key")
	}
	input = strings.Replace(input, `"version":1`, `"version":1,"cursor_key_file":"/private/live-cursor.key"`, 1)
	if _, err := Parse([]byte(input)); err != nil {
		t.Fatal(err)
	}
	for _, limits := range []string{
		`"sessions":1`, `"sessions_per_author":1`,
		`"watches":0`, `"watches_per_author":0`,
	} {
		bad := strings.Replace(input, `"version":1`, `"version":1,"limits":{`+limits+`}`, 1)
		if _, err := Parse([]byte(bad)); err == nil {
			t.Fatal("live watch admitted without reserve", limits)
		}
	}
}
