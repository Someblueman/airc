package adapter

import (
	"context"
	"net"
	"testing"
	"time"
)

// The next ReadFrom starts only after Serve has handled the prior packet.
// This wraps a real socket and fences synchronous invalid-payload processing.
type idlePacketFence struct {
	net.PacketConn
	processed chan int
	readCount int
}

func (c *idlePacketFence) ReadFrom(data []byte) (int, net.Addr, error) {
	select {
	case c.processed <- c.readCount:
	default:
	}
	n, address, err := c.PacketConn.ReadFrom(data)
	if err == nil {
		c.readCount++
	}
	return n, address, err
}

func TestUDPInvalidTrafficCannotRetainAdmissionLease(t *testing.T) {
	u := newUDPTestAdapter(t)
	server, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	fence := &idlePacketFence{PacketConn: server, processed: make(chan int, 64)}
	go func() { done <- u.Serve(ctx, fence) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(2 * time.Second):
			t.Error("UDP owner did not join")
		}
	}()
	address := "udp:" + client.LocalAddr().String()
	waitProcessed := func(want int) {
		t.Helper()
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		for {
			select {
			case got := <-fence.processed:
				if got >= want {
					return
				}
			case <-timer.C:
				t.Fatalf("UDP owner did not finish invalid packet %d", want)
			}
		}
	}
	waitProcessed(0)
	var original *packetSource
	var admitted time.Time
	sendInvalid := func(sequence int) {
		t.Helper()
		// Each transport ID is fresh, so completion caching cannot suppress
		// the valid AGD1 record's invalid application payload.
		fragments, err := fragmentAGD1([]byte("invalid-application"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.WriteToUDP(fragments[0], server.LocalAddr().(*net.UDPAddr)); err != nil {
			t.Fatal(err)
		}
		waitProcessed(sequence)
	}
	for packet := 1; packet <= 20; packet++ {
		sendInvalid(packet)
		u.mu.Lock()
		current := u.sources[address]
		if packet == 1 && current != nil {
			original, admitted = current, current.last
		}
		unchanged := current != nil && current == original && current.last.Equal(admitted) && len(current.active) == 0
		u.mu.Unlock()
		if !unchanged {
			t.Fatalf("invalid UDP packet %d changed authenticated source activity", packet)
		}
	}
	u.reapSources(admitted.Add(packetSourceIdle - time.Nanosecond))
	u.mu.Lock()
	retained := u.sources[address] == original && len(u.sources) == 1 && len(u.slots) == 1
	u.mu.Unlock()
	if !retained {
		t.Fatal("UDP idle reaper released source before its boundary")
	}
	u.reapSources(admitted.Add(packetSourceIdle))
	u.mu.Lock()
	released := len(u.sources) == 0 && len(u.slots) == 0 && len(u.requests) == 0
	u.mu.Unlock()
	if !released {
		t.Fatal("invalid UDP application traffic retained its expired admission slot")
	}
	sendInvalid(21)
	u.mu.Lock()
	fresh := u.sources[address]
	readmitted := fresh != nil && fresh != original && len(u.sources) == 1 && len(u.slots) == 1
	u.mu.Unlock()
	if !readmitted {
		t.Fatal("fresh real UDP packet did not acquire the released admission slot")
	}
}
