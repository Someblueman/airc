//go:build darwin || linux

package gateway_test

import (
	"context"
	"syscall"
	"testing"

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
			conn, stop, _ := startParentFD(t, binary, c, "socketpair", signal, false)
			writeFrame(t, conn, frameRequest{Version: 1, CorrelationID: "bad-auth", Profile: name, Request: "{}"})
			response := readFrame(t, conn)
			if response.CorrelationID != "bad-auth" || response.Error == nil || response.Error.Code != core.InvalidRequest || response.Result != nil {
				t.Fatal("socket-pair accepted a malformed request", response)
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
