package amqpwire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

func TestFrameIndependentVectorsAndBoundaries(t *testing.T) {
	// Extended AMQP header, nonzero channel, opaque performative bytes; then
	// an empty heartbeat. Independent literals do not use the writer under test.
	wire := []byte{0, 0, 0, 15, 3, 0, 0, 9, 0xaa, 0xbb, 0xcc, 0xdd, 0, 0x53, 0x18}
	heartbeat := []byte{0, 0, 0, 8, 2, 0, 0, 7}
	r := bytes.NewReader(append(append([]byte{}, wire...), heartbeat...))
	f, err := ReadFrame(r, InitialFrameLimit)
	if err != nil || f.Channel != 9 || f.Type != AMQP || !bytes.Equal(f.Body, []byte{0, 0x53, 0x18}) || r.Len() != 8 {
		t.Fatalf("frame=%+v remaining=%d err=%v", f, r.Len(), err)
	}
	f, err = ReadFrame(r, InitialFrameLimit)
	if err != nil || f.Channel != 7 || len(f.Body) != 0 || r.Len() != 0 {
		t.Fatalf("heartbeat=%+v err=%v", f, err)
	}
	for n := 0; n < len(wire); n++ {
		if _, err := ReadFrame(bytes.NewReader(wire[:n]), InitialFrameLimit); err == nil {
			t.Fatalf("accepted truncated prefix %d", n)
		}
	}
	// SASL reserved bytes and extended header are ignored, not channel grants.
	sasl := []byte{0, 0, 0, 15, 3, 1, 0xff, 0xff, 1, 2, 3, 4, 0, 0x53, 0x44}
	f, err = ReadFrame(bytes.NewReader(sasl), InitialFrameLimit)
	if err != nil || f.Channel != 0 || f.Type != SASL || !bytes.Equal(f.Body, sasl[12:]) {
		t.Fatalf("SASL=%+v err=%v", f, err)
	}
}

func TestFrameRefusesHeaderBeforeBodyRead(t *testing.T) {
	for _, h := range [][]byte{
		{0, 0, 0, 7, 2, 0, 0, 0}, // undersized
		{0, 0, 2, 1, 2, 0, 0, 0}, // above pre-open bound
		{0xff, 0xff, 0xff, 0xff, 2, 0, 0, 0},
		{0, 0, 0, 8, 1, 0, 0, 0}, // offset below header
		{0, 0, 0, 8, 3, 0, 0, 0}, // offset beyond size
		{0, 0, 0, 8, 2, 2, 0, 0}, // unknown type
		{0, 0, 0, 8, 2, 1, 0, 0}, // empty SASL
	} {
		r := bytes.NewReader(append(append([]byte{}, h...), 0x99))
		if _, err := ReadFrame(r, InitialFrameLimit); !errors.Is(err, ErrFrame) || r.Len() != 1 {
			t.Fatalf("header=%x remaining=%d err=%v", h, r.Len(), err)
		}
	}
	for _, limit := range []uint32{0, 511, MaxFrameBytes + 1, ^uint32(0)} {
		r := bytes.NewReader([]byte{1})
		if _, err := ReadFrame(r, limit); !errors.Is(err, ErrFrame) || r.Len() != 1 {
			t.Fatalf("invalid limit %d consumed input", limit)
		}
	}
}

func TestFrameExactLimitsAndIndependentWrite(t *testing.T) {
	for _, kind := range []byte{AMQP, SASL} {
		limit := uint32(MaxFrameBytes)
		if kind == SASL {
			limit = InitialFrameLimit
		}
		wire := make([]byte, limit)
		binary.BigEndian.PutUint32(wire, limit)
		wire[4], wire[5] = 2, kind
		for i := 8; i < len(wire); i++ {
			wire[i] = byte(i)
		}
		f, err := ReadFrame(bytes.NewReader(wire), MaxFrameBytes)
		if err != nil || !bytes.Equal(f.Body, wire[8:]) {
			t.Fatalf("exact limit kind=%d err=%v", kind, err)
		}
		var output bytes.Buffer
		if err := WriteFrame(&output, f, MaxFrameBytes); err != nil || !bytes.Equal(output.Bytes(), wire) {
			t.Fatalf("write kind=%d err=%v", kind, err)
		}
		f.Body = append(f.Body, 1)
		output.Reset()
		if err := WriteFrame(&output, f, MaxFrameBytes); !errors.Is(err, ErrFrame) || output.Len() != 0 {
			t.Fatalf("oversize kind=%d err=%v", kind, err)
		}
		binary.BigEndian.PutUint32(wire, limit+1)
		if _, err := ReadFrame(bytes.NewReader(wire), limit); !errors.Is(err, ErrFrame) {
			t.Fatalf("oversize read kind=%d err=%v", kind, err)
		}
	}
}

type byteReader struct{ io.Reader }

func (r byteReader) Read(p []byte) (int, error) { return r.Reader.Read(p[:1]) }

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestFrameShortIOAndWriteRefusals(t *testing.T) {
	wire := []byte{0, 0, 0, 11, 2, 0, 0, 1, 0, 0x53, 0x18}
	if _, err := ReadFrame(byteReader{bytes.NewReader(wire)}, InitialFrameLimit); err != nil {
		t.Fatal(err)
	}
	if err := WriteFrame(shortWriter{}, Frame{}, InitialFrameLimit); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal(err)
	}
	for _, f := range []Frame{{Type: 2}, {Type: SASL}, {Type: SASL, Channel: 1, Body: []byte{1}}} {
		var output bytes.Buffer
		if err := WriteFrame(&output, f, InitialFrameLimit); !errors.Is(err, ErrFrame) || output.Len() != 0 {
			t.Fatalf("invalid frame=%+v err=%v", f, err)
		}
	}
}

func FuzzReadFrame(f *testing.F) {
	f.Add([]byte{0, 0, 0, 8, 2, 0, 0, 0})
	f.Add([]byte{0, 0, 0, 11, 2, 1, 0xff, 0xff, 0, 0x53, 0x44})
	f.Fuzz(func(t *testing.T, wire []byte) {
		frame, err := ReadFrame(bytes.NewReader(wire), MaxFrameBytes)
		if err == nil && (len(frame.Body) > MaxFrameBytes-8 || frame.Type > SASL || frame.Type == SASL && (len(frame.Body) == 0 || frame.Channel != 0)) {
			t.Fatalf("unbounded/invalid frame=%+v", frame)
		}
	})
}
