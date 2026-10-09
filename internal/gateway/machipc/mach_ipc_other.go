//go:build !darwin || !arm64

package machipc

import "context"

type Listener struct{}

func Listen(string) (*Listener, error)                      { return nil, ErrUnavailable }
func (*Listener) Receive(context.Context) (*Request, error) { return nil, ErrUnavailable }
func (*Listener) Close() error                              { return nil }
func (*Request) Reply(context.Context, []byte) error        { return ErrUnavailable }
func (*Request) Close() error                               { return nil }
