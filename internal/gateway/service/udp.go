package service

import (
	"context"
	"net"
	"net/netip"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type udpOwner struct {
	conn   *net.UDPConn
	driver *adapter.UDPDatagrams
}

func newUDPOwner(c config.Config, l config.Listener, d *dispatch.Dispatcher, slots, requests chan struct{}) (*udpOwner, error) {
	if c.ResponseSigningKey == nil {
		return nil, store.ErrInvalid
	}
	private, err := auth.ReadResponseSigningKey(c.ResponseSigningKey.PrivateKeyFile)
	if err != nil {
		return nil, err
	}
	defer clear(private)
	sealer, err := auth.NewResponseSealer(c.ResponseSigningKey.ID, private)
	if err != nil {
		return nil, err
	}
	bindings := make(map[string]string, len(c.Principals))
	for _, principal := range c.Principals {
		bindings[principal.ID] = principal.Binding
	}
	routes := make([]adapter.UDPReturnRoute, 0)
	for _, route := range c.ReturnRoutes {
		if route.Protocol != "udp" || route.Listener != l.ID {
			continue
		}
		recipient, err := auth.ReadResponseRecipientKey(route.RecipientPublicKeyFile)
		if err != nil {
			return nil, err
		}
		destination, err := netip.ParseAddrPort(route.Destination)
		if err != nil {
			return nil, store.ErrInvalid
		}
		routes = append(routes, adapter.UDPReturnRoute{ID: route.ID, Principal: route.Principal, Binding: bindings[route.Principal], RecipientID: route.RecipientKeyID, Destination: destination, Recipient: recipient})
	}
	driver, err := adapter.NewUDPDatagrams(d, sealer, c.Realm, routes, slots, requests)
	if err != nil {
		return nil, err
	}
	address, err := net.ResolveUDPAddr("udp", l.Address)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", address)
	if err != nil {
		return nil, err
	}
	return &udpOwner{conn: conn, driver: driver}, nil
}

func (o *udpOwner) addr() net.Addr                  { return o.conn.LocalAddr() }
func (o *udpOwner) close() error                    { return o.conn.Close() }
func (o *udpOwner) serve(ctx context.Context) error { return o.driver.Serve(ctx, o.conn) }
