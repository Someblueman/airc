//go:build !linux

package adapter

import (
	"errors"
	"net"
)

func ListenCANISOTP(*CANISOTPAddr) (net.PacketConn, error) {
	return nil, errors.New("CAN ISO-TP requires Linux")
}

func RunCANISOTPWorker(string, string) error { return errors.New("CAN ISO-TP requires Linux") }
