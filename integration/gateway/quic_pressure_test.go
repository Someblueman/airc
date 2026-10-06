//go:build darwin || linux

package gateway_test

import (
	"time"

	"github.com/quic-go/quic-go/qlog"
	"github.com/quic-go/quic-go/qlogwriter"
)

type quicPressureEvent struct {
	at    time.Time
	kind  string
	limit int64
}

// The HTTP/3 fixture uses one application request stream (client stream 0).
// Peer blocked frames witness actual send-side flow control. Connection-level
// pressure includes small HTTP/3 control streams, so it is labelled separately.
type quicPressureTrace struct{ events chan quicPressureEvent }

func (t *quicPressureTrace) AddProducer() qlogwriter.Recorder { return t }
func (*quicPressureTrace) SupportsSchemas(string) bool        { return true }
func (*quicPressureTrace) Close() error                       { return nil }

func (t *quicPressureTrace) RecordEvent(event qlogwriter.Event) {
	packet, ok := event.(qlog.PacketReceived)
	if !ok {
		return
	}
	for _, observed := range packet.Frames {
		var pressure quicPressureEvent
		switch frame := observed.Frame.(type) {
		case *qlog.DataBlockedFrame:
			pressure = quicPressureEvent{at: time.Now(), kind: "connection", limit: int64(frame.MaximumData)}
		case *qlog.StreamDataBlockedFrame:
			if frame.StreamID != 0 {
				continue
			}
			pressure = quicPressureEvent{at: time.Now(), kind: "request stream", limit: int64(frame.MaximumStreamData)}
		default:
			continue
		}
		select {
		case t.events <- pressure:
		default:
		}
	}
}
