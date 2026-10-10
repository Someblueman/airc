//go:build darwin && arm64

package gateway_test

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv6"
	"golang.org/x/sys/unix"
)

const icmpV6ResponseKeyID = "icmpv6-response"
const icmpV6ResponseRouteID = "icmpv6-return"

type icmpV6Fixture struct {
	udpFixture
	client         *icmpV6Client
	serverChannel  uint16
	clientChannel  uint16
	interfaceIndex int
}

type icmpV6Client struct {
	conn            *net.UDPConn
	packets         *ipv6.PacketConn
	interfaceIndex  int
	serverChannel   uint16
	clientChannel   uint16
	autoReplies     int
	packetCount     int
	lastPacket      string
	echoRequests    int
	lastEchoRequest string
	appReplies      int
	lastAppReply    string
}

func newICMPv6Fixture(t *testing.T) icmpV6Fixture {
	t.Helper()
	channels := make([]byte, 4)
	if _, err := rand.Read(channels); err != nil {
		t.Fatalf("allocate cooperative ICMPv6 channels: %v", err)
	}
	base := uint16(62000 + binary.BigEndian.Uint32(channels)%3000)
	serverChannel, clientChannel := base, base+1
	iface, err := net.InterfaceByName("lo0")
	if err != nil {
		t.Fatalf("find Darwin loopback interface lo0: %v", err)
	}
	fixture := newPacketFixtureConfigured(t, "icmp_v6", func(c *config.Config, _, _ *net.UDPConn) {
		c.ResponseSigningKey.ID = icmpV6ResponseKeyID
		c.Listeners[0].ID = "icmpv6"
		c.Listeners[0].Address = (&net.UDPAddr{IP: net.ParseIP("::1"), Port: int(serverChannel)}).String()
		c.Listeners[0].ICMPv6 = &config.ICMPv6{Interface: "lo0"}
		c.ReturnRoutes[0].ID = icmpV6ResponseRouteID
		c.ReturnRoutes[0].Listener = c.Listeners[0].ID
		c.ReturnRoutes[0].Destination = (&net.UDPAddr{IP: net.ParseIP("::1"), Port: int(clientChannel)}).String()
	}, listenUDPFixtureSocket)
	syscall.ForkLock.RLock()
	fd, err := unix.Socket(unix.AF_INET6, unix.SOCK_DGRAM, unix.IPPROTO_ICMPV6)
	if err == nil {
		_, err = unix.FcntlInt(uintptr(fd), unix.F_SETFD, unix.FD_CLOEXEC)
		if err != nil {
			_ = unix.Close(fd)
		}
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		t.Fatalf("open independent native ICMPv6 datagram client on loopback: %v", err)
	}
	var loopback [16]byte
	copy(loopback[:], net.ParseIP("::1").To16())
	if err := unix.Bind(fd, &unix.SockaddrInet6{Addr: loopback}); err != nil {
		_ = unix.Close(fd)
		t.Fatalf("bind independent native ICMPv6 client on loopback: %v", err)
	}
	file := os.NewFile(uintptr(fd), "icmpv6-native-client")
	if file == nil {
		_ = unix.Close(fd)
		t.Fatal("wrap native ICMPv6 client descriptor")
	}
	connValue, err := net.FilePacketConn(file)
	closeFileErr := file.Close()
	if err != nil || closeFileErr != nil {
		if connValue != nil {
			_ = connValue.Close()
		}
		t.Fatalf("wrap native ICMPv6 client descriptor: %v (close source: %v)", err, closeFileErr)
	}
	conn, ok := connValue.(*net.UDPConn)
	if !ok {
		_ = connValue.Close()
		t.Fatalf("native ICMPv6 datagram endpoint type=%T, want *net.UDPConn", connValue)
	}
	if err := conn.SetReadBuffer(256 << 10); err != nil {
		_ = conn.Close()
		t.Fatalf("increase independent native ICMPv6 client receive buffer: %v", err)
	}
	raw, err := conn.SyscallConn()
	if err != nil {
		_ = conn.Close()
		t.Fatalf("inspect native ICMPv6 client receive buffer: %v", err)
	}
	var receiveBuffer int
	var optionErr error
	controlErr := raw.Control(func(fd uintptr) {
		receiveBuffer, optionErr = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_RCVBUF)
	})
	if err := errors.Join(controlErr, optionErr); err != nil || receiveBuffer != 256<<10 {
		_ = conn.Close()
		t.Fatalf("native ICMPv6 client SO_RCVBUF=%d want 256 KiB: %v", receiveBuffer, err)
	}
	packets := ipv6.NewPacketConn(conn)
	if err := packets.SetControlMessage(ipv6.FlagDst|ipv6.FlagInterface, true); err != nil {
		_ = conn.Close()
		t.Fatalf("enable native ICMPv6 destination/interface metadata: %v", err)
	}
	client := &icmpV6Client{conn: conn, packets: packets, interfaceIndex: iface.Index, serverChannel: serverChannel, clientChannel: clientChannel}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close native ICMPv6 client: %v", err)
		}
	})
	return icmpV6Fixture{udpFixture: fixture, client: client, serverChannel: serverChannel, clientChannel: clientChannel, interfaceIndex: iface.Index}
}

