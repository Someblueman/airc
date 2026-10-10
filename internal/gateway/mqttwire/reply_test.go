package mqttwire

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

type shortWriter struct {
	bytes.Buffer
	stall bool
}

func (w *shortWriter) Write(p []byte) (int, error) {
	if w.stall {
		return 0, nil
	}
	return w.Buffer.Write(p[:min(1, len(p))])
}
func TestRepliesAndShortWrites(t *testing.T) {
	w := &shortWriter{}
	if err := WriteConnack(w, 0); err != nil {
		t.Fatal(err)
	}
	if err := WritePuback(w, 65535); err != nil {
		t.Fatal(err)
	}
	if err := WritePingresp(w); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(w.Bytes(), []byte{0x20, 2, 0, 0, 0x40, 2, 0xff, 0xff, 0xd0, 0}) {
		t.Fatal(w.Bytes())
	}
	before := w.Len()
	if WriteConnack(w, 6) == nil || WritePuback(w, 0) == nil || w.Len() != before {
		t.Fatal("invalid reply emitted")
	}
	w.stall = true
	if err := WritePingresp(w); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal("zero write did not fail", err)
	}
}
