package config

import (
	"strings"
	"testing"
)

// Removed connection modes must fail loudly instead of being ignored.
func TestRemovedConnectionModesAreRejected(t *testing.T) {
	for _, key := range []string{"sqs", "s3", "sqlite_mailboxes", "couchdb_changes"} {
		input := strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[],"`+key+`":[]`, 1)
		if _, err := Parse([]byte(input)); err == nil {
			t.Errorf("accepted removed top-level field %s", key)
		}
	}
	for _, key := range []string{"nats", "twilio_sms", "postmark_email", "raw_ip", "icmp_v4", "raw_ethernet", "virtio_vsock", "rfcomm", "l2cap", "ble_gatt", "ftps_inbox"} {
		input := strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"x","protocol":"http","`+key+`":{}}]`, 1)
		if _, err := Parse([]byte(input)); err == nil {
			t.Errorf("accepted removed listener field %s", key)
		}
	}
	for _, protocol := range []string{"nats", "twilio_sms", "postmark_email", "raw_ip", "icmp_v4", "raw_ethernet", "virtio_vsock", "dccp", "bluetooth_rfcomm", "bluetooth_l2cap", "ble_gatt", "can_isotp", "canfd_isotp", "ftps_inbox"} {
		input := strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"x","protocol":"`+protocol+`","address":"127.0.0.1:1","enabled":true}]`, 1)
		if _, err := Parse([]byte(input)); err == nil || !strings.Contains(err.Error(), "not implemented") {
			t.Errorf("enabled removed protocol %s: %v", protocol, err)
		}
	}
}
