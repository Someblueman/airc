package config

import (
	"strings"
	"testing"
)

func TestEnabledHTTPAllowsOnlyImplementedOperations(t *testing.T) {
	valid := strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"http","protocol":"http","enabled":true,"operations":["send","reply","receipt.get"]}]`, 1)
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
}
