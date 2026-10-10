package service

import (
	"context"
	"net"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

type dccpOwner struct {
	listener net.Listener
	driver   *adapter.PacketDatagrams
}

func newDCCPOwner(c config.Config, l config.Listener, d *dispatch.Dispatcher, slots, requests chan struct{}) (*dccpOwner, error) {
	driver, _, err := newPacketDriver(c, l, d, slots, requests)
	if err != nil {
		return nil, err
	}
	listener, err := adapter.ListenDCCP(l.Address)
	if err != nil {
		return nil, err
	}
	return &dccpOwner{listener: listener, driver: driver}, nil
}

func (o *dccpOwner) serve(ctx context.Context) error {
	return o.driver.ServeDCCP(ctx, o.listener)
}
