package config

import (
	"strings"
	"testing"
)

func TestIPv4MulticastConfigurationBoundary(t *testing.T) {
	if err := (&IPv4Multicast{Interface: "lo0"}).Validate("239.1.2.3:9001"); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"127.0.0.1:9001", "224.0.0.1:9001", "[ff02::1]:9001", "239.1.2.3:0", "239.1.2.3:09001", "239.1.2.3:65536", "239.1.2.3:9001 ", "example.com:9001"} {
		if err := (&IPv4Multicast{Interface: "lo0"}).Validate(address); err == nil {
			t.Fatal("accepted endpoint", address)
		}
	}
	for _, name := range []string{"", " lo0", "lo0 ", "lo/0", "lo\n0", "lo\x000", strings.Repeat("a", 65)} {
		if err := (&IPv4Multicast{Interface: name}).Validate("239.1.2.3:9001"); err == nil {
			t.Fatal("accepted interface", name)
		}
	}
	if err := (&IPv4Multicast{Interface: strings.Repeat("a", 64)}).Validate("239.255.1.2:65535"); err != nil {
		t.Fatal("rejected configured upper bounds", err)
	}
	if err := (*IPv4Multicast)(nil).Validate("239.1.2.3:9001"); err == nil {
		t.Fatal("accepted missing interface")
	}
}
