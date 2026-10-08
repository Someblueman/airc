//go:build !darwin || !arm64

package sysvqueue

import "context"

type Queue struct{}

func Create(int32) (*Queue, error)                              { return nil, ErrUnsupported }
func Open(int32) (*Queue, error)                                { return nil, ErrUnsupported }
func (*Queue) Identity() Identity                               { return Identity{} }
func (*Queue) Close() error                                     { return ErrUnsupported }
func (*Queue) ReadMessage(context.Context, []byte) (int, error) { return 0, ErrUnsupported }
func (*Queue) WriteMessage(context.Context, []byte) error       { return ErrUnsupported }
