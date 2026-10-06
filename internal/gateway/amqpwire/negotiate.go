package amqpwire

import (
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

var ErrNegotiation = errors.New("unsupported AMQP connection/session negotiation")

// Negotiation is the peer's initial flow state for the single-channel profile.
// It confers neither link authority nor credit. Subsequent flow and transfer
// processing must maintain these values with AMQP serial-number arithmetic.
type Negotiation struct {
	PeerFrameLimit       uint32
	PeerHandleMax        uint32
	NextIncoming         uint32
	RemoteIncomingWindow uint32
	RemoteOutgoingWindow uint32
}

// NegotiateSession follows successful authentication/header exchange. It accepts
// Open then a remotely initiated Begin on channel zero, and advertises one
// session with at most two handles. Callers own deadlines, cancellation, closure
// on error, and the socket lifetime. No request/link/delivery is accepted here.
func NegotiateSession(r io.Reader, w io.Writer, container string) (Negotiation, error) {
	if len(container) == 0 || len(container) > 128 || !utf8.ValidString(container) || strings.HasPrefix(container, "\ufeff") {
		return Negotiation{}, ErrNegotiation
	}
	open, err := readNegotiation(r, InitialFrameLimit)
	if err != nil || open.Kind != 0x10 || len(open.Fields[0].Data) == 0 || len(open.Fields[0].Data) > 128 {
		return Negotiation{}, ErrNegotiation
	}
	n := Negotiation{PeerFrameLimit: MaxFrameBytes, PeerHandleMax: 1}
	if v := negotiationField(open, 2); v.Code != 0x40 {
		n.PeerFrameLimit = min(n.PeerFrameLimit, uint32(v.Number))
	}
	// The runtime will send heartbeats at a bounded interval. Until that runtime
	// is wired, refuse nonzero peer timeouts shorter than the planned 1s cadence.
	if v := negotiationField(open, 4); v.Code != 0x40 && v.Number != 0 && v.Number < 2000 {
		return Negotiation{}, ErrNegotiation
	}
	if err := writeNegotiation(w, Performative{Kind: 0x10, Fields: []Value{{Code: 0xa1, Data: []byte(container)}, {Code: 0x40}, {Code: 0x70, Number: MaxFrameBytes}, {Code: 0x60}, {Code: 0x70, Number: 15000}}}, InitialFrameLimit); err != nil {
		return Negotiation{}, err
	}
	begin, err := readNegotiation(r, MaxFrameBytes)
	for err == nil && begin.Kind == 0 {
		begin, err = readNegotiation(r, MaxFrameBytes)
	}
	if err != nil || begin.Kind != 0x11 || negotiationField(begin, 0).Code != 0x40 {
		return Negotiation{}, ErrNegotiation
	}
	n.NextIncoming = uint32(begin.Fields[1].Number)
	n.RemoteIncomingWindow = uint32(begin.Fields[2].Number)
	n.RemoteOutgoingWindow = uint32(begin.Fields[3].Number)
	if v := negotiationField(begin, 4); v.Code != 0x40 {
		n.PeerHandleMax = min(n.PeerHandleMax, uint32(v.Number))
	}
	if err := writeNegotiation(w, Performative{Kind: 0x11, Fields: []Value{{Code: 0x60}, {Code: 0x70, Number: 1}, {Code: 0x70, Number: 8}, {Code: 0x70, Number: 8}, {Code: 0x70, Number: 1}}}, n.PeerFrameLimit); err != nil {
		return Negotiation{}, err
	}
	return n, nil
}

func negotiationField(p Performative, i int) Value {
	if i < len(p.Fields) {
		return p.Fields[i]
	}
	return Value{Code: 0x40}
}

func readNegotiation(r io.Reader, limit uint32) (Performative, error) {
	f, err := ReadFrame(r, limit)
	if err != nil {
		return Performative{}, err
	}
	if f.Channel != 0 {
		return Performative{}, ErrNegotiation
	}
	return DecodePerformative(f)
}

func writeNegotiation(w io.Writer, p Performative, limit uint32) error {
	f, err := EncodePerformative(p, 0, limit)
	if err != nil {
		return err
	}
	return WriteFrame(w, f, limit)
}
