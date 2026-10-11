package server

import (
	"fmt"
	"testing"
)

// BenchmarkNoteFailedAddress is the per-address bookkeeping for one failed
// credential, which runs under the server lock.
func BenchmarkNoteFailedAddress(b *testing.B) {
	full := func() *Server {
		s := New(Config{})
		for i := range maxFailureAddresses {
			s.noteFailedAddressLocked(fmt.Sprintf("10.0.%d.%d", i>>8, i&255))
		}
		return s
	}
	b.Run("known_address", func(b *testing.B) {
		s := full()
		b.ReportAllocs()
		for b.Loop() {
			s.noteFailedAddressLocked("10.0.0.1")
		}
	})
	b.Run("new_address_table_full", func(b *testing.B) {
		s := full()
		keys := make([]string, 1<<16)
		for i := range keys {
			keys[i] = fmt.Sprintf("11.0.%d.%d", i>>8, i&255)
		}
		b.ReportAllocs()
		i := 0
		for b.Loop() {
			s.noteFailedAddressLocked(keys[i%len(keys)])
			i++
		}
	})
}
