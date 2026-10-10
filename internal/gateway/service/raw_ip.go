package service

import (
	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

func newRawIPOwner(c config.Config, l config.Listener, d *dispatch.Dispatcher, slots, requests chan struct{}) (*packetOwner, error) {
	if err := l.RawIP.Validate(l.Address); err != nil {
		return nil, err
	}
	driver, _, err := newPacketDriver(c, l, d, slots, requests)
	if err != nil {
		return nil, err
	}
	address, err := adapter.ParseRawIPAddr(l.Address)
	if err != nil {
		return nil, err
	}
	conn, err := adapter.ListenRawIP(address, l.RawIP.Interface)
	if err != nil {
		return nil, err
	}
	return &packetOwner{conn: conn, driver: driver}, nil
}
