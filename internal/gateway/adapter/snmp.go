package adapter

import (
	"context"
	"net"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/snmpwire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// ServeSNMP owns conn and joins cancellation closure. Traps are one-way: no
// transport acknowledgement or delivery receipt is emitted. A single bounded
// receive/admission runs at a time, without a per-source state or pending queue.
// Network, kernel and request-slot loss before durable intake remain possible.
func ServeSNMP(parent context.Context, conn net.PacketConn, a *auth.SNMPAuthenticator, d *dispatch.Dispatcher, requests chan struct{}) error {
	if conn == nil || conn.LocalAddr() == nil || conn.LocalAddr().Network() != "udp" || a == nil || d == nil || cap(requests) < 1 {
		return store.ErrInvalid
	}
	ctx, cancel := context.WithCancel(parent)
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = conn.Close(); close(closed) })
	defer func() {
		cancel()
		_ = conn.Close()
		if !stop() {
			<-closed
		}
	}()
	buffer := make([]byte, snmpwire.MaxPacket+1)
	defer clear(buffer)
	for ctx.Err() == nil {
		n, _, err := conn.ReadFrom(buffer)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		admitSNMP(ctx, buffer[:n], a, d, requests)
		clear(buffer[:n])
	}
	return nil
}

func admitSNMP(ctx context.Context, raw []byte, a *auth.SNMPAuthenticator, d *dispatch.Dispatcher, requests chan struct{}) {
	if ctx.Err() != nil {
		return
	}
	select {
	case requests <- struct{}{}:
		defer func() { <-requests }()
	default:
		return
	}
	proof, err := a.Verify(raw)
	if err != nil {
		return
	}
	admission, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, _ = d.SNMP(admission, proof, time.Now())
}
