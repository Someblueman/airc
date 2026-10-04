package adapter

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/store"
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

func TestWebTransportServerRejectsInvalidConstruction(t *testing.T) {
	if _, err := WebTransportServer(nil, make(chan struct{}, 1), nil); err != store.ErrInvalid {
		t.Fatalf("nil dispatcher err=%v", err)
	}
	if _, err := WebTransportServer(nil, nil, nil); err != store.ErrInvalid {
		t.Fatalf("nil requests err=%v", err)
	}
}
