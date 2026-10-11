//go:build darwin || linux

package service

import (
	"context"
	"errors"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
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
	if owner != (auth.OSCOREOwner{Realm: c.Realm, Listener: l.ID}) {
		return nil, errors.New("native OSCORE context belongs to another realm or listener")
	}
	// The native worker multiplexes the context's one peer onto a private
	// socket: one session for the context, optionally bound to its default
	// author. There is no client IP, so the source is the listener.
	if !d.AuthorAccess() {
		return nil, errors.New("native OSCORE requires an author listener")
	}
	session := dispatch.NewSession()
	if a := l.OSCORE.Author; a != nil {
		token := ""
		if a.TokenFile != "" {
			if token, err = auth.ReadToken(a.TokenFile); err != nil {
				return nil, err
			}
		}
		author, credential, err := core.NewAuthor(a.Nick, token)
		if err != nil {
			return nil, errors.New("invalid OSCORE default author")
		}
		session = dispatch.NewBoundSession(author, credential)
	}
	plain := &dispatch.Ingress{Source: d.Source(nil), Session: session}
	return oscore.Start(ctx, oscore.Options{Binary: l.OSCORE.Binary, Address: l.Address, Realm: c.Realm, Store: s, Material: material, Dispatcher: d, Requests: requests, Plain: plain})
}
