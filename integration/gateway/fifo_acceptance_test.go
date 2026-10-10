//go:build darwin || linux

package gateway_test

import (
	"syscall"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestFIFOAuthenticatedSendAndBoundedResume(t *testing.T) {
	binary := currentGatewayBinary(t)
	for _, encrypted := range []bool{false, true} {
		name, signal := "signed", syscall.SIGINT
		if encrypted {
			name, signal = "encrypted", syscall.SIGTERM
		}
		t.Run(name, func(t *testing.T) {
			socket, c, credentials, observer := stdioFixtureConfig(t)
			observer.Close()
			c = configureFIFOFixture(t, c)
			stream, stop, _ := startFIFO(t, binary, c, signal, false)
			writeFrame(t, stream, adapter.FrameRequest{Version: 1, CorrelationID: "bad-auth", Profile: name, Envelope: "invalid"})
			response := readFrame(t, stream)
			if response.CorrelationID != "bad-auth" || response.Error == nil || response.Error.Code != core.AuthenticationFailed || response.Result != nil {
				t.Fatal("FIFO accepted invalid authentication", response)
			}
			framedAuthenticatedSendAndBoundedResume(t, stream, credentials, socket, encrypted)
			stop()
			journal, err := store.Open(t.Context(), c)
			if err != nil {
				t.Fatal("FIFO signal shutdown retained journal ownership", err)
			}
			if err := journal.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
