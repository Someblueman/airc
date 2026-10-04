package server

// Opt-in, isolated-process soak: AIRC_SOAK_DURATION=30m go test ./internal/server
// -run '^TestResourceSoak$' -count=1 -timeout=40m -v. Ordinary tests skip it.
import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

type soakSample struct {
	Address          string  `json:"address,omitempty"`
	Cycle            int     `json:"cycle"`
	Seconds          float64 `json:"seconds"`
	Heap             uint64  `json:"server_live_heap"`
	Goroutines       int     `json:"server_goroutines"`
	FDs              int     `json:"server_descriptors"`
	Queue            int64   `json:"server_queued_bytes"`
	Clients          int     `json:"server_clients"`
	History          int     `json:"retained_messages"`
	ClientHeap       uint64  `json:"client_live_heap"`
	ClientGoroutines int     `json:"client_goroutines"`
}

func TestResourceSoakChild(t *testing.T) {
	if os.Getenv("AIRC_SOAK_CHILD") != "1" {
		t.Skip("soak subprocess only")
	}
	s := New(Config{HistoryLimit: 2000, MaxConnections: 64, OutboundBytes: 16 << 20, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := s.RestoreHistory(filepath.Join(t.TempDir(), "history.jsonl")); err != nil {
		t.Fatal(err)
	}
	listener, err := ListenTCP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve(listener)
	defer s.Shutdown(context.Background())
	enc := json.NewEncoder(os.Stdout)
	if err := enc.Encode(soakSample{Address: listener.Addr().String()}); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		// Identical drain and idle window at every checkpoint, including calibration.
		deadline := time.Now().Add(10 * time.Second)
		for {
			s.mu.Lock()
			n := len(s.clients)
			s.mu.Unlock()
			if n == 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("clients did not drain")
			}
			time.Sleep(10 * time.Millisecond)
		}
		time.Sleep(500 * time.Millisecond)
		runtime.GC()
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		fdDir, err := os.Open("/dev/fd")
		if err != nil {
			t.Fatal(err)
		}
		fds, err := fdDir.Readdirnames(-1)
		fdDir.Close()
		if err != nil {
			t.Fatal(err)
		}
		sample := soakSample{Heap: mem.HeapAlloc, Goroutines: runtime.NumGoroutine(), FDs: len(fds)}
		s.mu.Lock()
		sample.Clients = len(s.clients)
		sample.History = s.history.size
		for _, c := range s.clients {
			sample.Queue += c.outBytes.Load()
		}
		s.mu.Unlock()
		if err := enc.Encode(sample); err != nil {
			t.Fatal(err)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestResourceSoak(t *testing.T) {
	raw := os.Getenv("AIRC_SOAK_DURATION")
	if raw == "" {
		t.Skip("set AIRC_SOAK_DURATION=30m for the scorecard soak")
	}
	duration, err := time.ParseDuration(raw)
	if err != nil || duration <= 0 || duration > time.Hour {
		t.Fatal("invalid soak duration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), duration+5*time.Minute)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestResourceSoakChild$", "-test.timeout=0")
	child.Env = append(os.Environ(), "AIRC_SOAK_CHILD=1")
	child.Stderr = os.Stderr
	in, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		in.Close()
		if err := child.Wait(); err != nil {
			t.Error(err)
		}
	}()
	scanner := bufio.NewScanner(out)
	decode := func(s *soakSample) error {
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) > 0 && line[0] == '{' {
				return json.Unmarshal(line, s)
			}
			t.Logf("child: %s", line)
		}
		if err := scanner.Err(); err != nil {
			return err
		}
		return io.EOF
	}
	var ready soakSample
	if err = decode(&ready); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	checkpoint := func(cycle int) soakSample {
		if _, err := fmt.Fprintln(in, "checkpoint"); err != nil {
			t.Fatal(err)
		}
		var s soakSample
		if err := decode(&s); err != nil {
			t.Fatal(err)
		}
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		s.Cycle = cycle
		s.Seconds = time.Since(start).Seconds()
		s.ClientHeap = m.HeapAlloc
		s.ClientGoroutines = runtime.NumGoroutine()
		data, _ := json.Marshal(s)
		t.Log(string(data))
		return s
	}
	// Five fully saturated independent cycles establish the warm range before the
	// timed phase. Freeze a 3x noise allowance with a 1 MiB heap floor; no monotonic
	// growth is hidden by moving the baseline. Quiescent handles allow one extra.
	if err := soakCycle(ctx, ready.Address, -1); err != nil {
		t.Fatal(err)
	}
	var low, high soakSample
	for i := range 5 {
		if err := soakCycle(ctx, ready.Address, i); err != nil {
			t.Fatal(err)
		}
		s := checkpoint(i)
		if i == 0 {
			low = s
			high = s
		}
		low.Heap = min(low.Heap, s.Heap)
		high.Heap = max(high.Heap, s.Heap)
		high.Goroutines = max(high.Goroutines, s.Goroutines)
		high.FDs = max(high.FDs, s.FDs)
	}
	allowance := max(uint64(1<<20), 3*(high.Heap-low.Heap))
	t.Logf("FROZEN baseline=%d noise_range=%d heap_ceiling=%d goroutine_ceiling=%d descriptor_ceiling=%d duration=%s", high.Heap, high.Heap-low.Heap, high.Heap+allowance, high.Goroutines+1, high.FDs+1, duration)
	timed := time.Now()
	cycle := 5
	for time.Since(timed) < duration {
		if err := soakCycle(ctx, ready.Address, cycle); err != nil {
			t.Fatal(err)
		}
		s := checkpoint(cycle)
		if s.Heap > high.Heap+allowance || s.Goroutines > high.Goroutines+1 || s.FDs > high.FDs+1 || s.Queue != 0 || s.Clients != 0 || s.History != 2000 {
			t.Fatalf("resource budget exceeded: %+v", s)
		}
		cycle++
	}
}

func soakCycle(ctx context.Context, address string, cycle int) error {
	rootClient, err := irc.DialContext(ctx, irc.Config{Addr: address, Nick: "soakroot", Ephemeral: true})
	if err != nil {
		return err
	}
	defer rootClient.Close()
	root, err := soakSend(ctx, rootClient, "", fmt.Sprintf("root-%d", cycle), "root")
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	failures := make(chan error, 50)
	for i := range 50 {
		wg.Add(1)
		go func(i int) { defer wg.Done(); failures <- soakAgent(ctx, address, root, cycle, i) }(i)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			return err
		}
	}
	// Five concurrent maximum-limit retrievals, each covering the 1001-message
	// retained thread. Query clients run outside the measured server process.
	queries := make(chan error, 5)
	for i := range 5 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, err := irc.DialContext(ctx, irc.Config{Addr: address, Nick: fmt.Sprintf("reader%d", i), Ephemeral: true})
			if err != nil {
				queries <- err
				return
			}
			defer c.Close()
			queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			snapshot, err := irc.RequestContext(queryCtx, c, root, 1000)
			if err == nil && len(snapshot.Messages) != 1000 {
				err = fmt.Errorf("context returned %d messages", len(snapshot.Messages))
			}
			queries <- err
		}(i)
	}
	wg.Wait()
	close(queries)
	for err := range queries {
		if err != nil {
			return err
		}
	}
	return nil
}
