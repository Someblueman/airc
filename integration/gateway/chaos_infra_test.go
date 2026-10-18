//go:build darwin || linux

package gateway_test

import (
	"errors"
	"fmt"
	mathrand "math/rand/v2"
	"net"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"testing"
	"time"
)

// chaosProxy forwards TCP connections to target and injects link faults:
// cut closes every connection at once, as a reset link does; freeze stops
// forwarding in both directions without closing anything, as a dead NAT
// path or a stalled network does, until thaw; and slow forwards in small
// chunks with a delay before each, as a congested or lossy path does (TCP
// turns loss into delay), until fast. A connection accepted while frozen
// is held, not dialled, until thaw.
type chaosProxy struct {
	name, target string
	listener     net.Listener
	mu           sync.Mutex
	conns        map[net.Conn]struct{}
	thawed       chan struct{} // closed while traffic flows
	link         chaosLink
	wg           sync.WaitGroup
}

// chaosLink is a slow link: each chunk bytes wait delay. The zero value
// forwards at once.
type chaosLink struct {
	delay time.Duration
	chunk int
}

func newChaosProxy(t *testing.T, name, target string) *chaosProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &chaosProxy{name: name, target: target, listener: listener, conns: map[net.Conn]struct{}{}, thawed: make(chan struct{})}
	close(p.thawed)
	p.wg.Add(1)
	go p.accept()
	t.Cleanup(p.close)
	return p
}

func (p *chaosProxy) port() int { return p.listener.Addr().(*net.TCPAddr).Port }

func (p *chaosProxy) accept() {
	defer p.wg.Done()
	for {
		client, err := p.listener.Accept()
		if err != nil {
			return
		}
		p.wg.Add(1)
		go p.serve(client)
	}
}

func (p *chaosProxy) gate() chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.thawed
}

func (p *chaosProxy) slowness() chaosLink {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.link
}

func (p *chaosProxy) track(conns ...net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conns == nil {
		return false
	}
	for _, c := range conns {
		p.conns[c] = struct{}{}
	}
	return true
}

func (p *chaosProxy) untrack(conns ...net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range conns {
		delete(p.conns, c)
		_ = c.Close()
	}
}

func (p *chaosProxy) serve(client net.Conn) {
	defer p.wg.Done()
	if !p.track(client) {
		_ = client.Close()
		return
	}
	<-p.gate()
	upstream, err := net.DialTimeout("tcp", p.target, 2*time.Second)
	if err != nil || !p.track(upstream) {
		p.untrack(client)
		if upstream != nil {
			_ = upstream.Close()
		}
		return
	}
	done := make(chan struct{}, 2)
	pipe := func(dst, src net.Conn) {
		buf := make([]byte, 32<<10)
		for {
			n, err := src.Read(buf)
			if n > 0 {
				<-p.gate()
				if p.forward(dst, buf[:n]) != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}
	go pipe(upstream, client)
	go pipe(client, upstream)
	<-done
	p.untrack(client, upstream)
	<-done
}

// forward writes data to dst at the link's current speed.
func (p *chaosProxy) forward(dst net.Conn, data []byte) error {
	for len(data) > 0 {
		link := p.slowness()
		n := len(data)
		if link.chunk > 0 {
			n = min(n, link.chunk)
			time.Sleep(link.delay)
		}
		if _, err := dst.Write(data[:n]); err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}

func (p *chaosProxy) slow(link chaosLink) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.link = link
}

func (p *chaosProxy) fast() { p.slow(chaosLink{}) }

// cut closes every connection through the proxy.
func (p *chaosProxy) cut() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := len(p.conns)
	for c := range p.conns {
		_ = c.Close()
	}
	return n / 2
}

func (p *chaosProxy) freeze() {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.thawed:
		p.thawed = make(chan struct{})
	default:
	}
}

func (p *chaosProxy) thaw() {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.thawed:
	default:
		close(p.thawed)
	}
}

func (p *chaosProxy) close() {
	_ = p.listener.Close()
	p.thaw()
	p.mu.Lock()
	for c := range p.conns {
		_ = c.Close()
	}
	p.conns = nil
	p.mu.Unlock()
	p.wg.Wait()
}

// chaosProcess is a daemon the soak starts, signals, pauses and restarts
// with the same arguments and state.
type chaosProcess struct {
	name    string
	args    []string
	env     []string
	logPath string
	// ready reports when the process serves.
	ready func() error
	mu    sync.Mutex
	cmd   *exec.Cmd
	done  chan struct{}
}

