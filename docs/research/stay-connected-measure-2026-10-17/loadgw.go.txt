// Command loadgw measures many agents reaching aircd through the gateway's
// Unix socket, as airc does when native IRC is out of reach, against the same
// workload over native IRC. It runs real aircd and airc-gateway processes.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/admin"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/fallback"
	"github.com/Someblueman/airc/pkg/irc"
)

type sample struct {
	op  string
	ms  float64
	err string
}

type recorder struct {
	mu      sync.Mutex
	samples []sample
	on      bool
}

// latency records how long ago a message others posted was sent, when an
// agent first sees it.
func (r *recorder) latency(m *irc.MessageMetadata, nick string) {
	if m == nil || m.From == nick || m.Timestamp.IsZero() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.on {
		r.samples = append(r.samples, sample{op: "deliver", ms: float64(time.Since(m.Timestamp).Microseconds()) / 1000})
	}
}

func (r *recorder) add(op string, start time.Time, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.on {
		return
	}
	s := sample{op: op, ms: float64(time.Since(start).Microseconds()) / 1000}
	if err != nil {
		s.err = err.Error()
		if len(s.err) > 120 {
			s.err = s.err[:120]
		}
	}
	r.samples = append(r.samples, s)
}

type cpuMem struct {
	cpu float64 // seconds
	rss int64   // KiB
}

func usage(pid int) cpuMem {
	var u cpuMem
	if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
		f := strings.Fields(string(data)[strings.LastIndexByte(string(data), ')')+2:])
		ut, _ := strconv.ParseFloat(f[11], 64)
		st, _ := strconv.ParseFloat(f[12], 64)
		u.cpu = (ut + st) / 100
	}
	if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid)); err == nil {
		for line := range strings.SplitSeq(string(data), "\n") {
			if v, ok := strings.CutPrefix(line, "VmRSS:"); ok {
				u.rss, _ = strconv.ParseInt(strings.Fields(v)[0], 10, 64)
			}
		}
	}
	return u
}

func main() {
	mode := flag.String("mode", "gateway", "gateway or native")
	agents := flag.Int("agents", 50, "concurrent agents")
	rooms := flag.Int("rooms", 3, "rooms each agent follows (of 10)")
	poll := flag.Duration("poll", time.Second, "room read interval per agent")
	post := flag.Duration("post", 5*time.Second, "post interval per agent")
	duration := flag.Duration("duration", 30*time.Second, "measured time")
	warmup := flag.Duration("warmup", 5*time.Second, "unmeasured time first")
	bin := flag.String("bin", "", "directory with aircd and airc-gateway")
	out := flag.String("out", "", "JSON lines result file (appended)")
	flag.IntVar(&sessions, "sessions", 0, "gateway limits.sessions (0: default)")
	flag.IntVar(&workers, "workers", 0, "gateway limits.workers (0: default)")
	flag.IntVar(&perMinute, "per-minute", 0, "unix listener requests_per_minute (0: default 2400)")
	flag.IntVar(&procs, "procs", 1, "agent processes (gateway mode), so each has its own peer PID")
	flag.BoolVar(&useCheck, "check", false, "gateway mode: follow rooms with one waiting check, not a read per room each poll")
	flag.BoolVar(&useFollow, "follow", false, "gateway mode: follow rooms with one followed check (stream), resumed when its lease ends")
	child := flag.String("child", "", "internal: run agents FIRST:COUNT against SOCKET and print samples")
	childSock := flag.String("sock", "", "internal")
	flag.Parse()
	if *child != "" {
		runChild(*child, *childSock, *rooms, *poll, *post)
		return
	}
	if err := run(*mode, *agents, *rooms, *poll, *post, *duration, *warmup, *bin, *out); err != nil {
		fmt.Fprintln(os.Stderr, "loadgw:", err)
		os.Exit(1)
	}
}

