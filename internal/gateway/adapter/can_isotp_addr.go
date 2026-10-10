package adapter

import (
	"fmt"
	"net"
	"strings"

	"github.com/Someblueman/airc/internal/gateway/config"
)

// CANISOTPAddr identifies one classical CAN ISO-TP receive/transmit pair.
// CAN identifiers are normal 11-bit identifiers, not SocketCAN EFF/RTR flags.
type CANISOTPAddr struct {
	Interface string
	RXID      uint16
	TXID      uint16
}

func ParseCANISOTPAddr(value string) (*CANISOTPAddr, error) {
	interfaceName, rxID, txID, err := config.ParseCANISOTPEndpoint(value)
	if err != nil {
		return nil, err
	}
	address := &CANISOTPAddr{Interface: interfaceName, RXID: rxID, TXID: txID}
	if !validCANISOTPAddr(address) {
		return nil, fmt.Errorf("invalid CAN ISO-TP endpoint")
	}
	return address, nil
}

func validCANISOTPAddr(address *CANISOTPAddr) bool {
	if address == nil || address.Interface == "" || len(address.Interface) > 15 ||
		address.Interface == "." || address.Interface == ".." || address.RXID > 0x7ff || address.TXID > 0x7ff || address.RXID == address.TXID {
		return false
	}
	for _, r := range address.Interface {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}

func (address *CANISOTPAddr) Network() string { return "can_isotp" }

func (address *CANISOTPAddr) String() string {
	if address == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s/%03x/%03x", address.Interface, address.RXID, address.TXID)
}

func cloneCANISOTPAddr(address *CANISOTPAddr) *CANISOTPAddr {
	if address == nil {
		return nil
	}
	return &CANISOTPAddr{Interface: strings.Clone(address.Interface), RXID: address.RXID, TXID: address.TXID}
}

var _ net.Addr = (*CANISOTPAddr)(nil)
