package adapter

import (
	"errors"
	"github.com/Someblueman/airc/internal/gateway/config"
	"net"
	"time"
)

// UDPLitePackets owns an actual UDP-Lite socket with full checksum coverage.
// Checksum protection is independent of authenticated application framing.
type UDPLitePackets struct {
	conn *net.UDPConn
	ipv4 bool
}

func (c *UDPLitePackets) ReadFrom(body []byte) (int, net.Addr, error) { return c.conn.ReadFrom(body) }
func (c *UDPLitePackets) WriteTo(body []byte, destination net.Addr) (int, error) {
	addr, ok := destination.(*net.UDPAddr)
	if !ok || addr == nil || addr.Port < 1 || addr.Port > 65535 || addr.IP.IsUnspecified() || (addr.IP.To4() != nil) != c.ipv4 {
		return 0, errors.New("UDP-Lite replies require same-family unicast")
	}
	if err := (&config.UDPLite{ChecksumCoverage: "full"}).Validate(addr.String()); err != nil {
		return 0, err
	}
	return c.conn.WriteToUDP(body, addr)
}
func (c *UDPLitePackets) LocalAddr() net.Addr                { return c.conn.LocalAddr() }
func (c *UDPLitePackets) Close() error                       { return c.conn.Close() }
func (c *UDPLitePackets) SetDeadline(t time.Time) error      { return c.conn.SetDeadline(t) }
func (c *UDPLitePackets) SetReadDeadline(t time.Time) error  { return c.conn.SetReadDeadline(t) }
func (c *UDPLitePackets) SetWriteDeadline(t time.Time) error { return c.conn.SetWriteDeadline(t) }
