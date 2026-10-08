//go:build (!darwin && !linux) || !arm64

package sysvqueue

import "testing"

func TestUnsupportedPlatformDoesNotSubstituteCarrier(t *testing.T) {
	if q, err := Create(123); q != nil || err != ErrUnsupported {
		t.Fatal(q, err)
	}
	if q, err := Open(123); q != nil || err != ErrUnsupported {
		t.Fatal(q, err)
	}
	var q Queue
	if err := q.WriteMessage(t.Context(), []byte("body")); err != ErrUnsupported {
		t.Fatal(err)
	}
	if _, err := q.ReadMessage(t.Context(), make([]byte, MessageBytes)); err != ErrUnsupported {
		t.Fatal(err)
	}
	if err := q.Close(); err != ErrUnsupported {
		t.Fatal(err)
	}
}
