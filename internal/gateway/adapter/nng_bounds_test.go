//go:build darwin || linux

package adapter

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/nngwire"
)

func TestNNGInvalidLengthsNeverAdmit(t *testing.T) {
	for _, size := range []uint64{0, 4, nngwire.MaxData + 5, ^uint64(0)} {
		t.Run(time.Duration(size).String(), func(t *testing.T) {
			f := newLocalREPFixture(t, "nng")
			server, client := net.Pipe()
			defer client.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			requests := make(chan struct{}, 1)
			done := make(chan struct{})
			go func() { serveNNGConnection(ctx, server, f.d, requests); close(done) }()
			nngClient(t, client)
			if _, err := client.Write(binary.BigEndian.AppendUint64(nil, size)); err != nil {
				t.Fatal(err)
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("invalid frame retained owner")
			}
			if len(requests) != 0 {
				t.Fatal("invalid frame admitted")
			}
			if _, err := f.lookup(t, mqttSNRequest().RequestID); err == nil {
				t.Fatal("invalid frame journaled")
			}
		})
	}
}
