//go:build !darwin && !linux

package service

import (
	"context"
	"errors"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func startOSCORE(context.Context, config.Config, config.Listener, *store.Store, *dispatch.Dispatcher, chan struct{}) (oscoreOwner, error) {
	return nil, errors.New("native OSCORE listener is unavailable on this platform")
}
