package main

import (
	"bufio"
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

// BenchmarkAgentCLI includes process startup, TCP, SASL, local state and disk
// persistence. AIRC_BENCH_BINARY permits the same fixture to measure an older CLI.
func BenchmarkAgentCLI(b *testing.B) {
	b.Setenv("AIRC_STATE_DIR", b.TempDir())
	b.Setenv("AIRC_NICK", "")
	b.Setenv("AIRC_CHANNEL", "")
	binary := os.Getenv("AIRC_BENCH_BINARY")
	if binary == "" {
		binary = filepath.Join(b.TempDir(), "airc")
		if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
			b.Fatal(err, string(out))
		}
	}
	dir := b.TempDir()
	address := cliTestServerSetup(b, server.Config{HistoryLimit: 128}, func(s *server.Server) error {
		if err := s.RestoreAccounts(filepath.Join(dir, "accounts")); err != nil {
			return err
		}
		return s.RestoreHistory(filepath.Join(dir, "history"))
	})
	run := func(args ...string) {
		cmd := exec.Command(binary, append(args, "--addr", address, "--nick", "bench", "--json")...)
		if out, err := cmd.CombinedOutput(); err != nil {
			b.Fatal(err, string(out))
		}
	}
	run("user", "create")
	for _, test := range []struct {
		name string
		args []string
	}{
		{"login", []string{"user", "login"}},
		{"send", []string{"send", "--channel", "one", "--message", "benchmark post"}},
		{"empty_check_four_rooms", []string{"check", "--channel", "one,two,three,four"}},
	} {
		b.Run(test.name, func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				run(test.args...)
			}
		})
	}
	b.Run("wait_wake", func(b *testing.B) {
		ready, proxy := benchmarkWaitProxy(b, address)
		sender, err := irc.Dial(irc.Config{Nick: "emitter", Addr: address, Ephemeral: true})
		if err != nil {
			b.Fatal(err)
		}
		defer sender.Close()
		b.ResetTimer()
		b.StopTimer()
		for i := 0; i < b.N; i++ {
			cmd := exec.Command(binary, "check", "--addr", proxy, "--nick", "waiter", "--mentions", "--wait", "5s", "--json")
			cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
			if err := cmd.Start(); err != nil {
				b.Fatal(err)
			}
			select {
			case <-ready:
			case <-time.After(5 * time.Second):
				cmd.Process.Kill()
				cmd.Wait()
				b.Fatal("wait never completed its initial snapshot")
			}
			b.StartTimer()
			if err := sender.Send("waiter", "wake"); err != nil {
				b.Fatal(err)
			}
			if err := cmd.Wait(); err != nil {
				b.Fatal(err)
			}
			b.StopTimer()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			if _, err := awaitSend(ctx, sender, "emitter", "waiter", "wake", "", "", ""); err != nil {
				b.Fatal(err)
			}
			cancel()
		}
	})
}

// Signal only after the initial snapshot's end marker crosses the wire. The
// measured wake excludes wait startup and uses the same proxy for both versions.
func benchmarkWaitProxy(b *testing.B, address string) (<-chan struct{}, string) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { l.Close() })
	ready := make(chan struct{}, 1)
	go func() {
		for {
			front, err := l.Accept()
			if err != nil {
				return
			}
			back, err := net.Dial("tcp", address)
			if err != nil {
				front.Close()
				return
			}
			go func() {
				defer front.Close()
				defer back.Close()
				go func() { io.Copy(back, front); back.Close() }()
				scanner := bufio.NewScanner(back)
				for scanner.Scan() {
					line := scanner.Text()
					if _, err := io.WriteString(front, line+"\r\n"); err != nil {
						return
					}
					if strings.Contains(line, " 785 ") || strings.Contains(line, " 761 waiter @waiter ") {
						select {
						case ready <- struct{}{}:
						default:
						}
					}
				}
			}()
		}
	}()
	return ready, l.Addr().String()
}
