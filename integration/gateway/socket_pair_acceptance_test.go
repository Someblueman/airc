//go:build darwin || linux

package gateway_test

import (
	"context"
	"syscall"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestSocketPairAuthenticatedSendAndBoundedResume(t *testing.T) {
	binary := currentGatewayBinary(t)
	for _, encrypted := range []bool{false, true} {
		name, signal := "signed", syscall.SIGINT
		if encrypted {
			name, signal = "encrypted", syscall.SIGTERM
		}
		t.Run(name, func(t *testing.T) {
			socket, c, credentials, observer := stdioFixtureConfig(t)
			observer.Close()
			conn, stop := startSocketPair(t, binary, c, signal)
			writeFrame(t, conn, adapter.FrameRequest{Version: 1, CorrelationID: "bad-auth", Profile: name, Envelope: "invalid"})
			response := readFrame(t, conn)
			if response.CorrelationID != "bad-auth" || response.Error == nil || response.Error.Code != core.AuthenticationFailed || response.Result != nil {
				t.Fatal("socket-pair accepted invalid authentication", response)
			}
			framedAuthenticatedSendAndBoundedResume(t, conn, credentials, socket, encrypted)
			stop()
			journal, err := store.Open(context.Background(), c)
			if err != nil {
				t.Fatal("signal shutdown retained journal ownership", err)
			}
			if err := journal.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