func freePort() int {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func run(mode string, agents, rooms int, poll, post, duration, warmup time.Duration, bin, out string) error {
	dir, err := os.MkdirTemp("", "lgw")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	_ = os.Chmod(dir, 0o700)
	port := freePort()
	addr := "127.0.0.1:" + strconv.Itoa(port)
	gwToken := filepath.Join(dir, "gateway.token")
	if err := admin.CreateToken(gwToken); err != nil {
		return err
	}
	aircd := exec.Command(filepath.Join(bin, "aircd"), "--listen", addr, "--history", "10000", "--history-file", filepath.Join(dir, "history.jsonl"),
		"--gateway-token-file", gwToken, "--max-connections", "1024", "--log-file", filepath.Join(dir, "aircd.log"))
	aircd.Stderr = os.Stderr
	if err := aircd.Start(); err != nil {
		return err
	}
	defer func() { _ = aircd.Process.Kill(); _ = aircd.Wait() }()
	for range 100 {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	sock := filepath.Join(dir, "gw.sock")
	var gateway *exec.Cmd
	if mode == "gateway" {
		key := filepath.Join(dir, "cursor.key")
		if err := auth.CreateCredential(key); err != nil {
			return err
		}
		state := filepath.Join(dir, "gwstate")
		_ = os.Mkdir(state, 0o700)
		cfg := map[string]any{"version": 1, "realm": "load", "state_dir": state, "cursor_key_file": key,
			"upstream":  map[string]any{"network": "tcp", "address": addr, "gateway_token_file": gwToken},
			"carriers":  []string{"irc:" + strconv.Itoa(port), "unix:" + sock},
			"listeners": []any{map[string]any{"id": "unix", "protocol": "unix", "address": sock, "enabled": true}}}
		if perMinute > 0 {
			cfg["listeners"] = []any{map[string]any{"id": "unix", "protocol": "unix", "address": sock, "enabled": true, "limits": map[string]any{"requests_per_minute": perMinute}}}
		}
		if sessions > 0 {
			cfg["limits"] = map[string]any{"sessions": sessions, "sessions_per_author": 4, "watches": 8, "watches_per_author": 2, "workers": max(workers, 8), "pending": 4096, "pending_per_author": 128}
		}
		data, _ := json.Marshal(cfg)
		file := filepath.Join(dir, "gateway.json")
		if err := os.WriteFile(file, data, 0o600); err != nil {
			return err
		}
		gateway = exec.Command(filepath.Join(bin, "airc-gateway"), "serve", "--file", file)
		stdout, _ := gateway.StdoutPipe()
		gateway.Stderr = os.Stderr
		if err := gateway.Start(); err != nil {
			return err
		}
		defer func() { _ = gateway.Process.Kill(); _ = gateway.Wait() }()
		ready := bufio.NewReader(stdout)
		if _, err := ready.ReadString('\n'); err != nil {
			return fmt.Errorf("gateway did not start: %w", err)
		}
		go func() { _, _ = ready.WriteTo(discard{}) }()
	}

	rec := &recorder{}
	ctx, cancel := context.WithTimeout(context.Background(), warmup+duration+30*time.Second)
	defer cancel()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var children []*exec.Cmd
	if mode == "gateway" && procs > 1 {
		self, _ := os.Executable()
		per := (agents + procs - 1) / procs
		for first := 0; first < agents; first += per {
			count := min(per, agents-first)
			cmd := exec.CommandContext(ctx, self, "-child", fmt.Sprintf("%d:%d", first, count), "-sock", sock, "-rooms", strconv.Itoa(rooms), "-poll", poll.String(), "-post", post.String(), "-check="+strconv.FormatBool(useCheck), "-follow="+strconv.FormatBool(useFollow))
			stdout, _ := cmd.StdoutPipe()
			cmd.Stderr = os.Stderr
			if err := cmd.Start(); err != nil {
				return err
			}
			wg.Go(func() {
				scanner := bufio.NewScanner(stdout)
				for scanner.Scan() {
					var line struct {
						Op, Err string
						Ms      float64
					}
					if json.Unmarshal(scanner.Bytes(), &line) == nil {
						rec.mu.Lock()
						if rec.on {
							rec.samples = append(rec.samples, sample{op: line.Op, ms: line.Ms, err: line.Err})
						}
						rec.mu.Unlock()
					}
				}
				_ = cmd.Wait()
			})
			children = append(children, cmd)
		}
	}
	for i := range agents {
		if mode == "gateway" && procs > 1 {
			break
		}
		nick := fmt.Sprintf("agent%d", i)
		var mine []string
		for k := range rooms {
			mine = append(mine, fmt.Sprintf("#room%d", (i+k)%10))
		}
		wg.Go(func() {
			time.Sleep(time.Duration(rand.Int64N(int64(poll))))
			if mode == "gateway" {
				gatewayAgent(ctx, rec, stop, nick, mine, sock, poll, post)
			} else {
				nativeAgent(ctx, rec, stop, nick, mine, addr, poll, post)
			}
		})
	}
	time.Sleep(warmup)
	pids := map[string]int{"aircd": aircd.Process.Pid}
	if gateway != nil {
		pids["gateway"] = gateway.Process.Pid
	}
	before := map[string]cpuMem{}
	for name, pid := range pids {
		before[name] = usage(pid)
	}
	rec.mu.Lock()
	rec.on = true
	rec.mu.Unlock()
	time.Sleep(duration)
	rec.mu.Lock()
	rec.on = false
	rec.mu.Unlock()
	after := map[string]cpuMem{}
	for name, pid := range pids {
		after[name] = usage(pid)
	}
	close(stop)
	for _, c := range children {
		_ = c.Process.Signal(os.Interrupt)
	}
	wg.Wait()

	result := map[string]any{"check": useCheck, "follow": useFollow, "procs": procs, "per_minute": perMinute, "sessions": sessions, "workers": workers, "mode": mode, "agents": agents, "rooms": rooms, "poll_ms": poll.Milliseconds(), "post_ms": post.Milliseconds(), "duration_s": duration.Seconds()}
	for name := range pids {
		result[name+"_cpu_pct"] = (after[name].cpu - before[name].cpu) / duration.Seconds() * 100
		result[name+"_rss_mib"] = float64(after[name].rss) / 1024
	}
	ops := map[string][]float64{}
	errs := map[string]map[string]int{}
	for _, s := range rec.samples {
		if s.err != "" {
			if errs[s.op] == nil {
				errs[s.op] = map[string]int{}
			}
			errs[s.op][s.err]++
			continue
		}
		ops[s.op] = append(ops[s.op], s.ms)
	}
	for op, v := range ops {
		slices.Sort(v)
		q := func(p float64) float64 { return v[min(len(v)-1, int(p*float64(len(v))))] }
		result[op] = map[string]any{"ok": len(v), "per_s": float64(len(v)) / duration.Seconds(), "p50": q(.5), "p90": q(.9), "p99": q(.99), "max": v[len(v)-1]}
	}
	result["errors"] = errs
	line, _ := json.Marshal(result)
	fmt.Println(string(line))
	if out != "" {
		f, err := os.OpenFile(out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = f.Write(append(line, '\n'))
			f.Close()
		}
	}
	return nil
}

var sessions, workers, perMinute, procs int

// useCheck follows rooms with one waiting gateway check instead of a read
// per room every poll; useFollow with one followed check (a stream).
var useCheck, useFollow bool

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// gatewayAgent follows its rooms as airc does over a gateway carrier: one
// connection reads every room after its last message each poll; each post is
// its own short command over a new connection, as airc send is.
func gatewayAgent(ctx context.Context, rec *recorder, stop <-chan struct{}, nick string, rooms []string, sock string, poll, post time.Duration) {
	e, _ := fallback.ParseEndpoint("unix:" + sock)
	c := &fallback.Client{Host: "127.0.0.1", Nick: nick, Endpoints: []fallback.Endpoint{e}}
	if useFollow {
		followAgent(ctx, rec, stop, c, nick, rooms, post)
		return
	}
	if useCheck {
		checkAgent(ctx, rec, stop, c, nick, rooms, post)
		return
	}
	after := map[string]string{}
	var reader *fallback.RoomReader
	nextPost := time.Now().Add(time.Duration(rand.Int64N(int64(post))))
	seq := 0
	for {
		select {
		case <-stop:
			if reader != nil {
				reader.Close()
			}
			return
		default:
		}
		if reader == nil {
			start := time.Now()
			var err error
			reader, err = c.ReadRooms(ctx)
			rec.add("connect", start, err)
			if err != nil {
				time.Sleep(time.Second)
				continue
			}
		}
		for _, room := range rooms {
			start := time.Now()
			page, err := reader.Read(ctx, room, after[room], 100)
			rec.add("read", start, err)
			if err != nil {
				reader.Close()
				reader = nil
				break
			}
			if n := len(page.Messages); n > 0 {
				if after[room] != "" {
					for _, m := range page.Messages {
						rec.latency(m, nick)
					}
				}
				after[room] = page.Messages[n-1].ID
			}
		}
		if time.Now().After(nextPost) {
			seq++
			start := time.Now()
			_, err := c.Post(ctx, fallback.Message{Target: rooms[0], Body: fmt.Sprintf("%s update %d", nick, seq), RequestID: fmt.Sprintf("%s-%d-%d", nick, time.Now().UnixNano(), seq)})
			rec.add("post", start, err)
			nextPost = nextPost.Add(post)
		}
		select {
		case <-stop:
		case <-time.After(poll):
		}
	}
}

// checkAgent follows its rooms with one gateway check that waits for news,
// posting from its own goroutine on the same schedule.
func checkAgent(ctx context.Context, rec *recorder, stop <-chan struct{}, c *fallback.Client, nick string, rooms []string, post time.Duration) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-stop
		cancel()
	}()
	go func() {
		next := time.Now().Add(time.Duration(rand.Int64N(int64(post))))
		for seq := 1; ; seq++ {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Until(next)):
			}
			start := time.Now()
			_, err := c.Post(ctx, fallback.Message{Target: rooms[0], Body: fmt.Sprintf("%s update %d", nick, seq), RequestID: fmt.Sprintf("%s-%d-%d", nick, time.Now().UnixNano(), seq)})
			if ctx.Err() == nil {
				rec.add("post", start, err)
			}
			next = next.Add(post)
		}
	}()
	after := map[string]string{}
	var reader *fallback.RoomReader
	for ctx.Err() == nil {
		if reader == nil {
			start := time.Now()
			var err error
			reader, err = c.ReadRooms(ctx)
			rec.add("connect", start, err)
			if err != nil {
				time.Sleep(time.Second)
				continue
			}
		}
		reads := make([]fallback.RoomAfter, len(rooms))
		for i, room := range rooms {
			reads[i] = fallback.RoomAfter{Room: room, After: after[room]}
		}
		start := time.Now()
		pages, err := reader.Check(ctx, reads, 100, 20*time.Second)
		if ctx.Err() != nil {
			break
		}
		rec.add("check", start, err)
		if err != nil {
			// As airc does: back off, and reconnect.
			reader.Close()
			reader = nil
			select {
			case <-ctx.Done():
			case <-time.After(250*time.Millisecond + time.Duration(rand.Int64N(int64(250*time.Millisecond)))):
			}
			continue
		}
		for i, page := range pages {
			if n := len(page.Messages); n > 0 {
				if after[rooms[i]] != "" {
					for _, m := range page.Messages {
						rec.latency(m, nick)
					}
				}
				after[rooms[i]] = page.Messages[n-1].ID
			}
		}
		// As airc does: start rounds at most once a second.
		if pause := time.Second - time.Since(start); pause > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(pause):
			}
		}
	}
	if reader != nil {
		reader.Close()
	}
}

