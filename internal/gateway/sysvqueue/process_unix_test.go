//go:build (darwin || linux) && arm64

package sysvqueue

import (
	"os"
	"os/exec"
	"strconv"
	"testing"
)

func TestQueueChildProducer(t *testing.T) {
	key := os.Getenv("AIRC_SYSV_TEST_CHILD_KEY")
	if key == "" {
		t.Skip("owned child only")
	}
	parsed, err := strconv.ParseInt(key, 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	q, err := Open(int32(parsed))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	body := make([]byte, MessageBytes)
	for i := range body {
		body[i] = byte(i)
	}
	if err := q.WriteMessage(t.Context(), body); err != nil {
		t.Fatal(err)
	}
}

func TestSeparateProcessProducerPreservesWholePacket(t *testing.T) {
	q := fixture(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(t.Context(), executable, "-test.run=^TestQueueChildProducer$", "-test.count=1")
	child.Env = append(os.Environ(), "AIRC_SYSV_TEST_CHILD_KEY="+strconv.FormatInt(int64(q.Identity().Key), 10))
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	body := make([]byte, MessageBytes)
	n, err := q.ReadMessage(t.Context(), body)
	if err != nil || n != MessageBytes {
		t.Fatal(n, err)
	}
	for i, b := range body {
		if b != byte(i) {
			t.Fatal("child packet changed", i)
		}
	}
}
