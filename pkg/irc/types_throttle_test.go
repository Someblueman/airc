package irc

import "testing"

func TestThrottledReadsTheParameterAfterTheNick(t *testing.T) {
	for _, c := range []struct {
		params []string
		want   bool
	}{
		{[]string{"*", "throttled"}, true},
		{[]string{"alice", "throttled"}, true},
		{[]string{"throttled"}, false},
		{[]string{"*"}, false},
		{nil, false},
		{[]string{"*", "alice", "throttled"}, false},
	} {
		if got := throttled(c.params); got != c.want {
			t.Fatalf("throttled(%q) = %v", c.params, got)
		}
	}
}
