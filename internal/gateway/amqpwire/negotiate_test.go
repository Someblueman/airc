package amqpwire

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io"
	"testing"
)

func TestNegotiationIndependentWire(t *testing.T) {
	// Compact Open(max-frame-size=512) and Begin(next-outgoing=0xffffffff,
	// windows 2/3, handle-max=0), independently spelled wire vectors.
	input, err := hex.DecodeString("0000001c02000000005310c00f03a106636c69656e744070000002000000001902000000005311c00c054070ffffffff5202520343")
	if err != nil {
		t.Fatal(err)
	}
	var w bytes.Buffer
	n, err := NegotiateSession(bytes.NewReader(input), &w, "server")
	if err != nil || n.PeerFrameLimit != 512 || n.PeerHandleMax != 0 || n.NextIncoming != 0xffffffff {
		t.Fatal(n, err)
	}
}

func TestNegotiationIOAndChannelBounds(t *testing.T) {
	open, begin := initialNegotiation()
	input := negotiationInput(t, open, begin)
	openOnly := negotiationInput(t, open, Performative{})
	openSize := len(openOnly) - 8
	for _, offset := range []int{7, openSize + 7} {
		bad := append([]byte(nil), input...)
		bad[offset] = 1
		var w bytes.Buffer
		if _, err := NegotiateSession(bytes.NewReader(bad), &w, "server"); err == nil {
			t.Fatal("accepted channel1")
		}
	}
	r := bytes.NewReader(input)
	if _, err := NegotiateSession(r, &headerWriter{n: 1}, "server"); !errors.Is(err, io.ErrShortWrite) || r.Len() != len(input)-openSize {
		t.Fatal("write failure consumed Begin", err, r.Len())
	}
	for _, container := range []string{"", "\xff", "\ufeffserver"} {
		r := &headerReader{}
		var w bytes.Buffer
		if _, err := NegotiateSession(r, &w, container); err == nil || r.calls != 0 || w.Len() != 0 {
			t.Fatal("invalid container touched transport")
		}
	}
}

func TestNegotiationHeartbeatAndDefaults(t *testing.T) {
	open, begin := initialNegotiation()
	open.Fields = open.Fields[:1]
	begin.Fields = begin.Fields[:4]
	var input, w bytes.Buffer
	for _, p := range []Performative{open, {}, begin} {
		if err := writeNegotiation(&input, p, MaxFrameBytes); err != nil {
			t.Fatal(err)
		}
	}
	n, err := NegotiateSession(&input, &w, "server")
	if err != nil || n.PeerFrameLimit != MaxFrameBytes || n.PeerHandleMax != 1 {
		t.Fatal(n, err)
	}
}

func negotiationInput(t *testing.T, open, begin Performative) []byte {
	t.Helper()
	var b bytes.Buffer
	for _, p := range []Performative{open, begin} {
		if err := writeNegotiation(&b, p, MaxFrameBytes); err != nil {
			t.Fatal(err)
		}
	}
	return b.Bytes()
}

func initialNegotiation() (Performative, Performative) {
	return Performative{Kind: 0x10, Fields: []Value{{Code: 0xa1, Data: []byte("client")}, {Code: 0x40}, {Code: 0x70, Number: 512}}}, Performative{Kind: 0x11, Fields: []Value{{Code: 0x40}, {Code: 0x70, Number: 0xffffffff}, {Code: 0x70, Number: 2}, {Code: 0x70, Number: 3}, {Code: 0x70}}}
}

func TestNegotiateSession(t *testing.T) {
	open, begin := initialNegotiation()
	r := bytes.NewReader(append(negotiationInput(t, open, begin), 0x99))
	var w bytes.Buffer
	n, err := NegotiateSession(r, &w, "server")
	if err != nil || n != (Negotiation{PeerFrameLimit: 512, PeerHandleMax: 0, NextIncoming: 0xffffffff, RemoteIncomingWindow: 2, RemoteOutgoingWindow: 3}) || r.Len() != 1 {
		t.Fatal(n, err, r.Len())
	}
	response := bytes.NewReader(w.Bytes())
	p, err := readNegotiation(response, 512)
	if err != nil || p.Kind != 0x10 || string(p.Fields[0].Data) != "server" || p.Fields[2].Number != MaxFrameBytes || p.Fields[3].Number != 0 || p.Fields[4].Number != 15000 {
		t.Fatal(p, err)
	}
	p, err = readNegotiation(response, 512)
	if err != nil || p.Kind != 0x11 || p.Fields[0].Code != 0x60 || p.Fields[0].Number != 0 || p.Fields[1].Number != 1 || p.Fields[2].Number != 8 || p.Fields[3].Number != 8 || p.Fields[4].Number != 1 || response.Len() != 0 {
		t.Fatal(p, err)
	}
}

func TestNegotiateSessionRefusals(t *testing.T) {
	for _, kind := range []string{"heartbeat", "begin_first", "mapped_begin", "empty_container", "idle", "second_open", "truncated"} {
		t.Run(kind, func(t *testing.T) {
			open, begin := initialNegotiation()
			switch kind {
			case "heartbeat":
				open = Performative{}
			case "begin_first":
				open = begin
			case "mapped_begin":
				begin.Fields[0] = Value{Code: 0x60}
			case "empty_container":
				open.Fields[0].Data = nil
			case "idle":
				open.Fields = append(open.Fields, Value{Code: 0x40}, Value{Code: 0x70, Number: 1000})
			case "second_open":
				begin = open
			}
			input := negotiationInput(t, open, begin)
			if kind == "truncated" {
				input = input[:len(input)-1]
			}
			var w bytes.Buffer
			if n, err := NegotiateSession(bytes.NewReader(input), &w, "server"); err == nil || n != (Negotiation{}) {
				t.Fatal("accepted unsupported negotiation", n, err)
			}
		})
	}
	var w bytes.Buffer
	if _, err := NegotiateSession(bytes.NewReader(nil), &w, ""); err == nil || w.Len() != 0 {
		t.Fatal("invalid local container touched transport")
	}
}
