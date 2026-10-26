package adapter

import (
	"testing"
)

func TestWebTransportProtocolNegotiationIsExact(t *testing.T) {
	for _, test := range []struct {
		value string
		want  bool
	}{
		{`"airc-gateway.v1"`, true},
		{`airc-gateway.v1,other`, false},
		{`xairc-gateway.v1`, false},
		{`airc-gateway.v10`, false},
		{`"airc-gateway.v1`, false},
		{`"airc-gateway.v1"junk`, false},
		{`("airc-gateway.v1")`, false},
		{``, false},
	} {
		if got := webTransportOffered([]string{test.value}); got != test.want {
			t.Fatalf("offered(%q)=%v want %v", test.value, got, test.want)
		}
	}
}
