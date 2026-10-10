//go:build !darwin && !linux

package adapter

import (
	"errors"
	"net"
)

func OpenUART(string) (net.Conn, error) {
	return nil, errors.New("UART requires Darwin or Linux")
}