func icmpV6Frame(t *testing.T, f icmpV6Fixture, request core.Request) adapter.FrameRequest {
	t.Helper()
	return adapter.FrameRequest{Version: 1, CorrelationID: "icmpv6-" + request.RequestID, Profile: "encrypted", Envelope: f.credentials.token(t, request, true)}
}

func icmpV6FrameForEnvelope(requestID, envelope string) adapter.FrameRequest {
	return adapter.FrameRequest{Version: 1, CorrelationID: "icmpv6-" + requestID, Profile: "encrypted", Envelope: envelope}
}

func (c *icmpV6Client) sendFrame(t *testing.T, frame adapter.FrameRequest, destinationChannel uint16, omitLast bool, allowOversize bool) {
	t.Helper()
	body, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("encode ICMPv6 request frame: %v", err)
	}
	if len(body) == 0 || (!allowOversize && len(body) > adapter.MaxRequestFrameBytes) {
		t.Fatalf("ICMPv6 request frame bytes=%d max=%d", len(body), adapter.MaxRequestFrameBytes)
	}
	var deliveryID [16]byte
	if _, err := rand.Read(deliveryID[:]); err != nil {
		t.Fatalf("allocate ICMPv6 delivery ID: %v", err)
	}
	fragments := webTransportDatagrams(body, deliveryID)
	if omitLast {
		if len(fragments) < 2 {
			t.Fatal("partial ICMPv6 acceptance witness needs multiple AGD1 fragments")
		}
		fragments = fragments[:len(fragments)-1]
	}
	deadline := time.Now().Add(3 * time.Second)
	for _, fragment := range fragments {
		if len(fragment) == 0 || len(fragment) > 1000 {
			t.Fatalf("ICMPv6 AGD1 fragment bytes=%d exceed native payload bound", len(fragment))
		}
		message := &icmp.Message{Type: ipv6.ICMPTypeEchoRequest, Code: 0, Body: &icmp.Echo{ID: int(destinationChannel), Seq: int(c.clientChannel), Data: fragment}}
		wire, err := message.Marshal(nil)
		if err != nil {
			t.Fatalf("marshal native ICMPv6 echo request: %v", err)
		}
		if err := c.conn.SetWriteDeadline(deadline); err != nil {
			t.Fatalf("set ICMPv6 absolute write deadline: %v", err)
		}
		control := &ipv6.ControlMessage{Src: net.ParseIP("::1"), IfIndex: c.interfaceIndex}
		n, err := c.packets.WriteTo(wire, control, &net.UDPAddr{IP: net.ParseIP("::1")})
		if err != nil {
			t.Fatalf("write ICMPv6 AGD1 fragment: %v", err)
		}
		if n != len(wire) {
			t.Fatalf("short ICMPv6 write: %d of %d", n, len(wire))
		}
	}
}