// followAgent follows its rooms with one followed check: a stream the
// gateway keeps for its lease, resumed from the last positions when it ends.
// Each stream open is a "follow" request; each frame it delivers is a
// "frame" (news) or "keepalive" (empty) sample. Posts run on their own
// schedule as in checkAgent.
func followAgent(ctx context.Context, rec *recorder, stop <-chan struct{}, c *fallback.Client, nick string, rooms []string, post time.Duration) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-stop
		cancel()
	}()
	go func() {
		next := time.Now().Add(time.Duration(rand.Int64N(int64(post))))
		for seq := 1; ; seq++ {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Until(next)):
			}
			start := time.Now()
			_, err := c.Post(ctx, fallback.Message{Target: rooms[0], Body: fmt.Sprintf("%s update %d", nick, seq), RequestID: fmt.Sprintf("%s-%d-%d", nick, time.Now().UnixNano(), seq)})
			if ctx.Err() == nil {
				rec.add("post", start, err)
			}
			next = next.Add(post)
		}
	}()
	after := map[string]string{}
	var reader *fallback.RoomReader
	for ctx.Err() == nil {
		if reader == nil {
			start := time.Now()
			var err error
			reader, err = c.ReadRooms(ctx)
			rec.add("connect", start, err)
			if err != nil {
				time.Sleep(time.Second)
				continue
			}
		}
		set := fallback.CheckSet{Limit: 100}
		for _, room := range rooms {
			set.Rooms = append(set.Rooms, fallback.RoomAfter{Room: room, After: after[room]})
		}
		start := time.Now()
		err := reader.Follow(ctx, set, 150*time.Second, func(checked fallback.Checked) error {
			news := false
			for i, page := range checked.Rooms {
				if n := len(page.Messages); n > 0 {
					news = true
					if after[rooms[i]] != "" {
						for _, m := range page.Messages {
							rec.latency(m, nick)
						}
					}
					after[rooms[i]] = page.Messages[n-1].ID
				} else if page.Cursor != "" {
					after[rooms[i]] = page.Cursor
				}
			}
			if news {
				rec.add("frame", time.Now(), nil)
			} else {
				rec.add("keepalive", time.Now(), nil)
			}
			return nil
		})
		if ctx.Err() != nil {
			break
		}
		rec.add("follow", start, err)
		if err != nil {
			// As airc does: the reader is closed; back off and reconnect.
			reader = nil
			select {
			case <-ctx.Done():
			case <-time.After(250*time.Millisecond + time.Duration(rand.Int64N(int64(250*time.Millisecond)))):
			}
		}
	}
	if reader != nil {
		reader.Close()
	}
}

