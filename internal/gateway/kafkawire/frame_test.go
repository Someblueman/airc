package kafkawire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

func responseWire(correlation int32, body []byte) []byte {
	wire := make([]byte, 8+len(body))
	binary.BigEndian.PutUint32(wire, uint32(4+len(body)))
	binary.BigEndian.PutUint32(wire[4:], uint32(correlation))
	copy(wire[8:], body)
	return wire
}

func TestKafkaRequestExactHeaderAndBounds(t *testing.T) {
	body := []byte{0, 255, 1}
	wire, err := Request(1, 11, 42, body)
	expected := []byte{0, 0, 0, 25, 0, 1, 0, 11, 0, 0, 0, 42, 0, 12, 'a', 'i', 'r', 'c', '-', 'g', 'a', 't', 'e', 'w', 'a', 'y', 0, 255, 1}
	if err != nil || !bytes.Equal(wire, expected) {
		t.Fatal("request bytes", wire, err)
	}
	body[0] = 9
	if wire[len(wire)-3] != 0 {
		t.Fatal("request aliases input")
	}
	maximum := make([]byte, MaxFrame-10-len(ClientID))
	if wire, err := Request(0, 7, 0, maximum); err != nil || len(wire) != MaxFrame+4 {
		t.Fatal("exact bound", err)
	}
	if wire, err := Request(0, 7, 0, append(maximum, 0)); !errors.Is(err, ErrFrame) || wire != nil {
		t.Fatal("oversize accepted")
	}
	for _, v := range []struct {
		api, version int16
		correlation  int32
	}{{-1, 0, 0}, {0, -1, 0}, {0, 0, -1}} {
		if wire, err := Request(v.api, v.version, v.correlation, nil); !errors.Is(err, ErrFrame) || wire != nil {
			t.Fatal("invalid header")
		}
	}
}

func TestKafkaResponseFragmentationPipelineAndBounds(t *testing.T) {
	first, second := []byte{0, 255, 2}, []byte("next")
	reader := bytes.NewReader(append(responseWire(9, first), responseWire(10, second)...))
	body, err := Response(reader, 9)
	if err != nil || !bytes.Equal(body, first) {
		t.Fatal(err)
	}
	body[0] = 8
	next, err := Response(reader, 10)
	if err != nil || !bytes.Equal(next, second) || reader.Len() != 0 {
		t.Fatal("pipeline", err)
	}
	fragmented := &oneByteReader{bytes.NewReader(responseWire(1, first))}
	if got, err := Response(fragmented, 1); err != nil || !bytes.Equal(got, first) {
		t.Fatal("fragmented", err)
	}
	if got, err := Response(bytes.NewReader(responseWire(1, make([]byte, MaxFrame-4))), 1); err != nil || len(got) != MaxFrame-4 {
		t.Fatal("maximum", err)
	}
	if got, err := Response(bytes.NewReader(responseWire(0, nil)), 0); err != nil || len(got) != 0 {
		t.Fatal("empty body", err)
	}
}

type oneByteReader struct{ r *bytes.Reader }

func (r *oneByteReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.r.Read(p)
}

func TestKafkaResponseRefusesBeforeReadingPayload(t *testing.T) {
	for _, size := range []uint32{0, 1, 3, MaxFrame + 1, 0xffffffff, 0x80000000} {
		header := make([]byte, 4)
		binary.BigEndian.PutUint32(header, size)
		reader := bytes.NewReader(append(header, []byte("untouched")...))
		if got, err := Response(reader, 1); !errors.Is(err, ErrFrame) || got != nil || reader.Len() != 9 {
			t.Fatal("invalid length consumed payload", size, err)
		}
	}
	reader := bytes.NewReader(responseWire(2, []byte("payload")))
	if got, err := Response(reader, 1); !errors.Is(err, ErrFrame) || got != nil || reader.Len() != 7 {
		t.Fatal("correlation consumed payload", err)
	}
	for n := 0; n < 11; n++ {
		wire := responseWire(1, []byte{0, 1, 2})
		if got, err := Response(bytes.NewReader(wire[:n]), 1); err == nil || got != nil {
			t.Fatal("truncation", n)
		}
	}
	if _, err := Response(nil, 1); !errors.Is(err, ErrFrame) {
		t.Fatal("nil reader")
	}
	if _, err := Response(bytes.NewReader(nil), -1); !errors.Is(err, ErrFrame) {
		t.Fatal("negative correlation")
	}
	if _, err := Response(bytes.NewReader(nil), 0); !errors.Is(err, io.EOF) {
		t.Fatal("EOF", err)
	}
}
