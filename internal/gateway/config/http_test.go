package config

import (
	"strings"
	"testing"
)

func TestEnabledHTTPAllowsOnlyImplementedOperations(t *testing.T) {
	valid := strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"http","protocol":"http","enabled":true,"operations":["send","reply","reaction.add","context.read","receipt.get"]}]`, 1)
	c, err := Parse([]byte(valid))
	if err != nil || !c.Listeners[0].Enabled || c.Listeners[0].Address != "127.0.0.1:0" {
		t.Fatal("enabled HTTP validation failed", c, err)
	}
	for _, input := range []string{
		strings.Replace(valid, `"receipt.get"`, `"history.read"`, 1),
		strings.Replace(valid, `"protocol":"http"`, `"protocol":"http3"`, 1),
		strings.Replace(valid, `"enabled":true`, `"enabled":true,"address":"0.0.0.0:9000"`, 1),
	} {
		if _, err := Parse([]byte(input)); err == nil {
			t.Fatal("unimplemented/insecure enabled HTTP accepted", input)
		}
	}
	history := strings.Replace(valid, `"receipt.get"`, `"history.read"`, 1)
	history = strings.Replace(history, `"version":1`, `"version":1,"cursor_key_file":"/private/gateway/cursor.key"`, 1)
	if _, err := Parse([]byte(history)); err != nil {
		t.Fatal("configured HTTP history rejected", err)
	}
	for _, limits := range []string{`"sessions":1,"sessions_per_principal":1,"workers":1,"watches":0,"watches_per_principal":0`, `"sessions_per_principal":1,"watches_per_principal":0`} {
		input := strings.Replace(history, `"version":1`, `"version":1,"limits":{`+limits+`}`, 1)
		if _, err := Parse([]byte(input)); err == nil {
			t.Fatal("history without send reserve accepted", input)
		}
	}
}

func TestDirectoryRequiresSendReserveWithoutCursorKey(t *testing.T) {
	input := strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"directory","protocol":"http","enabled":true,"operations":["directory"]}]`, 1)
	if _, err := Parse([]byte(input)); err != nil {
		t.Fatal("directory-only config requires unrelated cursor", err)
	}
	input = strings.Replace(input, `"version":1`, `"version":1,"limits":{"sessions_per_principal":1,"watches_per_principal":0}`, 1)
	if _, err := Parse([]byte(input)); err == nil {
		t.Fatal("directory admitted without send reserve")
	}
}

func TestContextRequiresSessionReserveWithoutCursorKey(t *testing.T) {
	input := strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"context","protocol":"http","enabled":true,"operations":["context.read"]}]`, 1)
	if _, err := Parse([]byte(input)); err != nil {
		t.Fatal("context-only config should not require a cursor key", err)
	}
	input = strings.Replace(input, `"version":1`, `"version":1,"limits":{"sessions_per_principal":1,"watches_per_principal":0}`, 1)
	if _, err := Parse([]byte(input)); err == nil {
		t.Fatal("context admitted without send reserve")
	}
}

func TestPinsRequiresSessionReserveWithoutCursorKey(t *testing.T) {
	input := strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"pins","protocol":"http","enabled":true,"operations":["pins.read"]}]`, 1)
	if _, err := Parse([]byte(input)); err != nil {
		t.Fatal("pins-only config should not require a cursor key", err)
	}
	input = strings.Replace(input, `"version":1`, `"version":1,"limits":{"sessions_per_principal":1,"watches_per_principal":0}`, 1)
	if _, err := Parse([]byte(input)); err == nil {
		t.Fatal("pins admitted without send reserve")
	}
}

func TestRoomAndWaitingRequireSessionReserveWithoutCursorKey(t *testing.T) {
	for _, operation := range []string{"room.read", "waiting.read"} {
		input := strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"read","protocol":"http","enabled":true,"operations":["`+operation+`"]}]`, 1)
		if _, err := Parse([]byte(input)); err != nil {
			t.Fatalf("%s config should not require cursor key: %v", operation, err)
		}
		input = strings.Replace(input, `"version":1`, `"version":1,"limits":{"sessions_per_principal":1,"watches_per_principal":0}`, 1)
		if _, err := Parse([]byte(input)); err == nil {
			t.Fatalf("%s admitted without send reserve", operation)
		}
	}
}

func TestPollResultsRequiresSessionReserveWithoutCursorKey(t *testing.T) {
	input := strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"poll","protocol":"http","enabled":true,"operations":["poll.results"]}]`, 1)
	if _, err := Parse([]byte(input)); err != nil {
		t.Fatal("poll-only config should not require a cursor key", err)
	}
	input = strings.Replace(input, `"version":1`, `"version":1,"limits":{"sessions_per_principal":1,"watches_per_principal":0}`, 1)
	if _, err := Parse([]byte(input)); err == nil {
		t.Fatal("poll admitted without send reserve")
	}
}

func TestTopicReadRequiresSessionReserveWithoutCursorKey(t *testing.T) {
	input := strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"topic","protocol":"http","enabled":true,"operations":["topic.read"]}]`, 1)
	if _, err := Parse([]byte(input)); err != nil {
		t.Fatal("topic-only config should not require a cursor key", err)
	}
	input = strings.Replace(input, `"version":1`, `"version":1,"limits":{"sessions_per_principal":1,"watches_per_principal":0}`, 1)
	if _, err := Parse([]byte(input)); err == nil {
		t.Fatal("topic admitted without send reserve")
	}
}
