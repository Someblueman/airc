package adapter

import (
	"strconv"

	"github.com/Someblueman/airc/internal/gateway/config"
)

// VSockAddr identifies a virtio-vsock endpoint by context ID and port.
type VSockAddr struct {
	CID  uint32
	Port uint32
}

func ParseVSockAddr(endpoint string) (*VSockAddr, error) {
	cid, port, err := config.ParseVSockEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	return &VSockAddr{CID: cid, Port: port}, nil
}

func (a *VSockAddr) Network() string { return "virtio_vsock" }

func (a *VSockAddr) String() string {
	if a == nil {
		return "<nil>"
	}
	return strconv.FormatUint(uint64(a.CID), 10) + ":" + strconv.FormatUint(uint64(a.Port), 10)
}
