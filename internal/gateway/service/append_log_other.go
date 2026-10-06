//go:build !darwin && !linux

package service

import (
	"errors"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

func openAppendLog(config.Config, config.Listener, *dispatch.Dispatcher, chan struct{}) (mailboxOwner, error) {
	return nil, errors.New("append log is unavailable on this platform")
}
