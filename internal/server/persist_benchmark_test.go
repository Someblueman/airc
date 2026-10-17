package server

import (
	"bufio"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/atomicfile"
)

// BenchmarkPersistedPosts measures confirmed direct messages against a
// history file, with senders posting concurrently. Each post waits for its
// 762 receipt, so the result is end-to-end durable throughput.
func BenchmarkPersistedPosts(b *testing.B) {
	for _, mode := range []struct {
		name string
		sync atomicfile.Sync
	}{{"full", atomicfile.SyncFull}, {"none", atomicfile.SyncNone}} {
		for _, senders := range []int{1, 16, 64} {
			b.Run(fmt.Sprintf("sync=%s/senders=%d", mode.name, senders), func(b *testing.B) {
				path := filepath.Join(b.TempDir(), "history.jsonl")
				address, _ := startServer(b, Config{HistoryLimit: 10000, Sync: mode.sync, PingInterval: time.Hour, ReadTimeout: time.Hour},
					func(s *Server) error { return s.RestoreHistory(path) })
				type sender struct {
					conn   net.Conn
					reader *bufio.Reader
				}
				all := make([]sender, senders)
				for i := range all {
					conn, err := net.Dial("tcp", address)
					if err != nil {
						b.Fatal(err)
					}
					b.Cleanup(func() { _ = conn.Close() })
					fmt.Fprintf(conn, "NICK s%d\r\nUSER s 0 * :s\r\n", i)
					all[i] = sender{conn, bufio.NewReader(conn)}
					if err := readFor(all[i].reader, " 001 "); err != nil {
						b.Fatal(err)
					}
				}
				var left atomic.Int64
				left.Store(int64(b.N))
				var wg sync.WaitGroup
				errs := make(chan error, senders)
				b.ResetTimer()
				for i := range all {
					wg.Go(func() {
						for left.Add(-1) >= 0 {
							fmt.Fprintf(all[i].conn, "PRIVMSG absent :status from s%d\r\n", i)
							if err := readFor(all[i].reader, " 762 "); err != nil {
								errs <- err
								return
							}
						}
					})
				}
				wg.Wait()
				b.StopTimer()
				close(errs)
				for err := range errs {
					b.Fatal(err)
				}
			})
		}
	}
}

func readFor(reader *bufio.Reader, want string) error {
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		if strings.Contains(line, want) {
			return nil
		}
	}
}