// nativeAgent runs the same workload over one persistent native connection:
// one CHECK reads every room each poll, and posts wait for their receipts.
func nativeAgent(ctx context.Context, rec *recorder, stop <-chan struct{}, nick string, rooms []string, addr string, poll, post time.Duration) {
	var client *irc.Client
	after := map[string]string{}
	nextPost := time.Now().Add(time.Duration(rand.Int64N(int64(post))))
	seq := 0
	for {
		select {
		case <-stop:
			if client != nil {
				client.Close()
			}
			return
		default:
		}
		if client == nil {
			start := time.Now()
			var err error
			client, err = irc.DialContext(ctx, irc.Config{Nick: nick, Addr: addr, Ephemeral: true})
			rec.add("connect", start, err)
			if err != nil {
				time.Sleep(time.Second)
				continue
			}
		}
		req := irc.CheckRequest{IncludeOwn: true, MaxMessages: 100 * len(rooms)}
		for _, room := range rooms {
			req.Targets = append(req.Targets, irc.CheckTarget{Target: room, After: after[room], Limit: 100})
		}
		start := time.Now()
		rctx, rcancel := context.WithTimeout(ctx, 10*time.Second)
		entries, err := irc.RequestCheck(rctx, client, req, nil)
		rcancel()
		rec.add("read", start, err)
		if err != nil {
			client.Close()
			client = nil
			continue
		}
		for _, e := range entries {
			if e.Kind == "message" && e.Message != nil && protocol.IsChannel(e.Message.Target) {
				if after[e.Message.Target] != "" {
					rec.latency(e.Message, nick)
				}
				after[e.Message.Target] = e.Message.ID
			}
		}
		if time.Now().After(nextPost) {
			seq++
			id := fmt.Sprintf("%s-%d-%d", nick, time.Now().UnixNano(), seq)
			start := time.Now()
			err := client.SendWithID(rooms[0], fmt.Sprintf("%s update %d", nick, seq), id)
			if err == nil {
				err = awaitReceipt(ctx, client, id)
			}
			rec.add("post", start, err)
			if err != nil {
				client.Close()
				client = nil
				continue
			}
			nextPost = nextPost.Add(post)
		}
		select {
		case <-stop:
		case <-time.After(poll):
		}
	}
}

