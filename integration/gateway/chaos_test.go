//go:build darwin || linux

package gateway_test

// Opt-in fault-injection soak across aircd, the gateway and their clients:
//
//	AIRC_CHAOS_DURATION=30m go test ./integration/gateway -run '^TestChaosSoak$' -count=1 -timeout 60m -v
//
// AIRC_CHAOS_SEED replays a run's faults; AIRC_CHAOS_REPORT names a file
// for the JSON summary. Ordinary test runs skip it.

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	mathrand "math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/internal/testfs"
	"github.com/Someblueman/airc/pkg/fallback"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/coder/websocket"
)

var chaosRooms = []string{"#chaos-a", "#chaos-b", "#chaos-c"}

// chaosInbox is the account whose inbox one reader follows; the posters
// send it direct messages.
const chaosInbox = "inbox-reader"

// chaosWakeBound is how long after its acknowledgement a direct message to
// the sleeper may wait for a notice that covers it: the 10 s quiet period,
// an aircd pause or restart, and one failed notice's backoff.
const chaosWakeBound = 90 * time.Second

// chaosLog is the run's event record: faults, client errors, recoveries.
type chaosLog struct {
	mu     sync.Mutex
	start  time.Time
	t      *testing.T
	events []string
	counts map[string]int
}

func (l *chaosLog) add(kind, format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	line := fmt.Sprintf("%7.1fs %-18s %s", time.Since(l.start).Seconds(), kind, fmt.Sprintf(format, args...))
	l.events = append(l.events, line)
	l.counts[kind]++
	l.t.Log(line)
}

// chaosSeen records what one client received, by room: each message ID
// once, with how many times it came again, and gaps it was told of.
type chaosSeen struct {
	mu    sync.Mutex
	name  string
	ids   map[string]map[string]int // room -> message ID -> deliveries
	last  map[string]string         // room -> the newest message ID seen
	seeds map[string]string         // room -> the message reading starts after
	gaps  int
	dupes int
}

func newChaosSeen(name string, seeds map[string]string) *chaosSeen {
	s := &chaosSeen{name: name, ids: map[string]map[string]int{}, last: map[string]string{}, seeds: seeds}
	for room, seed := range seeds {
		s.ids[room] = map[string]int{}
		s.last[room] = seed
	}
	return s
}

func (s *chaosSeen) record(room, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ids[room] == nil {
		return
	}
	s.ids[room][id]++
	if s.ids[room][id] > 1 {
		s.dupes++
		return
	}
	s.last[room] = id
}

func (s *chaosSeen) after(room string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last[room]
}

func (s *chaosSeen) has(room, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ids[room][id] > 0
}

// chaosPost is one poster request and what became of it.
type chaosPost struct {
	RequestID, Target, Body, MessageID string
	Attempts                           int
	AckedAt                            time.Time
}

type chaosReport struct {
	Seed           uint64         `json:"seed"`
	Duration       string         `json:"duration"`
	Faults         map[string]int `json:"events"`
	Acked          int            `json:"posts_acknowledged"`
	Unacked        int            `json:"posts_unacknowledged"`
	Retried        int            `json:"posts_retried"`
	InHistory      int            `json:"posts_in_history"`
	DuplicatePosts int            `json:"duplicate_posts"`
	MissingAcked   int            `json:"acknowledged_posts_missing"`
	Clients        []chaosClient  `json:"clients"`
	WakeNotices    int            `json:"wake_notices"`
	DirectMessages int            `json:"direct_messages"`
	WakeLate       int            `json:"direct_messages_woken_late"`
	WakeExcused    int            `json:"direct_messages_excused_by_sigkill"`
	FinalWake      bool           `json:"final_direct_message_woke"`
	Failures       []string       `json:"failures,omitempty"`
}

type chaosClient struct {
	Name       string `json:"name"`
	Received   int    `json:"received"`
	Missing    int    `json:"missing"`
	Duplicates int    `json:"duplicates"`
	Gaps       int    `json:"gaps"`
}

