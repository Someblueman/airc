package service

import (
	"context"
	"net"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// newAuthorPacketOwner binds a UDP-family listener without return routes: it
// carries plain author requests and replies to each request's source. CoAP
// over UDP is served as ordinary CoAP request/response.
func newAuthorPacketOwner(l config.Listener, d *dispatch.Dispatcher, slots, requests chan struct{}) (*packetOwner, error) {
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
	var conn net.PacketConn
	switch l.Protocol {
	case "udp_lite":
		conn, err = adapter.ListenUDPLite(address)
	case "ipv4_multicast":
		conn, err = adapter.ListenIPv4Multicast(address, l.IPv4Multicast.Interface)
	case "ipv4_broadcast":
		conn, err = adapter.ListenIPv4Broadcast(address, l.IPv4Broadcast.Interface, l.IPv4Broadcast.Subnet)
	case "ipv6_multicast":
		conn, err = adapter.ListenIPv6Multicast(address, l.IPv6Multicast.Interface)
	case "udp":
		conn, err = net.ListenUDP("udp", address)
	default:
		return nil, store.ErrInvalid
	}
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
