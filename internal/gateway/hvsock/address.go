package hvsock

import (
	"fmt"
	"regexp"
	"strings"
)

var canonicalGUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

const (
	guidZero       = "00000000-0000-0000-0000-000000000000"
	guidBroadcast  = "ffffffff-ffff-ffff-ffff-ffffffffffff"
	guidChildren   = "90db8b89-0d35-4f79-8ce9-49ea0ac8b7cd"
	guidLoopback   = "e0e16197-dd56-4a10-9195-5ee7a155a838"
	guidParent     = "a42e7cda-d03f-480c-9cc2-a4de20abb878"
	guidSiloHost   = "36bd0c5c-7276-4223-88ba-7d03b654c568"
	vsockNamespace = "-facb-11e6-bd58-64006a7986d3"
)

var errUnsupported = fmt.Errorf("Hyper-V sockets are available only on Windows")

func ValidateGuest(value string) error {
	if !canonicalGUID.MatchString(value) {
		return fmt.Errorf("guest VM ID must be a canonical lowercase UUID")
	}
	switch value {
	case guidZero, guidBroadcast, guidChildren, guidLoopback, guidParent, guidSiloHost:
		return fmt.Errorf("guest VM ID is a reserved Hyper-V address")
	}
	return nil
}

func ValidateService(value string) error {
	if !canonicalGUID.MatchString(value) {
		return fmt.Errorf("service ID must be a canonical lowercase UUID")
	}
	switch value {
	case guidZero, guidBroadcast, guidChildren, guidLoopback, guidParent, guidSiloHost:
		return fmt.Errorf("service ID is reserved")
	}
	if strings.HasSuffix(value, vsockNamespace) {
		return fmt.Errorf("service ID uses the reserved VSOCK template namespace")
	}
	return nil
}
