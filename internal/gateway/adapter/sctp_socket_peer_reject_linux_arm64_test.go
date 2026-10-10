//go:build linux && arm64

package adapter

import (
	"errors"
	"testing"
)

func TestSCTPRejectsUnorderedAndNonzeroPPIDFromPeer(t *testing.T) {
	for _, tc := range []struct {
		name         string
		listenerMode SCTPMode
		flags        uint16
		ppid         bool
	}{
		{name: "ordered listener rejects unordered", listenerMode: SCTPOrdered, flags: sctpMessageUnordered},
		{name: "ordered listener rejects nonzero ppid", listenerMode: SCTPOrdered, ppid: true},
		{name: "unordered listener rejects ordered", listenerMode: SCTPUnordered},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener, err := ListenSCTP("127.0.0.1:0", tc.listenerMode)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			client := sctpTestClient(t, listener.Addr())
			defer client.close()
			accepted := acceptSCTPTestConn(t, listener)
			defer accepted.Close()
			client.sendInfo(t, []byte("invalid-mode"), tc.flags, tc.ppid)
			if _, err := accepted.Read(make([]byte, 64)); !errors.Is(err, errSCTPInvalidRecord) {
				t.Fatalf("invalid metadata read error = %v", err)
			}
		})
	}
}
