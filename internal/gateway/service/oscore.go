package service

import "net"

type oscoreOwner interface {
	Addr() net.Addr
	Wait() error
	Close() error
}
