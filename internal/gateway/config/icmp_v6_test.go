package config

import (
	"net/netip"
	"testing"
)

func TestParseICMPv6Endpoint(t *testing.T) {
	for _, input := range []string{"[::1]:1", "[2001:db8::1]:65535", "[fd00::1]:42"} {
		address, channel, err := ParseICMPv6Endpoint(input)
		if err != nil || !address.Is6() || channel == 0 {
			t.Fatalf("ParseICMPv6Endpoint(%q) = %v, %d, %v", input, address, channel, err)
		}
	}
	for _, input := range []string{
		"127.0.0.1:1", "[::ffff:127.0.0.1]:1", "[ff02::1]:1", "[::]:1", "[fe80::1]:1",
		"[::1%lo0]:1", "[0:0:0:0:0:0:0:1]:1", "[::1]:0", "[::1]:65536", "[::1]:01",
		"[::1]:+1", "[::1]: 1", "example.com:1", "::1:1",
	} {
		if _, _, err := ParseICMPv6Endpoint(input); err == nil {
			t.Errorf("ParseICMPv6Endpoint(%q) accepted invalid endpoint", input)
		}
	}
	address, channel, err := ParseICMPv6Endpoint("[::1]:65535")
	if err != nil || address != netip.MustParseAddr("::1") || channel != 65535 {
		t.Fatalf("upper channel boundary: %v, %d, %v", address, channel, err)
	}
}

func TestICMPv6OptionsRequireInterfaceAndCanonicalEndpoint(t *testing.T) {
	for _, iface := range []string{"en0", "lo0", "bridge.1"} {
		if err := (&ICMPv6{Interface: iface}).Validate("[::1]:7"); err != nil {
			t.Errorf("interface %q rejected: %v", iface, err)
		}
	}
	for _, iface := range []string{"", " has-space", "bad/name", "bad\\name", "bad\x00name", "bad\nname"} {
		if err := (&ICMPv6{Interface: iface}).Validate("[::1]:7"); err == nil {
			t.Errorf("unsafe interface %q accepted", iface)
		}
	}
	if err := (*ICMPv6)(nil).Validate("[::1]:7"); err == nil {
		t.Fatal("accepted missing ICMPv6 options")
	}
}
