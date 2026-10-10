package config

import (
	"strings"
	"testing"
)

func TestIPv4BroadcastEndpointBoundary(t *testing.T) {
	for _, pair := range [][2]string{{"192.0.2.0/24", "192.0.2.255:9001"}, {"192.0.2.0/24", "255.255.255.255:9001"}, {"198.51.100.4/30", "198.51.100.7:65535"}, {"10.16.0.0/12", "10.31.255.255:9001"}} {
		if err := (&IPv4Broadcast{Interface: "en0", Subnet: pair[0]}).Validate(pair[1]); err != nil {
			t.Fatal(pair, err)
		}
	}
	for _, subnet := range []string{"", "192.0.2.1/24", "192.0.2.0/024", "192.0.2.0/31", "192.0.2.1/32", "0.0.0.0/0", "0.0.0.0/8", "224.0.0.0/24", "2001:db8::/64"} {
		if err := (&IPv4Broadcast{Interface: "en0", Subnet: subnet}).Validate("255.255.255.255:9001"); err == nil {
			t.Fatal("accepted subnet", subnet)
		}
	}
	for _, address := range []string{"192.0.2.1:9001", "192.0.2.0:9001", "198.51.100.255:9001", "239.1.2.3:9001", "0.0.0.0:9001", "[::1]:9001", "example.com:9001", "192.0.2.255:0", "192.0.2.255:09001", "192.0.2.255:65536"} {
		if err := (&IPv4Broadcast{Interface: "en0", Subnet: "192.0.2.0/24"}).Validate(address); err == nil {
			t.Fatal("accepted endpoint", address)
		}
	}
	for _, name := range []string{"", " en0", "en0 ", "en/0", "en\\0", "en\n0", "en\x000", strings.Repeat("a", 65)} {
		if err := (&IPv4Broadcast{Interface: name, Subnet: "192.0.2.0/24"}).Validate("192.0.2.255:9001"); err == nil {
			t.Fatal("accepted interface", name)
		}
	}
	if err := (&IPv4Broadcast{Interface: strings.Repeat("a", 64), Subnet: "192.0.2.0/24"}).Validate("192.0.2.255:65535"); err != nil {
		t.Fatal(err)
	}
	if err := (*IPv4Broadcast)(nil).Validate("192.0.2.255:9001"); err == nil {
		t.Fatal("accepted missing options")
	}
}
