package service

import (
	"context"
	"net"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/Someblueman/airc/gateway/internal/store"
)

// newUDPOwner binds a UDP listener: it carries plain author requests and
// replies to each request's source. CoAP over UDP is served as ordinary CoAP
// request/response.
func newUDPOwner(l config.Listener, d *dispatch.Dispatcher, slots, requests chan struct{}) (*packetOwner, error) {
	address, err := net.ResolveUDPAddr("udp", l.Address)
	if err != nil {
		return nil, err
	}
	if l.Protocol == "coap_udp" {
		conn, err := net.ListenUDP("udp", address)
		if err != nil {
			return nil, err
		}
		return &packetOwner{conn: conn, driver: coapUDPDriver{d: d, slots: slots, requests: requests}}, nil
	}
	driver, err := adapter.NewAuthorDatagrams(d, slots, requests)
	if err != nil {
		return nil, err
	}
	if l.Protocol != "udp" {
		return nil, store.ErrInvalid
	}
	conn, err := net.ListenUDP("udp", address)
	if err != nil {
		return nil, err
	}
	return &packetOwner{conn: conn, driver: driver}, nil
}

type coapUDPDriver struct {
	d               *dispatch.Dispatcher
	slots, requests chan struct{}
}

func (c coapUDPDriver) Serve(ctx context.Context, conn net.PacketConn) error {
	udp, ok := conn.(*net.UDPConn)
	if !ok {
		return store.ErrInvalid
	}
	return adapter.ServeCoAPUDP(ctx, udp, c.d, c.slots, c.requests)
}
