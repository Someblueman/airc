package service

import (
	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

func newICMPv4Owner(c config.Config, l config.Listener, d *dispatch.Dispatcher, slots, requests chan struct{}) (*packetOwner, error) {
	if err := l.ICMPv4.Validate(l.Address); err != nil {
		return nil, err
	}
	driver, _, err := newPacketDriver(c, l, d, slots, requests)
	if err != nil {
		return nil, err
	}
	address, err := adapter.ParseICMPv4Addr(l.Address)
	if err != nil {
		return nil, err
	}
	conn, err := adapter.ListenICMPv4(address, l.ICMPv4.Interface)
	if err != nil {
		return nil, err
	}
	return &packetOwner{conn: conn, driver: driver}, nil
}
