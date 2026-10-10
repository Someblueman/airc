//go:build darwin || linux

package service

import (
	"context"
	"errors"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/oscore"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func startOSCORE(ctx context.Context, c config.Config, l config.Listener, s *store.Store, d *dispatch.Dispatcher, requests chan struct{}) (oscoreOwner, error) {
	if l.OSCORE == nil {
		return nil, errors.New("missing native OSCORE configuration")
	}
	material, err := auth.ReadOSCOREContext(l.OSCORE.ContextFile)
	if err != nil {
		return nil, err
	}
	owner := material.Owner()
	valid := false
	for _, p := range c.Principals {
		if p.ID == l.OSCORE.Principal && !p.Revoked && owner == (auth.OSCOREOwner{Realm: c.Realm, Principal: p.ID, Binding: p.Binding}) {
			valid = true
			break
		}
	}
	if !valid {
		return nil, errors.New("native OSCORE context does not belong to configured owner")
	}
	return oscore.Start(ctx, oscore.Options{Binary: l.OSCORE.Binary, Address: l.Address, Realm: c.Realm, Store: s, Material: material, Dispatcher: d, Requests: requests})
}
