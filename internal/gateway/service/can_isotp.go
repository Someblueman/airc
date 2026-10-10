package service

import (
	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

func newCANISOTPOwner(c config.Config, l config.Listener, d *dispatch.Dispatcher, slots, requests chan struct{}) (*packetOwner, error) {
	address, err := adapter.ParseCANISOTPAddr(l.Address)
	if err != nil {
		return nil, err
	}
	driver, _, err := newPacketDriver(c, l, d, slots, requests)
	if err != nil {
		return nil, err
	}
	conn, err := adapter.ListenCANISOTP(address)
	if err != nil {
		return nil, err
	}
	return &packetOwner{conn: conn, driver: driver}, nil
}
