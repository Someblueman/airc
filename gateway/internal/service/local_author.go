package service

import (
	"context"
	"net"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
)

// loadBoundAuthor reads a local listener's bound author once at startup:
// its account token (if any) comes from an owner-only file and stays in
// memory. nil means requests must name their own author.
func loadBoundAuthor(bound *config.BoundAuthor) (*dispatch.BoundAuthor, error) {
	if bound == nil {
		return nil, nil
	}
	token := ""
	if bound.AccountTokenFile != "" {
		var err error
		if token, err = auth.ReadToken(bound.AccountTokenFile); err != nil {
			return nil, err
		}
	}
	return dispatch.NewBoundAuthor(bound.Nick, token)
}

type packetDriver interface {
	Serve(context.Context, net.PacketConn) error
}

// newUnixgramOwner binds a Unix datagram listener: the socket keeps its
// owner-only mode, and replies go to each sender's own bound address.
func newUnixgramOwner(l config.Listener, d *dispatch.Dispatcher, slots, requests chan struct{}) (*packetOwner, error) {
	driver, err := adapter.NewLocalAuthorPackets(d, nil, nil, slots, requests)
	if err != nil {
		return nil, err
	}
	return listenUnixgramOwner(l.Address, driver)
}
