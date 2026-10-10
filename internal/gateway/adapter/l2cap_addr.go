package adapter

import (
	"fmt"

	"github.com/Someblueman/airc/internal/gateway/config"
)

// L2CAPAddr identifies a local Bluetooth BR/EDR PSM. Address bytes use the
// conventional human-readable BD_ADDR order.
type L2CAPAddr struct {
	Address [6]byte
	PSM     uint16
}

func ParseL2CAPAddr(value string) (*L2CAPAddr, error) {
	address, psm, err := config.ParseL2CAPEndpoint(value)
	if err != nil {
		return nil, err
	}
	return &L2CAPAddr{Address: address, PSM: psm}, nil
}

func (a *L2CAPAddr) Network() string { return "bluetooth_l2cap" }

func (a *L2CAPAddr) String() string {
	if a == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x/%d", a.Address[0], a.Address[1], a.Address[2], a.Address[3], a.Address[4], a.Address[5], a.PSM)
}
