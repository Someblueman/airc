package config

import (
	"fmt"
	"strings"
	"testing"
)

func TestIPv6MulticastConfigurationBoundary(t *testing.T) {
	for _, scope := range []int{1, 2, 3, 4, 5, 8, 14} {
		address := fmt.Sprintf("[ff1%x::1234]:9001", scope)
		if err := (&IPv6Multicast{Interface: "lo0"}).Validate(address); err != nil {
			t.Fatalf("defined scope %x: %v", scope, err)
		}
	}
	for _, address := range []string{
		"[::1]:9001", "239.1.2.3:9001", "[::ffff:239.1.2.3]:9001",
		"[ff02::1]:9001", "[ff32::1234]:9001", "[ff92::1234]:9001",
		"[ff10::1234]:9001", "[ff16::1234]:9001", "[ff1f::1234]:9001",
		"[ff12::]:9001", "[ff12::1234%lo0]:9001", "[FF12::1234]:9001",
		"[ff12:0:0:0:0:0:0:1234]:9001", "ff12::1234:9001",
		"[ff12::1234]:0", "[ff12::1234]:09001", "[ff12::1234]:65536",
		"[ff12::1234]:9001 ", "example.com:9001",
	} {
		if err := (&IPv6Multicast{Interface: "lo0"}).Validate(address); err == nil {
			t.Fatal("accepted endpoint", address)
		}
	}
	for _, name := range []string{"", " lo0", "lo0 ", "lo/0", "lo\\0", "lo\n0", "lo\x000", strings.Repeat("a", 65)} {
		if err := (&IPv6Multicast{Interface: name}).Validate("[ff12::1234]:9001"); err == nil {
			t.Fatal("accepted interface", name)
		}
	}
	if err := (&IPv6Multicast{Interface: strings.Repeat("a", 64)}).Validate("[ff1e::1234]:65535"); err != nil {
		t.Fatal("rejected configured upper bounds", err)
	}
	if err := (*IPv6Multicast)(nil).Validate("[ff12::1234]:9001"); err == nil {
		t.Fatal("accepted missing interface")
	}
}