func (c *icmpV6Client) Read(data []byte) (int, error) {
	var wire [1016]byte
	for {
		n, control, source, err := c.packets.ReadFrom(wire[:])
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				return 0, fmt.Errorf("ICMPv6 receive timeout packets=%d last=%s echo-requests=%d last-echo-request=%s app-replies=%d last-app-reply=%s: %w", c.packetCount, c.lastPacket, c.echoRequests, c.lastEchoRequest, c.appReplies, c.lastAppReply, err)
			}
			return 0, err
		}
		c.packetCount++
		if n <= 0 {
			c.lastPacket = fmt.Sprintf("empty packet from=%v", source)
			continue
		}
		message, err := icmp.ParseMessage(58, wire[:n])
		if err != nil {
			c.lastPacket = fmt.Sprintf("unparsed bytes=%d from=%v", n, source)
			continue
		}
		c.lastPacket = fmt.Sprintf("type=%v code=%d from=%v control=%+v bytes=%d", message.Type, message.Code, source, control, n)
		if message.Type == ipv6.ICMPTypeEchoReply {
			c.autoReplies++
			if echo, ok := message.Body.(*icmp.Echo); ok {
				c.lastPacket += fmt.Sprintf(" echo-reply-id=%d seq=%d data=%d", echo.ID, echo.Seq, len(echo.Data))
			}
			continue
		}
		if message.Type != ipv6.ICMPTypeEchoRequest || message.Code != 0 {
			continue
		}
		c.echoRequests++
		echo, ok := message.Body.(*icmp.Echo)
		c.lastEchoRequest = c.lastPacket
		if !ok || echo.ID != int(c.clientChannel) || echo.Seq != int(c.serverChannel) {
			if ok {
				c.lastEchoRequest += fmt.Sprintf(" request-id=%d seq=%d data=%d", echo.ID, echo.Seq, len(echo.Data))
			}
			c.lastPacket = c.lastEchoRequest
			continue
		}
		c.appReplies++
		c.lastAppReply = fmt.Sprintf("id=%d seq=%d bytes=%d source=%v control=%+v", echo.ID, echo.Seq, len(echo.Data), source, control)
		if !icmpV6SourceIsLoopback(source) || control == nil || control.Dst == nil || !control.Dst.Equal(net.ParseIP("::1")) || control.IfIndex != c.interfaceIndex {
			return 0, fmt.Errorf("ICMPv6 response peer/control mismatch: source=%v control=%+v", source, control)
		}
		if len(echo.Data) == 0 || len(echo.Data) > 1000 || len(echo.Data) > len(data) {
			return 0, fmt.Errorf("ICMPv6 response payload length=%d exceeds AGD1/datagram bound", len(echo.Data))
		}
		copy(data, echo.Data)
		return len(echo.Data), nil
	}
}

func icmpV6SourceIsLoopback(address net.Addr) bool {
	switch value := address.(type) {
	case *net.IPAddr:
		return value != nil && value.IP.Equal(net.ParseIP("::1"))
	case *net.UDPAddr:
		return value != nil && value.IP.Equal(net.ParseIP("::1"))
	default:
		return false
	}
}

func (c *icmpV6Client) SetReadDeadline(deadline time.Time) error {
	return c.conn.SetReadDeadline(deadline)
}

func icmpV6ReadResponse(t *testing.T, f icmpV6Fixture, frame adapter.FrameRequest, deadline time.Time) (adapter.FrameResponse, int) {
	t.Helper()
	token := string(readAGD1Payload(t, f.client, deadline))
	plain, err := auth.OpenResponse(token, icmpV6ResponseKeyID, f.responseSigning, "udp-reader", f.responseRecipient,
		auth.ResponseScope{Realm: f.config.Realm, Principal: f.config.Principals[0].ID, Binding: f.config.Principals[0].Binding, Route: icmpV6ResponseRouteID}, time.Now())
	if err != nil {
		t.Fatalf("open protected ICMPv6 response: %v", err)
	}
	var response adapter.FrameResponse
	if err := json.Unmarshal(plain, &response); err != nil {
		t.Fatalf("decode protected ICMPv6 response: %v", err)
	}
	if response.Version != 1 || response.CorrelationID != frame.CorrelationID || response.Sequence != 1 || response.Event != "result" {
		t.Fatalf("ICMPv6 response version/correlation/sequence/event=%d/%q/%d/%q", response.Version, response.CorrelationID, response.Sequence, response.Event)
	}
	return response, len(token)
}

func icmpV6Call(t *testing.T, f icmpV6Fixture, frame adapter.FrameRequest) (adapter.FrameResponse, int) {
	t.Helper()
	f.client.sendFrame(t, frame, f.serverChannel, false, false)
	return icmpV6ReadResponse(t, f, frame, time.Now().Add(6*time.Second))
}

func icmpV6ExpectNoResponse(t *testing.T, f icmpV6Fixture) {
	t.Helper()
	if err := f.client.SetReadDeadline(time.Now().Add(350 * time.Millisecond)); err != nil {
		t.Fatalf("set bounded ICMPv6 no-response deadline: %v", err)
	}
	_, err := f.client.Read(make([]byte, 1001))
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("unexpected ICMPv6 application response or read error: %v", err)
	}
	if err := f.client.SetReadDeadline(time.Time{}); err != nil {
		t.Fatalf("clear ICMPv6 no-response deadline: %v", err)
	}
}