func TestChaosSoak(t *testing.T) {
	duration, err := time.ParseDuration(os.Getenv("AIRC_CHAOS_DURATION"))
	if err != nil || duration <= 0 {
		t.Skip("opt-in: set AIRC_CHAOS_DURATION, such as 30m")
	}
	seed := mathrand.Uint64()
	if s := os.Getenv("AIRC_CHAOS_SEED"); s != "" {
		if seed, err = strconv.ParseUint(s, 10, 64); err != nil {
			t.Fatal(err)
		}
	}
	rng := mathrand.New(mathrand.NewPCG(seed, seed))
	log := &chaosLog{start: time.Now(), t: t, counts: map[string]int{}}
	log.add("seed", "%d", seed)

	root, err := testfs.MkdirTemp("agw-chaos-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if t.Failed() || os.Getenv("AIRC_CHAOS_KEEP") != "" {
			t.Logf("state kept in %s", root)
			return
		}
		_ = os.RemoveAll(root)
	})
	ports := freePorts(t, 5)
	aircAddress := "127.0.0.1:" + strconv.Itoa(ports[0])
	framedAddress := "127.0.0.1:" + strconv.Itoa(ports[1])
	websocketAddress := "127.0.0.1:" + strconv.Itoa(ports[2])
	mqttAddress := "127.0.0.1:" + strconv.Itoa(ports[3])
	mqttWebSocketAddress := "127.0.0.1:" + strconv.Itoa(ports[4])

	// Wake notices go straight to an in-process receiver.
	var notices struct {
		sync.Mutex
		latest []string
		at     []time.Time
	}
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var n struct {
			Latest string `json:"latest"`
		}
		_ = json.NewDecoder(r.Body).Decode(&n)
		notices.Lock()
		notices.latest = append(notices.latest, n.Latest)
		notices.at = append(notices.at, time.Now())
		notices.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer hook.Close()

	gatewayToken := filepath.Join(root, "gateway.token")
	if err := auth.CreateCredential(gatewayToken); err != nil {
		t.Fatal(err)
	}
	env := []string{"HOME=" + root, "XDG_CONFIG_HOME=" + root, "AIRC_STATE_DIR=" + root}
	aircd := &chaosProcess{name: "aircd", logPath: filepath.Join(root, "aircd.log"), env: env, ready: dialReady(aircAddress),
		args: []string{aircdBinary(t), "--listen", aircAddress, "--history-file", filepath.Join(root, "history.jsonl"), "--accounts-file", filepath.Join(root, "accounts.json"),
			"--gateway-token-file", gatewayToken, "--wake-allow", hook.URL + "/", "--log-format", "json"}}
	if err := aircd.start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = aircd.stop(syscall.SIGTERM) }()

	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "chaos", filepath.Join(root, "gateway")
	if err := os.Mkdir(c.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	c.CursorKeyFile = filepath.Join(root, "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	c.Upstream = &config.Upstream{Network: "tcp", Address: aircAddress, GatewayTokenFile: gatewayToken}
	for _, l := range []config.Listener{{ID: "framed", Protocol: "framed", Address: framedAddress}, {ID: "websocket", Protocol: "websocket", Address: websocketAddress}, {ID: "mqtt", Protocol: "mqtt", Address: mqttAddress}, {ID: "mqtt-ws", Protocol: "mqtt_websocket", Address: mqttWebSocketAddress}} {
		l.Enabled, l.Limits, l.Operations = true, fixtureListenerLimits(), dispatch.ImplementedOperations()
		c.Listeners = append(c.Listeners, l)
	}
	configPath := filepath.Join(root, "gateway.json")
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	gateway := &chaosProcess{name: "gateway", logPath: filepath.Join(root, "gateway.log"), env: env, ready: dialReady(framedAddress, websocketAddress, mqttAddress, mqttWebSocketAddress),
		args: []string{gatewayBinary(t), "serve", "--file", configPath}}
	if err := gateway.start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gateway.stop(syscall.SIGTERM) }()

	// Every client reaches its server through a fault proxy.
	proxies := map[string]*chaosProxy{
		"irc":       newChaosProxy(t, "irc", aircAddress),
		"framed":    newChaosProxy(t, "framed", framedAddress),
		"websocket": newChaosProxy(t, "websocket", websocketAddress),
		"mqtt":      newChaosProxy(t, "mqtt", mqttAddress),
		"mqtt-ws":   newChaosProxy(t, "mqtt-ws", mqttWebSocketAddress),
	}
	endpoint := func(name string) fallback.Endpoint {
		return fallback.Endpoint{Carrier: protocol.Carrier{Name: name, Port: proxies[name].port()}}
	}

	// Accounts for the posters and the account wake notices are for, and a
	// seed message per room that every reader starts after.
	tokens := map[string]string{}
	for _, nick := range []string{"poster-native", "poster-gateway", "sleeper", chaosInbox} {
		key := make([]byte, 32)
		_, _ = rand.Read(key)
		tokens[nick] = hex.EncodeToString(key)
	}
	for nick, token := range tokens {
		if nick != "sleeper" {
			_ = chaosNative(t, aircAddress, nick, token, true).Close()
		}
	}
	sleeper := chaosNative(t, aircAddress, "sleeper", tokens["sleeper"], true)
	wakeSecret := make([]byte, 32)
	_, _ = rand.Read(wakeSecret)
	if err := sleeper.Raw("WAKE SET " + hook.URL + "/sleeper " + hex.EncodeToString(wakeSecret) + " 10"); err != nil {
		t.Fatal(err)
	}
	next(t, sleeper, func(e *irc.RawEvent) bool { return e.Command == "787" })
	_ = sleeper.Close()
	seeder := chaosNative(t, aircAddress, "chaos-seeder", "", false)
	seeds := map[string]string{}
	for _, room := range chaosRooms {
		if err := seeder.Send(room, "seed "+room); err != nil {
			t.Fatal(err)
		}
		seeds[room] = next(t, seeder, func(e *irc.SendReceiptEvent) bool { return e.Message == "seed "+room }).ID
	}
	if err := seeder.Send(chaosInbox, "seed inbox"); err != nil {
		t.Fatal(err)
	}
	inboxSeed := map[string]string{chaosInbox: next(t, seeder, func(e *irc.SendReceiptEvent) bool { return e.Message == "seed inbox" }).ID}
	_ = seeder.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var clients sync.WaitGroup

	// Readers: followers on each streaming gateway carrier, and MQTT 5
	// persistent sessions.
	var readers []*chaosSeen
	for _, carrier := range []string{"framed", "framed", "websocket", "websocket"} {
		seen := newChaosSeen(fmt.Sprintf("follow-%s-%d", carrier, len(readers)), seeds)
		readers = append(readers, seen)
		clients.Add(1)
		go func() {
			defer clients.Done()
			chaosFollow(ctx, log, seen, &fallback.Client{Host: "127.0.0.1", Nick: seen.name, Endpoints: []fallback.Endpoint{endpoint(carrier)}}, false)
		}()
	}
	// One account follows its inbox, which the posters' direct messages
	// fill.
	inbox := newChaosSeen("follow-inbox", inboxSeed)
	readers = append(readers, inbox)
	clients.Add(1)
	go func() {
		defer clients.Done()
		chaosFollow(ctx, log, inbox, &fallback.Client{Host: "127.0.0.1", Nick: chaosInbox, Token: tokens[chaosInbox], Endpoints: []fallback.Endpoint{endpoint("framed")}}, true)
	}()
	mqttTCP := func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		return d.DialContext(ctx, "tcp", "127.0.0.1:"+strconv.Itoa(proxies["mqtt"].port()))
	}
	mqttWebSocket := func(ctx context.Context) (net.Conn, error) {
		dialCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		ws, _, err := websocket.Dial(dialCtx, "ws://127.0.0.1:"+strconv.Itoa(proxies["mqtt-ws"].port())+"/mqtt", &websocket.DialOptions{Subprotocols: []string{"mqtt"}})
		if err != nil {
			return nil, err
		}
		return websocket.NetConn(ctx, ws, websocket.MessageBinary), nil
	}
	for i, dial := range []func(context.Context) (net.Conn, error){mqttTCP, mqttTCP, mqttWebSocket} {
		name := fmt.Sprintf("mqtt-%d", i)
		if i == 2 {
			name = "mqtt-ws-2"
		}
		seen := newChaosSeen(name, seeds)
		readers = append(readers, seen)
		clients.Add(1)
		go func() {
			defer clients.Done()
			chaosMQTT(ctx, log, seen, dial)
		}()
	}
	time.Sleep(2 * time.Second) // let the readers start following

	// Posters: one prefers native IRC, one the gateway; each request keeps
	// its ID across every retry and carrier.
	var posts struct {
		sync.Mutex
		all []*chaosPost
	}
	var dms atomic.Int64
	postCtx, stopPosting := context.WithCancel(context.Background())
	defer stopPosting()
	var posters sync.WaitGroup
	for _, p := range []struct {
		nick      string
		endpoints []string
	}{{"poster-native", []string{"irc", "framed"}}, {"poster-gateway", []string{"framed", "websocket", "irc"}}} {
		client := &fallback.Client{Host: "127.0.0.1", Nick: p.nick, Token: tokens[p.nick], KeepAlive: 5 * time.Second, ProbeTimeout: 5 * time.Second}
		for _, name := range p.endpoints {
			client.Endpoints = append(client.Endpoints, endpoint(name))
		}
		posters.Add(1)
		go func() {
			defer posters.Done()
			for seq := 1; postCtx.Err() == nil; seq++ {
				post := &chaosPost{RequestID: fmt.Sprintf("%s-%d", p.nick, seq), Target: chaosRooms[seq%len(chaosRooms)], Body: fmt.Sprintf("%s #%d", p.nick, seq)}
				switch {
				case seq%25 == 0:
					post.Target, post.Body = "sleeper", "dm "+post.Body
					dms.Add(1)
				case seq%10 == 5:
					post.Target, post.Body = chaosInbox, "inbox "+post.Body
				}
				posts.Lock()
				posts.all = append(posts.all, post)
				posts.Unlock()
				chaosSend(log, client, post, postCtx)
				select {
				case <-postCtx.Done():
				case <-time.After(400 * time.Millisecond):
				}
			}
		}()
	}

	// Faults, one at a time, until the duration is spent.
	var kills []time.Time // aircd SIGKILLs: unsaved wake news may be lost
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		time.Sleep(time.Duration(10+rng.IntN(30)) * time.Second)
		if !time.Now().Before(deadline) {
			break
		}
		names := []string{"irc", "framed", "websocket", "mqtt", "mqtt-ws"}
		switch roll := rng.IntN(100); {
		case roll < 25:
			name := names[rng.IntN(len(names))]
			log.add("fault-cut", "%s: %d connections", name, proxies[name].cut())
		case roll < 40:
			name := names[rng.IntN(len(names))]
			hold := time.Duration(5+rng.IntN(36)) * time.Second
			log.add("fault-freeze", "%s for %v", name, hold)
			proxies[name].freeze()
			time.Sleep(hold)
			proxies[name].thaw()
		case roll < 55:
			name := names[rng.IntN(len(names))]
			link := chaosLink{delay: time.Duration(20+rng.IntN(181)) * time.Millisecond, chunk: 256 << rng.IntN(5)}
			hold := time.Duration(10+rng.IntN(31)) * time.Second
			log.add("fault-slow", "%s: %d bytes per %v for %v", name, link.chunk, link.delay, hold)
			proxies[name].slow(link)
			time.Sleep(hold)
			proxies[name].fast()
		case roll < 67:
			log.add("fault-gateway", "SIGTERM and restart")
			chaosRestart(t, log, gateway, syscall.SIGTERM)
		case roll < 75:
			log.add("fault-gateway", "SIGKILL and restart")
			chaosRestart(t, log, gateway, syscall.SIGKILL)
		case roll < 83:
			hold := time.Duration(3+rng.IntN(13)) * time.Second
			log.add("fault-aircd-pause", "SIGSTOP for %v", hold)
			aircd.signal(syscall.SIGSTOP)
			time.Sleep(hold)
			aircd.signal(syscall.SIGCONT)
		case roll < 93:
			log.add("fault-aircd", "SIGTERM and restart")
			chaosRestart(t, log, aircd, syscall.SIGTERM)
		default:
			log.add("fault-aircd", "SIGKILL and restart")
			kills = append(kills, time.Now())
			chaosRestart(t, log, aircd, syscall.SIGKILL)
		}
	}

	// Calm: no more faults. Posters stop; every request they started is
	// retried until it is answered.
	log.add("calm", "faults over; stopping posters")
	time.Sleep(20 * time.Second)
	stopPosting()
	posters.Wait()
	// One last direct message, natively, must wake the sleeper.
	final := chaosNative(t, aircAddress, "chaos-closer", "", false)
	if err := final.Send("sleeper", "the last direct message"); err != nil {
		t.Fatal(err)
	}
	finalDM := next(t, final, func(e *irc.SendReceiptEvent) bool { return e.Message == "the last direct message" }).ID
	_ = final.Close()
	dms.Add(1)

	// Catch-up: each reader should reach the newest acknowledged post in
	// every room.
	newest := map[string]string{}
	posts.Lock()
	for _, p := range posts.all {
		if p.MessageID != "" && (strings.HasPrefix(p.Target, "#") || p.Target == chaosInbox) {
			newest[p.Target] = p.MessageID
		}
	}
	posts.Unlock()
	caughtUp := time.Now().Add(3 * time.Minute)
	for _, r := range readers {
		for room, id := range newest {
			if r.ids[room] == nil {
				continue
			}
			for !r.has(room, id) && time.Now().Before(caughtUp) {
				time.Sleep(200 * time.Millisecond)
			}
		}
	}
	woke := false
	for wait := time.Now().Add(30 * time.Second); time.Now().Before(wait) && !woke; time.Sleep(200 * time.Millisecond) {
		notices.Lock()
		woke = slices.Contains(notices.latest, finalDM)
		notices.Unlock()
	}
	cancel()
	clients.Wait()
	if err := gateway.stop(syscall.SIGTERM); err != nil {
		t.Error(err)
	}
	if err := aircd.stop(syscall.SIGTERM); err != nil {
		t.Error(err)
	}

	// Verdict, from aircd's own history file.
	history := chaosHistory(t, root)
	report := chaosReport{Seed: seed, Duration: duration.String(), Faults: log.counts}
	bodies := map[string][]string{} // body -> message IDs
	for _, m := range history {
		bodies[m.Body] = append(bodies[m.Body], m.ID)
	}
	for _, p := range posts.all {
		ids := bodies[p.Body]
		if p.Attempts > 1 {
			report.Retried++
		}
		if len(ids) > 0 {
			report.InHistory++
		}
		if len(ids) > 1 {
			report.DuplicatePosts++
			report.Failures = append(report.Failures, fmt.Sprintf("%s posted %d times: %v", p.RequestID, len(ids), ids))
		}
		if p.MessageID == "" {
			report.Unacked++
			continue
		}
		report.Acked++
		if !slices.Contains(ids, p.MessageID) {
			report.MissingAcked++
			report.Failures = append(report.Failures, fmt.Sprintf("%s acknowledged as %s but not in history", p.RequestID, p.MessageID))
		}
	}
	for _, r := range readers {
		client := chaosClient{Name: r.name, Duplicates: r.dupes, Gaps: r.gaps}
		var missing []string
		for _, m := range history {
			if _, followed := r.ids[m.Target]; !followed || m.ID == r.seeds[m.Target] {
				continue
			}
			if r.ids[m.Target][m.ID] == 0 {
				missing = append(missing, m.ID)
			} else {
				client.Received++
			}
		}
		client.Missing = len(missing)
		report.Clients = append(report.Clients, client)
		if len(missing) > 0 {
			report.Failures = append(report.Failures, fmt.Sprintf("%s missed %d messages, first %s", r.name, len(missing), missing[0]))
		}
		if r.gaps > 0 {
			report.Failures = append(report.Failures, fmt.Sprintf("%s was told of %d gaps", r.name, r.gaps))
		}
		if strings.HasPrefix(r.name, "follow-") && r.dupes > 0 {
			report.Failures = append(report.Failures, fmt.Sprintf("%s saw %d messages twice; a follow resumes after its last position", r.name, r.dupes))
		}
	}
	// Every direct message to the sleeper is covered, within
	// chaosWakeBound of its acknowledgement, by a notice whose latest is it
	// or a later one; news counted less than two seconds before an aircd
	// SIGKILL may not have been saved, and waits for the next message.
	order := map[string]int{}
	for _, m := range history {
		if m.Target == "sleeper" {
			order[m.ID] = len(order)
		}
	}
	notices.Lock()
	report.WakeNotices, report.DirectMessages, report.FinalWake = len(notices.latest), int(dms.Load()), woke
	for _, p := range posts.all {
		i, ok := order[p.MessageID]
		if p.Target != "sleeper" || !ok {
			continue
		}
		covered := false
		for n, latest := range notices.latest {
			if j, ok := order[latest]; ok && j >= i && !notices.at[n].Before(p.AckedAt.Add(-chaosWakeBound)) && notices.at[n].Before(p.AckedAt.Add(chaosWakeBound)) {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		if slices.ContainsFunc(kills, func(k time.Time) bool {
			return !k.Before(p.AckedAt.Add(-time.Second)) && k.Before(p.AckedAt.Add(2*time.Second))
		}) {
			report.WakeExcused++
			continue
		}
		report.WakeLate++
		report.Failures = append(report.Failures, fmt.Sprintf("%s (%s) had no wake notice within %v", p.RequestID, p.MessageID, chaosWakeBound))
	}
	notices.Unlock()
	if !woke {
		report.Failures = append(report.Failures, "the last direct message to the sleeper sent no wake notice")
	}
	summary, _ := json.MarshalIndent(report, "", "  ")
	t.Logf("summary:\n%s", summary)
	if path := os.Getenv("AIRC_CHAOS_REPORT"); path != "" {
		_ = os.WriteFile(path, summary, 0o600)
		_ = os.WriteFile(path+".events", []byte(strings.Join(log.events, "\n")+"\n"), 0o600)
	}
	for _, f := range report.Failures {
		t.Error(f)
	}
}

// chaosNative connects straight to aircd, outside the faults, creating the
// account when create is set.
func chaosNative(t *testing.T, address, nick, token string, create bool) *irc.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := irc.DialContext(ctx, irc.Config{Network: "tcp", Addr: address, Nick: nick, IdentityToken: token, CreateAccount: create, Ephemeral: true, ReadTimeout: 30 * time.Second, WriteTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func chaosRestart(t *testing.T, log *chaosLog, p *chaosProcess, sig syscall.Signal) {
	if err := p.stop(sig); err != nil {
		log.add("restart-slow", "%s: %v", p.name, err)
	}
	for attempt := 1; ; attempt++ {
		err := p.start()
		if err == nil {
			return
		}
		log.add("restart-failed", "%s attempt %d: %v", p.name, attempt, err)
		if attempt == 5 {
			t.Fatalf("%s did not restart", p.name)
		}
		time.Sleep(time.Second)
	}
}

// chaosSend posts until the request is answered: a definite refusal ends
// it, anything else is retried under the same request ID. Once stop ends
// it may only finish what it started, within two minutes.
func chaosSend(log *chaosLog, client *fallback.Client, post *chaosPost, stop context.Context) {
	giveUp := time.Time{}
	for {
		post.Attempts++
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		receipt, err := client.Post(ctx, fallback.Message{Target: post.Target, Body: post.Body, RequestID: post.RequestID})
		cancel()
		if err == nil {
			post.MessageID, post.AckedAt = receipt.MessageID, time.Now()
			if post.Attempts > 1 {
				log.add("post-recovered", "%s after %d attempts via %s", post.RequestID, post.Attempts, receipt.Endpoint.Name)
			}
			return
		}
		if fallback.Definite(err) {
			log.add("post-refused", "%s: %v", post.RequestID, err)
			return
		}
		if post.Attempts == 1 {
			log.add("post-retry", "%s: %v", post.RequestID, err)
		}
		if stop.Err() != nil {
			if giveUp.IsZero() {
				giveUp = time.Now().Add(2 * time.Minute)
			} else if time.Now().After(giveUp) {
				log.add("post-abandoned", "%s after %d attempts", post.RequestID, post.Attempts)
				return
			}
		}
		time.Sleep(time.Second)
	}
}

// chaosFollow follows the rooms, or with inbox the client's inbox, over
// one gateway carrier, resuming from the last message it saw whenever the
// follow ends.
func chaosFollow(ctx context.Context, log *chaosLog, seen *chaosSeen, client *fallback.Client, inbox bool) {
	for ctx.Err() == nil {
		reader, err := client.ReadRooms(ctx)
		if err != nil {
			time.Sleep(time.Second)
			continue
		}
		for ctx.Err() == nil {
			set := fallback.CheckSet{Limit: 200}
			if inbox {
				after := seen.after(chaosInbox)
				set.Inbox = &after
			} else {
				for _, room := range chaosRooms {
					set.Rooms = append(set.Rooms, fallback.RoomAfter{Room: room, After: seen.after(room)})
				}
			}
			err = reader.Follow(ctx, set, 45*time.Second, func(checked fallback.Checked) error {
				if page := checked.Inbox; inbox && page != nil {
					if page.Gap {
						seen.mu.Lock()
						seen.gaps++
						seen.mu.Unlock()
					}
					for _, m := range page.Messages {
						seen.record(chaosInbox, m.ID)
					}
				}
				for i, page := range checked.Rooms {
					if page.Gap {
						seen.mu.Lock()
						seen.gaps++
						seen.mu.Unlock()
					}
					for _, m := range page.Messages {
						seen.record(set.Rooms[i].Room, m.ID)
					}
				}
				return nil
			})
			if err != nil {
				if ctx.Err() == nil {
					log.add("follow-resume", "%s: %v", seen.name, err)
				}
				break
			}
		}
		_ = reader.Close()
		time.Sleep(time.Second)
	}
}

// chaosMQTT keeps an MQTT 5 persistent session subscribed to the rooms,
// reconnecting with Clean Start 0 after every failure. A lost session is
// subscribed again after the last message seen, with airc-after, as a
// client that keeps its own positions would.
func chaosMQTT(ctx context.Context, log *chaosLog, seen *chaosSeen, dial func(context.Context) (net.Conn, error)) {
	clientID := seen.name
	first := true
	for ctx.Err() == nil {
		err := chaosMQTTSession(ctx, seen, dial, clientID, first)
		first = false
		if ctx.Err() == nil {
			log.add("mqtt-reconnect", "%s: %v", seen.name, err)
			time.Sleep(time.Second)
		}
	}
}

func chaosMQTTSession(ctx context.Context, seen *chaosSeen, dial func(context.Context) (net.Conn, error), clientID string, clean bool) error {
	conn, err := dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	var writes sync.Mutex
	write := func(header byte, body []byte) error {
		writes.Lock()
		defer writes.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_, err := conn.Write(append(mqttVarint([]byte{header}, len(body)), body...))
		return err
	}
	flags := byte(0)
	if clean {
		flags = 2
	}
	body := append(mqttString(nil, "MQTT"), 5, flags, 0, 20)
	body = append(body, mqttProps(byte(0x11), uint32(3600), byte(0x26), "airc-nick="+clientID)...)
	if err := write(0x10, mqttString(body, clientID)); err != nil {
		return err
	}
	r := bufio.NewReader(conn)
	read := func() (byte, []byte, error) {
		_ = conn.SetReadDeadline(time.Now().Add(45 * time.Second))
		header, err := r.ReadByte()
		if err != nil {
			return 0, nil, err
		}
		size, shift := 0, 0
		for {
			b, err := r.ReadByte()
			if err != nil {
				return 0, nil, err
			}
			size |= int(b&0x7f) << shift
			if b&0x80 == 0 {
				break
			}
			shift += 7
		}
		packet := make([]byte, size)
		_, err = io.ReadFull(r, packet)
		return header, packet, err
	}
	header, connack, err := read()
	if err != nil {
		return err
	}
	if header>>4 != 2 || len(connack) < 2 || connack[1] != 0 {
		return fmt.Errorf("CONNACK %x", connack)
	}
	if connack[0]&1 == 0 {
		// No session: subscribe after the last message seen in each room.
		for i, room := range chaosRooms {
			sub := binary.BigEndian.AppendUint16(nil, uint16(i+1))
			sub = append(sub, mqttProps(byte(0x26), "airc-after="+seen.after(room))...)
			sub = append(mqttString(sub, "airc/room/"+strings.TrimPrefix(room, "#")), 1)
			if err := write(0x82, sub); err != nil {
				return err
			}
		}
	}
	pings := time.NewTicker(10 * time.Second)
	defer pings.Stop()
	go func() {
		for range pings.C {
			if write(0xc0, nil) != nil {
				return
			}
		}
	}()
	for {
		header, packet, err := read()
		if err != nil {
			return err
		}
		switch header >> 4 {
		case 3: // PUBLISH
			qos := header >> 1 & 3
			topicLen := int(binary.BigEndian.Uint16(packet))
			topic := string(packet[2 : 2+topicLen])
			pos := 2 + topicLen
			var id uint16
			if qos > 0 {
				id = binary.BigEndian.Uint16(packet[pos:])
				pos += 2
			}
			propLen, n := 0, 0
			for shift := 0; ; shift += 7 {
				b := packet[pos+n]
				n++
				propLen |= int(b&0x7f) << shift
				if b&0x80 == 0 {
					break
				}
			}
			var m struct {
				ID     string `json:"id"`
				Target string `json:"target"`
			}
			if json.Unmarshal(packet[pos+n+propLen:], &m) == nil && strings.HasPrefix(topic, "airc/room/") {
				seen.record(m.Target, m.ID)
			}
			if qos == 1 {
				if err := write(0x40, binary.BigEndian.AppendUint16(nil, id)); err != nil {
					return err
				}
			}
		case 9: // SUBACK
			if len(packet) < 4 || packet[len(packet)-1] >= 0x80 {
				return fmt.Errorf("SUBACK %x", packet)
			}
		case 14: // DISCONNECT
			return fmt.Errorf("DISCONNECT %x", packet)
		}
	}
}

// chaosMessage is a line of aircd's history file.
type chaosMessage struct {
	ID     string `json:"id"`
	Target string `json:"target"`
	From   string `json:"from"`
	Body   string `json:"message"`
}

// chaosHistory reads every message aircd kept: the active history file and
// its sealed segments.
func chaosHistory(t *testing.T, root string) []chaosMessage {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, "history.jsonl.[0-9]*"))
	if err != nil {
		t.Fatal(err)
	}
	paths = slices.DeleteFunc(paths, func(p string) bool { return strings.HasSuffix(p, ".idx") })
	slices.Sort(paths)
	paths = append(paths, filepath.Join(root, "history.jsonl"))
	var messages []chaosMessage
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		for scanner.Scan() {
			var m chaosMessage
			if json.Unmarshal(scanner.Bytes(), &m) == nil && m.ID != "" {
				messages = append(messages, m)
			}
		}
		_ = file.Close()
	}
	return messages
}
