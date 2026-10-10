package server

import "testing"

func TestChannelValidation(t *testing.T) {
	for _, channel := range []string{"#research", "&local"} {
		if !validChannel(channel) {
			t.Errorf("validChannel(%q) = false", channel)
		}
	}
	for _, channel := range []string{"", "research", "#bad name", "#bad,channel", "#bad:channel", "#", string([]byte{'#', 0xff})} {
		if validChannel(channel) {
			t.Errorf("validChannel(%q) = true", channel)
		}
	}
}