func (p *chaosProcess) start() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	log, err := os.OpenFile(p.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	fmt.Fprintf(log, "--- start %s\n", time.Now().Format(time.RFC3339Nano))
	cmd := exec.Command(p.args[0], p.args[1:]...)
	cmd.Env = p.env
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		_ = log.Close()
		return err
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); _ = log.Close(); close(done) }()
	p.cmd, p.done = cmd, done
	deadline := time.Now().Add(20 * time.Second)
	for {
		if err := p.ready(); err == nil {
			return nil
		} else if time.Now().After(deadline) {
			return fmt.Errorf("%s not ready: %w", p.name, err)
		}
		select {
		case <-done:
			return fmt.Errorf("%s exited while starting", p.name)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// stop sends sig and waits; a process that outlives ten seconds is killed.
func (p *chaosProcess) stop(sig syscall.Signal) error {
	p.mu.Lock()
	cmd, done := p.cmd, p.done
	p.mu.Unlock()
	if cmd == nil {
		return errors.New(p.name + " not running")
	}
	_ = cmd.Process.Signal(syscall.SIGCONT)
	_ = cmd.Process.Signal(sig)
	select {
	case <-done:
		return nil
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		return fmt.Errorf("%s ignored %v for ten seconds", p.name, sig)
	}
}

func (p *chaosProcess) signal(sig syscall.Signal) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd != nil {
		_ = p.cmd.Process.Signal(sig)
	}
}

// dialReady is a ready check: every address accepts a connection.
func dialReady(addresses ...string) func() error {
	return func() error {
		for _, address := range addresses {
			c, err := net.DialTimeout("tcp", address, 200*time.Millisecond)
			if err != nil {
				return err
			}
			_ = c.Close()
		}
		return nil
	}
}

// freePorts reserves nothing: it names ports that were free a moment ago.
func freePorts(t *testing.T, n int) []int {
	t.Helper()
	var listeners []net.Listener
	var ports []int
	for range n {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		listeners = append(listeners, l)
		ports = append(ports, l.Addr().(*net.TCPAddr).Port)
	}
	for _, l := range listeners {
		_ = l.Close()
	}
	return ports
}

// chaosUDPProxy forwards UDP datagrams to target, each client address over
// its own upstream socket, and injects path faults: cut gives every client
// a new upstream socket, so the server sees each client come from a new
// address (a NAT rebinding, which QUIC migrates across); freeze drops
// every datagram until thaw; slow delays each datagram; and lossy drops a
// share of them at random, until fast.
type chaosUDPProxy struct {
	target *net.UDPAddr
	conn   *net.UDPConn
	mu     sync.Mutex
	flows  map[string]*net.UDPConn
	frozen bool
	delay  time.Duration
	loss   float64
	rng    *mathrand.Rand
	closed bool
	wg     sync.WaitGroup
}

func newChaosUDPProxy(t *testing.T, target string) *chaosUDPProxy {
	t.Helper()
	addr, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	p := &chaosUDPProxy{target: addr, conn: conn, flows: map[string]*net.UDPConn{}, rng: mathrand.New(mathrand.NewPCG(1, 2))}
	p.wg.Add(1)
	go p.serve()
	t.Cleanup(p.close)
	return p
}

func (p *chaosUDPProxy) port() int { return p.conn.LocalAddr().(*net.UDPAddr).Port }

func (p *chaosUDPProxy) serve() {
	defer p.wg.Done()
	buf := make([]byte, 65535)
	for {
		n, from, err := p.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		up := p.flow(from)
		if up == nil {
			continue
		}
		p.forward(append([]byte(nil), buf[:n]...), func(b []byte) { _, _ = up.Write(b) })
	}
}

// flow is from's upstream socket, made (with its return path) if from has
// none since the last cut.
func (p *chaosUDPProxy) flow(from *net.UDPAddr) *net.UDPConn {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	if up := p.flows[from.String()]; up != nil {
		return up
	}
	up, err := net.DialUDP("udp", nil, p.target)
	if err != nil {
		return nil
	}
	p.flows[from.String()] = up
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		buf := make([]byte, 65535)
		for {
			n, err := up.Read(buf)
			if err != nil {
				return
			}
			p.forward(append([]byte(nil), buf[:n]...), func(b []byte) { _, _ = p.conn.WriteToUDP(b, from) })
		}
	}()
	return up
}

// forward sends a datagram through the current faults.
func (p *chaosUDPProxy) forward(datagram []byte, send func([]byte)) {
	p.mu.Lock()
	drop := p.frozen || p.loss > 0 && p.rng.Float64() < p.loss
	delay := p.delay
	p.mu.Unlock()
	switch {
	case drop:
	case delay > 0:
		time.AfterFunc(delay, func() { send(datagram) })
	default:
		send(datagram)
	}
}

// cut rebinds every client to a new upstream socket.
func (p *chaosUDPProxy) cut() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := len(p.flows)
	for key, up := range p.flows {
		_ = up.Close()
		delete(p.flows, key)
	}
	return n
}

func (p *chaosUDPProxy) set(frozen bool, delay time.Duration, loss float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.frozen, p.delay, p.loss = frozen, delay, loss
}

func (p *chaosUDPProxy) close() {
	p.mu.Lock()
	p.closed = true
	_ = p.conn.Close()
	for _, up := range p.flows {
		_ = up.Close()
	}
	p.mu.Unlock()
	p.wg.Wait()
}
