//go:build darwin || linux

package adapter

import (
	"errors"
	"io"
	"testing"
	"time"
)

func TestFIFOBlockedReadSeesWriterClose(t *testing.T) {
	f, root, _ := fifoFixture(t)
	writer := fifoWriter(t, root)
	if _, err := writer.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if n, err := f.Read(make([]byte, 1)); n != 1 || err != nil {
		t.Fatal(n, err)
	}
	if err := f.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := f.Read(make([]byte, 1)); done <- err }()
	timer := time.NewTimer(20 * time.Millisecond)
	<-timer.C
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("blocked close: %v", err)
		}
	case <-time.After(2 * time.Second):
		_ = f.Close()
		<-done
		t.Fatal("writer close did not wake read")
	}
}
