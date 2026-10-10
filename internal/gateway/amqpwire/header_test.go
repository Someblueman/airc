package amqpwire

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestProtocolHeaderExchange(t *testing.T) {
	for _, protocol := range []byte{AMQPProtocol, SASLProtocol} {
		wire := []byte{'A', 'M', 'Q', 'P', protocol, 1, 0, 0}
		r := bytes.NewReader(append(append([]byte(nil), wire...), 0x99))
		var w bytes.Buffer
		if err := ExchangeProtocolHeader(r, &w, protocol); err != nil || !bytes.Equal(w.Bytes(), wire) || r.Len() != 1 {
			t.Fatalf("protocol=%d: %x remaining=%d err=%v", protocol, w.Bytes(), r.Len(), err)
		}
		for i := range wire {
			bad := append([]byte(nil), wire...)
			bad[i] ^= 1
			w.Reset()
			if err := ExchangeProtocolHeader(bytes.NewReader(bad), &w, protocol); !errors.Is(err, ErrHeader) || !bytes.Equal(w.Bytes(), wire) {
				t.Fatalf("mismatch byte=%d: %x %v", i, w.Bytes(), err)
			}
		}
		for i := range wire {
			w.Reset()
			err := ExchangeProtocolHeader(bytes.NewReader(wire[:i]), &w, protocol)
			if !errors.Is(err, ErrHeader) || !bytes.Equal(w.Bytes(), wire) || !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("truncation=%d: %x %v", i, w.Bytes(), err)
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

func TestProtocolHeaderIOFailures(t *testing.T) {
	r := &headerReader{}
	w := &headerWriter{n: 7}
	if err := ExchangeProtocolHeader(r, w, SASLProtocol); !errors.Is(err, io.ErrShortWrite) || r.calls != 0 {
		t.Fatal(err, r.calls)
	}
	w = &headerWriter{err: io.ErrClosedPipe}
	if err := ExchangeProtocolHeader(r, w, AMQPProtocol); !errors.Is(err, io.ErrClosedPipe) || r.calls != 0 {
		t.Fatal(err, r.calls)
	}
	w = &headerWriter{}
	if err := ExchangeProtocolHeader(r, w, 2); !errors.Is(err, ErrHeader) || r.calls != 0 || w.calls != 0 {
		t.Fatal(err, r.calls, w.calls)
	}
}

func FuzzProtocolHeader(f *testing.F) {
	f.Add([]byte{'A', 'M', 'Q', 'P', 3, 1, 0, 0})
	f.Add([]byte("AMQP\x00\x01\x00\x00next"))
	f.Fuzz(func(t *testing.T, input []byte) {
		for _, protocol := range []byte{AMQPProtocol, SASLProtocol} {
			r := bytes.NewReader(input)
			var w bytes.Buffer
			err := ExchangeProtocolHeader(r, &w, protocol)
			want := []byte{'A', 'M', 'Q', 'P', protocol, 1, 0, 0}
			valid := len(input) >= 8 && bytes.Equal(input[:8], want)
			remaining := max(0, len(input)-8)
			if (err == nil) != valid || r.Len() != remaining || !bytes.Equal(w.Bytes(), want) {
				t.Fatal("header boundary or negotiation mismatch")
			}
		}
	})
}
