package service

import (
	"context"
	"net"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
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

// newLocalPacketDriver is newPacketDriver for local queue, shared memory and
// mapped file listeners: in author mode (no return routes) it serves plain
// author requests and returns the reply destination, if any, as the only
// route for the owner to open.
func newLocalPacketDriver(c config.Config, l config.Listener, d *dispatch.Dispatcher, slots, requests chan struct{}) (packetDriver, []adapter.PacketReturnRoute, error) {
	if !c.LocalAuthorMode(l) {
		return newPacketDriver(c, l, d, slots, requests)
	}
	bound, err := loadBoundAuthor(l.BoundAuthor)
	if err != nil {
		return nil, nil, err
	}
	var reply net.Addr
	switch {
	case l.ReplyTo == "":
	case l.Protocol == "posix_mqueue":
		reply = &adapter.POSIXQueueAddr{Name: l.ReplyTo}
	case l.Protocol == "system_v_queue":
		key, err := config.SystemVQueueKey(l.ReplyTo)
		if err != nil {
			return nil, nil, err
		}
		reply = &adapter.SystemVQueueAddr{Key: key}
	case l.Protocol == "shared_memory":
		reply = &adapter.SharedMemoryAddr{Name: l.ReplyTo}
	case l.Protocol == "mapped_file":
		reply = &adapter.MappedFileAddr{Directory: l.ReplyTo}
	default:
		return nil, nil, store.ErrInvalid
	}
	driver, err := adapter.NewLocalAuthorPackets(d, bound, reply, slots, requests)
	if err != nil {
		return nil, nil, err
	}
	var routes []adapter.PacketReturnRoute
	if reply != nil {
		routes = append(routes, adapter.PacketReturnRoute{ID: "reply", Destination: reply})
	}
	return driver, routes, nil
}

// newUnixgramAuthorOwner binds an author-mode Unix datagram listener: the
// socket keeps its owner-only mode, and replies go to each sender's own
// bound address rather than to provisioned mailboxes.
func newUnixgramAuthorOwner(l config.Listener, d *dispatch.Dispatcher, slots, requests chan struct{}) (*packetOwner, error) {
	driver, err := adapter.NewLocalAuthorPackets(d, nil, nil, slots, requests)
	if err != nil {
		return nil, err
	}
	owner, err := listenUnixgramOwner(l.Address, nil, nil)
	if err != nil {
		return nil, err
	}
	// The shared constructor restricts writes to provisioned mailboxes;
	// author replies go to the sender, so serve the plain socket.
	if checked, ok := owner.conn.(*checkedUnixPacketConn); ok {
		owner.conn = checked.UnixConn
	}
	owner.driver = driver
	return owner, nil
}
