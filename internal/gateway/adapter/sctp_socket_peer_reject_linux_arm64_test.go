//go:build linux && arm64

package adapter

import (
	"errors"
	"testing"
)

func TestSCTPRejectsUnorderedAndNonzeroPPIDFromPeer(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags uint16
		ppid  bool
	}{
		{name: "unordered", flags: sctpMessageUnordered},
		{name: "nonzero ppid", ppid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener, err := ListenSCTP("127.0.0.1:0")
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
