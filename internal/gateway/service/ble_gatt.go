package service

import (
	"context"
	"crypto/ecdsa"
	"slices"
	"sync"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/bluezgatt"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type bleGATTPeer struct {
	options   config.BLEGATTPeer
	binding   string
	recipient *ecdsa.PublicKey
}

type bleGATTOwner struct {
	server *bluezgatt.Server
	sealer *auth.ResponseSealer
	once   sync.Once
	err    error
}

func openBLEGATT(ctx context.Context, c config.Config, l config.Listener, d *dispatch.Dispatcher, slots, requests chan struct{}) (*bleGATTOwner, error) {
	if l.BLEGATT == nil || d == nil || cap(slots) < 1 || cap(requests) < 1 {
		return nil, store.ErrInvalid
	}
	options := *l.BLEGATT
	options.Peers = slices.Clone(options.Peers)
	l.BLEGATT = &options
	l.Operations = slices.Clone(l.Operations)
	peers := make(map[string]bleGATTPeer, len(options.Peers))
	transportPeers := make([]bluezgatt.Peer, 0, len(options.Peers))
	for _, peer := range options.Peers {
		owner := bleGATTPeer{options: peer}
		for _, principal := range c.Principals {
			if principal.ID == peer.Principal && !principal.Revoked {
				owner.binding = principal.Binding
				break
			}
		}
		if owner.binding == "" {
			return nil, auth.ErrCredential
		}
		var err error
		owner.recipient, err = auth.ReadResponseRecipientKey(peer.RecipientPublicKeyFile)
		if err != nil {
			return nil, err
		}
		peers[peer.Address] = owner
		transportPeers = append(transportPeers, bluezgatt.Peer{Address: peer.Address, AddressType: peer.AddressType})
	}
	sealer, err := loadSignedResponseSigner(c)
	if err != nil {
		return nil, err
	}
	o := &bleGATTOwner{sealer: sealer}
	handler := func(call context.Context, address string, transfer [8]byte, request []byte) ([]byte, error) {
		peer, exists := peers[address]
		if !exists {
			return nil, auth.ErrCredential
		}
		select {
		case requests <- struct{}{}:
			defer func() { <-requests }()
		case <-call.Done():
			return nil, call.Err()
		}
		return adapter.ProcessBLEGATT(call, d, sealer, peer.recipient, c.Realm, peer.binding, l, peer.options, transfer, request)
	}
	o.server, err = bluezgatt.Start(ctx, l.Address, options.AdapterAddress, transportPeers, handler, slots)
	if err != nil {
		sealer.Close()
		return nil, err
	}
	return o, nil
}

func (o *bleGATTOwner) serve(ctx context.Context) error { return o.server.Run(ctx) }

func (o *bleGATTOwner) Close() error {
	o.once.Do(func() {
		o.err = o.server.Close()
		o.sealer.Close()
	})
	return o.err
}