func awaitReceipt(ctx context.Context, c *irc.Client, id string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		select {
		case ev, ok := <-c.Events():
			if !ok {
				return fmt.Errorf("connection closed")
			}
			if r, ok := ev.(*irc.SendReceiptEvent); ok && r.RequestID == id {
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// runChild runs agents FIRST..FIRST+COUNT in this process, printing each
// sample as a JSON line until interrupted.
func runChild(spec, sock string, rooms int, poll, post time.Duration) {
	var first, count int
	fmt.Sscanf(spec, "%d:%d", &first, &count)
	rec := &recorder{on: true}
	stop := make(chan struct{})
	ctx := context.Background()
	go func() {
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, os.Interrupt)
		<-signals
		close(stop)
	}()
	var mu sync.Mutex
	enc := json.NewEncoder(os.Stdout)
	go func() {
		for {
			time.Sleep(200 * time.Millisecond)
			rec.mu.Lock()
			batch := rec.samples
			rec.samples = nil
			rec.mu.Unlock()
			mu.Lock()
			for _, s := range batch {
				_ = enc.Encode(struct {
					Op, Err string
					Ms      float64
				}{s.op, s.err, s.ms})
			}
			mu.Unlock()
		}
	}()
	var wg sync.WaitGroup
	for i := first; i < first+count; i++ {
		nick := fmt.Sprintf("agent%d", i)
		var mine []string
		for k := range rooms {
			mine = append(mine, fmt.Sprintf("#room%d", (i+k)%10))
		}
		wg.Go(func() {
			time.Sleep(time.Duration(rand.Int64N(int64(poll))))
			gatewayAgent(ctx, rec, stop, nick, mine, sock, poll, post)
		})
	}
	wg.Wait()
	time.Sleep(300 * time.Millisecond)
}
