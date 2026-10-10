package config

import "testing"

func TestUDPLiteEndpointAndIntegrityPolicy(t *testing.T) {
	for _, address := range []string{"127.0.0.1:9001", "169.254.1.2:9001", "0.0.0.0:0", "[::]:0", "[::1]:65535", "[2001:db8::1]:9001", "[fe80::1%lo0]:9001"} {
		if err := (&UDPLite{ChecksumCoverage: "full"}).Validate(address); err != nil {
			t.Fatal(address, err)
		}
	}
	for _, address := range []string{"example.com:9001", "239.1.2.3:9001", "255.255.255.255:9001", "[ff12::1234]:9001", "[::ffff:127.0.0.1]:9001", "[::1%lo0]:9001", "[fe80::1]:9001", "[fe80::1%bad name]:9001", "127.0.0.1:-1", "127.0.0.1:09001", "127.0.0.1:65536", "[0:0:0:0:0:0:0:1]:9001"} {
		if err := (&UDPLite{ChecksumCoverage: "full"}).Validate(address); err == nil {
			t.Fatal("accepted endpoint", address)
		}
	}
	for _, policy := range []string{"", "partial", "header", "0", "8", "FULL", "full "} {
		if err := (&UDPLite{ChecksumCoverage: policy}).Validate("127.0.0.1:9001"); err == nil {
			t.Fatal("accepted integrity policy", policy)
		}
	}
	if err := (*UDPLite)(nil).Validate("127.0.0.1:9001"); err == nil {
		t.Fatal("accepted missing integrity policy")
	}
}
