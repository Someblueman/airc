package adapter

import (
	"fmt"

	"github.com/Someblueman/airc/internal/gateway/config"
)

// RFCOMMAddr identifies a local Bluetooth RFCOMM address and channel.
// Address bytes use the conventional human-readable BD_ADDR order.
type RFCOMMAddr struct {
	Address [6]byte
	Channel uint8
}

func ParseRFCOMMAddr(value string) (*RFCOMMAddr, error) {
	address, channel, err := config.ParseRFCOMMEndpoint(value)
	if err != nil {
		return nil, err
	}
	return &RFCOMMAddr{Address: address, Channel: channel}, nil
}

func (a *RFCOMMAddr) Network() string { return "bluetooth_rfcomm" }

func (a *RFCOMMAddr) String() string {
	if a == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x/%d", a.Address[0], a.Address[1], a.Address[2], a.Address[3], a.Address[4], a.Address[5], a.Channel)
}
