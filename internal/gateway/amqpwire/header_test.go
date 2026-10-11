package amqpwire

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestProtocolHeaderReadWrite(t *testing.T) {
	for _, protocol := range []byte{AMQPProtocol, SASLProtocol} {
		wire := []byte{'A', 'M', 'Q', 'P', protocol, 1, 0, 0}
		var w bytes.Buffer
		if err := WriteProtocolHeader(&w, protocol); err != nil || !bytes.Equal(w.Bytes(), wire) {
			t.Fatalf("protocol=%d: wrote %x %v", protocol, w.Bytes(), err)
		}
		r := bytes.NewReader(append(append([]byte(nil), wire...), 0x99))
		if got, err := ReadProtocolHeader(r); err != nil || got != protocol || r.Len() != 1 {
			t.Fatalf("protocol=%d: read %d remaining=%d err=%v", protocol, got, r.Len(), err)
		}
		for i := range wire {
			bad := append([]byte(nil), wire...)
			bad[i] ^= 1
			if _, err := ReadProtocolHeader(bytes.NewReader(bad)); !errors.Is(err, ErrHeader) {
				t.Fatalf("mismatch byte=%d: %v", i, err)
			}
		}
		for i := range wire {
			_, err := ReadProtocolHeader(bytes.NewReader(wire[:i]))
			if !errors.Is(err, ErrHeader) || !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("truncation=%d: %v", i, err)
			}
		}
	}
}

type headerReader struct{ calls int }

func (r *headerReader) Read([]byte) (int, error) { r.calls++; return 0, io.EOF }

type headerWriter struct {
	err   error
	n     int
	calls int
}

func (w *headerWriter) Write([]byte) (int, error) { w.calls++; return w.n, w.err }

func TestProtocolHeaderWriteFailures(t *testing.T) {
	if err := WriteProtocolHeader(&headerWriter{n: 7}, SASLProtocol); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal(err)
	}
	if err := WriteProtocolHeader(&headerWriter{err: io.ErrClosedPipe}, AMQPProtocol); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
	w := &headerWriter{}
	if err := WriteProtocolHeader(w, 2); !errors.Is(err, ErrHeader) || w.calls != 0 {
		t.Fatal(err, w.calls)
	}
}

func FuzzProtocolHeader(f *testing.F) {
	f.Add([]byte{'A', 'M', 'Q', 'P', 3, 1, 0, 0})
	f.Add([]byte("AMQP\x00\x01\x00\x00next"))
	f.Fuzz(func(t *testing.T, input []byte) {
		r := bytes.NewReader(input)
		protocol, err := ReadProtocolHeader(r)
		valid := len(input) >= 8 && (bytes.Equal(input[:8], []byte{'A', 'M', 'Q', 'P', AMQPProtocol, 1, 0, 0}) || bytes.Equal(input[:8], []byte{'A', 'M', 'Q', 'P', SASLProtocol, 1, 0, 0}))
		if (err == nil) != valid || r.Len() != max(0, len(input)-8) || valid && protocol != input[4] {
			t.Fatal("header boundary or protocol mismatch")
		}
	})
}
