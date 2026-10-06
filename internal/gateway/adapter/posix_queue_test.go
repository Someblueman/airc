package adapter

import (
	"strings"
	"testing"
)

func TestPOSIXQueueNames(t *testing.T) {
	for _, name := range []string{"/a", "/queue-1_return.2", "/" + strings.Repeat("x", 254)} {
		if !ValidPOSIXQueueName(name) {
			t.Fatal("rejected valid name", name)
		}
	}
	for _, name := range []string{"", "/", "queue", "//queue", "/../queue", "/.", "/..", "/queue/other", "/queue\x00", "/queue\n", "/é", "/" + strings.Repeat("x", 255)} {
		if ValidPOSIXQueueName(name) {
			t.Fatal("accepted invalid name", name)
		}
	}
}
