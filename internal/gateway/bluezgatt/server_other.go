//go:build !linux

package bluezgatt

import (
	"context"
	"errors"
)

func Start(context.Context, string, string, []Peer, Handler, chan struct{}) (*Server, error) {
	return nil, errors.New("BlueZ GATT requires Linux")
}
