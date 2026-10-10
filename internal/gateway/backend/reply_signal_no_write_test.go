package backend

import (
	"testing"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

const replySignalNoWriteFence = "PING :reply-signal-no-write-fence"

// The exchange is synchronous. A marker written after it follows every mutation
// on the same native stream, and the fixture records those in channel order.
func requireNoReplySignalWrite(t *testing.T, client *irc.Client, commands <-chan string) {
	t.Helper()
	if err := client.Raw(replySignalNoWriteFence); err != nil {
		t.Fatal(err)
	}
	select {
	case command := <-commands:
		if command != replySignalNoWriteFence {
			t.Fatalf("rejected parent reached native write: %q", command)
		}
	case <-time.After(time.Second):
		t.Fatal("reply-signal fixture did not reach ordered no-write fence")
	}
}
