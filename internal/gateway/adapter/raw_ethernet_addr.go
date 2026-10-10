package adapter

import (
	"net"
	"strconv"

	"github.com/Someblueman/airc/internal/gateway/config"
)

// RawEthernetAddr identifies a unicast MAC and its application channel.
type RawEthernetAddr struct {
	MAC     net.HardwareAddr
	Channel uint16
}

func ParseRawEthernetAddr(endpoint string) (*RawEthernetAddr, error) {
	mac, channel, err := config.ParseRawEthernetEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	return &RawEthernetAddr{MAC: append(net.HardwareAddr(nil), mac...), Channel: channel}, nil
}

func (a *RawEthernetAddr) Network() string { return "raw_ethernet" }

func (a *RawEthernetAddr) String() string {
	if a == nil {
		return "<nil>"
	}
	return a.MAC.String() + "/" + strconv.Itoa(int(a.Channel))
}

func validRawEthernetAddr(address *RawEthernetAddr) bool {
	return address != nil && address.Channel != 0 && validRawEthernetMAC(address.MAC)
}

func validRawEthernetMAC(mac net.HardwareAddr) bool {
	if len(mac) != 6 || mac[0]&1 != 0 {
		return false
	}
	var allZero, allBroadcast byte
	for _, octet := range mac {
		allZero |= octet
		allBroadcast |= ^octet
	}
	return allZero != 0 && allBroadcast != 0
}
