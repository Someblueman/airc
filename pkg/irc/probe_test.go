package irc

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeServer registers one client and then answers its PINGs while answer
// is true; otherwise it reads and stays silent, like a link that died.
func fakeServer(t *testing.T, answer bool) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				scanner := bufio.NewScanner(conn)
				for scanner.Scan() {
					line := scanner.Text()
					if strings.HasPrefix(line, "USER ") {
						fmt.Fprint(conn, ":server 001 me :Welcome\r\n")
					}
					if token, ok := strings.CutPrefix(line, "PING :"); ok && answer {
						fmt.Fprintf(conn, ":server PONG server :%s\r\n", token)
					}
				}
			}()
		}
	}()
	return listener.Addr().String()
}

func TestProbeDropsASilentConnection(t *testing.T) {
	client, err := Dial(Config{Nick: "me", Addr: fakeServer(t, false)})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	start := time.Now()
	if err := client.Probe(200 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("a silent connection survived its probe")
	}
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Fatalf("dropped after %s, before the probe timed out", elapsed)
	}
}

func TestAnsweredProbeKeepsTheConnectionAndIsNotAnEvent(t *testing.T) {
	client, err := Dial(Config{Nick: "me", Addr: fakeServer(t, true)})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for range 3 {
		if err := client.Probe(200 * time.Millisecond); err != nil {
			t.Fatal(err)
		}
		time.Sleep(300 * time.Millisecond)
	}
	select {
	case <-client.Done():
		t.Fatal("an answered probe dropped the connection")
	default:
	}
	for {
		select {
		case event := <-client.Events():
			if raw, ok := event.(*RawEvent); ok && raw.Command == "PONG" {
				t.Fatalf("probe answer surfaced as an event: %+v", raw)
			}
			continue
		default:
		}
		break
	}
}

func TestKeepAliveDropsAnIdleSilentLinkAndReconnects(t *testing.T) {
	client, err := Dial(Config{Nick: "me", Addr: fakeServer(t, false), KeepAlive: 100 * time.Millisecond, ProbeTimeout: 100 * time.Millisecond, Reconnect: true, MinBackoff: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-client.Events():
			if e, ok := event.(*ConnectionEvent); ok && !e.Connected {
				if !strings.Contains(e.Error, ErrStalled.Error()) {
					t.Fatalf("connection lost with %q", e.Error)
				}
				return
			}
		case <-deadline:
			t.Fatal("keepalive did not drop the silent link")
		}
	}
}

func TestKeepAliveLeavesAnAnsweringLinkUp(t *testing.T) {
	client, err := Dial(Config{Nick: "me", Addr: fakeServer(t, true), KeepAlive: 50 * time.Millisecond, ProbeTimeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	select {
	case <-client.Done():
		t.Fatal("keepalive dropped an answering link")
	case <-time.After(time.Second):
	}
	if err := client.Probe(time.Second); errors.Is(err, ErrStalled) {
		t.Fatal(err)
	}
}

// A server busy sending other lines is answering, even before it gets to the
// probe's PONG.
func TestProbeSettlesOnAnyLine(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			if strings.HasPrefix(scanner.Text(), "USER ") {
				break
			}
		}
		fmt.Fprint(conn, ":server 001 me :Welcome\r\n")
		go func() {
			for scanner.Scan() { // PINGs go unanswered
			}
		}()
		for range 40 {
			time.Sleep(25 * time.Millisecond)
			if _, err := fmt.Fprint(conn, ":server NOTICE me :busy\r\n"); err != nil {
				return
			}
		}
	}()
	client, err := Dial(Config{Nick: "me", Addr: listener.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	go func() {
		for range client.Events() {
		}
	}()
	if err := client.Probe(200 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.Done():
		t.Fatal("a server sending other lines was taken for silent")
	case <-time.After(600 * time.Millisecond):
	}
}
