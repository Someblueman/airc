//go:build unix

package auth

import (
	"sync"
	"testing"

	"github.com/Someblueman/airc/internal/testsnmp"
)

func TestSNMPConcurrentImmutableVerification(t *testing.T) {
	v, o, p, k := snmpFixture(t, true)
	a, err := NewSNMPAuthenticator(v, o)
	if err != nil {
		t.Fatal(err)
	}
	raw := testsnmp.Encode(t, p, k)
	var workers sync.WaitGroup
	for range 12 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 10 {
				n, err := a.Verify(raw)
				if err != nil || !v.OwnsIdentity(n.Identity()) || n.Body() != "SNMP Alert\nMessage: 世界\nbody" {
					t.Error("concurrent verification", err)
					return
				}
				packet := n.Packet()
				clear(packet.Raw)
			}
		}()
	}
	workers.Wait()
}
