package service

import (
	"context"
	"net"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

func newICMPv6Owner(c config.Config, l config.Listener, d *dispatch.Dispatcher, slots, requests chan struct{}) (*packetOwner, error) {
	if err := l.ICMPv6.Validate(l.Address); err != nil {
		return nil, err
	}
	var driver interface {
		Serve(context.Context, net.PacketConn) error
	}
	var err error
	if c.LegacyRoutes(l.ID) {
		driver, _, err = newPacketDriver(c, l, d, slots, requests)
	} else {
		// Plain author requests, answered to the Echo source.
		driver, err = adapter.NewAuthorDatagrams(d, slots, requests)
	}
	if err != nil {
		return nil, err
	}
	address, err := adapter.ParseICMPv6Addr(l.Address)
	if err != nil {
		return nil, err
	}
	conn, err := adapter.ListenICMPv6(address, l.ICMPv6.Interface)
	if err != nil {
		return nil, err
	}
	return &packetOwner{conn: conn, driver: driver}, nil
}
