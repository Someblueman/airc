package service

import (
	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

func newICMPv6Owner(l config.Listener, d *dispatch.Dispatcher, slots, requests chan struct{}) (*packetOwner, error) {
	if err := l.ICMPv6.Validate(l.Address); err != nil {
		return nil, err
	}
	// Plain author requests, answered to the Echo source.
	driver, err := adapter.NewAuthorDatagrams(d, slots, requests)
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
