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

// Configurations of the removed identity model fail with one error naming
// the field and the migration note, not a generic strict-decoding error.
func TestRemovedIdentityFieldsPointToTheMigrationNote(t *testing.T) {
	for field, input := range map[string]string{
		"principals":                         strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[],"principals":[]`, 1),
		"bindings":                           strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[],"bindings":[]`, 1),
		"return_routes":                      strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[],"return_routes":[]`, 1),
		"recipient_keys":                     strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[],"recipient_keys":[]`, 1),
		"response_signing_key":               strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[],"response_signing_key":{}`, 1),
		"limits.sessions_per_principal":      strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[],"limits":{"sessions_per_principal":4}`, 1),
		"listeners[0].anonymous_principal":   strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"x","protocol":"udp","anonymous_principal":"a"}]`, 1),
		"listeners[0].mqtt.routes[0].target": strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"x","protocol":"mqtt","mqtt":{"routes":[{"topic":"t","target":{"kind":"room","name":"#r"}}]}}]`, 1),
	} {
		_, err := Parse([]byte(input))
		if err == nil || !strings.Contains(err.Error(), `"`+field+`"`) || !strings.Contains(err.Error(), RemovedIdentityDoc) {
			t.Errorf("%s: %v", field, err)
		}
	}
}

// A configuration naming a carrier removed on 2026-10-26 (a bridge section,
// a listener protocol or an option object only that carrier had) fails with
// one error naming the field and the removed-carriers note, before any other
// check.
func TestRemovedCarriersPointToTheNote(t *testing.T) {
	listener := func(fields string) string {
		return strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"x",`+fields+`}]`, 1)
	}
	cases := map[string]string{
		"listeners[0].reply_to":        listener(`"protocol":"unixgram","address":"/private/gram.sock","reply_to":"/private/out.sock"`),
		"listeners[0].oscore":          listener(`"protocol":"coap_udp","oscore":{"principal":"a"}`),
		"listeners[0].mach_ipc":        listener(`"protocol":"udp","mach_ipc":{"callers":[{"uid":1,"principal":"a"}]}`),
		"listeners[0].amqp":            listener(`"protocol":"mqtt","amqp":{"routes":[{"request_address":"a","response_address":"b","target":{"kind":"room","name":"#r"}}]}`),
		"listeners[0].mqtt_sn":         listener(`"protocol":"udp","mqtt_sn":{"routes":[{"topic_id":1,"target":{"kind":"room","name":"#r"}}]}`),
		"listeners[0].fifo":            listener(`"protocol":"stdio","fifo":{}`),
		"listeners[0].bundle_protocol": listener(`"protocol":"file_mailbox","address":"/private/mailbox","bundle_protocol":{}`),
		"listeners[0].webrtc":          listener(`"protocol":"http","webrtc":{"ice_transport_policy":"all"}`),
	}
	for _, section := range []string{"redis_pubsub", "redis_streams", "irc_bridges", "xmpp_bridges", "sftp", "postgres_notify"} {
		cases[section] = strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[],"`+section+`":[]`, 1)
	}
	for _, protocol := range []string{"fifo", "uart", "system_v_queue", "posix_mqueue", "shared_memory", "mapped_file", "mach_ipc", "udp_lite", "ipv4_multicast", "ipv6_multicast", "ipv4_broadcast", "icmp_v6", "sctp", "sctp_unordered", "sctp_pr_ttl", "coap_tcp", "coaps_tcp", "coap_oscore", "mqtt_sn", "amqp", "stomp", "scp_inbox", "offline_bundle", "bpv7", "syslog_tls", "syslog_signed", "snmp_v3", "webrtc", "dtls", "coaps"} {
		for _, enabled := range []string{"true", "false"} {
			cases["listeners[0].protocol/"+protocol+"/"+enabled] = listener(`"protocol":"` + protocol + `","address":"127.0.0.1:1","enabled":` + enabled)
		}
	}
	for name, input := range cases {
		field, _, _ := strings.Cut(name, "/")
		_, err := Parse([]byte(input))
		if err == nil || !strings.Contains(err.Error(), `"`+field+`"`) || !strings.Contains(err.Error(), RemovedCarriersDoc) || strings.Contains(err.Error(), RemovedIdentityDoc) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The kept carriers still parse.
	for _, protocol := range []string{"udp", "coap_udp", "mqtt", "zeromq", "rawquic"} {
		if _, err := Parse([]byte(listener(`"protocol":"` + protocol + `","address":"127.0.0.1:1"`))); err != nil {
			t.Errorf("%s refused: %v", protocol, err)
		}
	}
}

// The removed Matrix bridge, GitHub webhook and ChirpStack carriers fail with
// one error naming the field and the removed-modes note.
func TestRemovedBridgeCarriersPointToTheNote(t *testing.T) {
	for field, input := range map[string]string{
		"matrix_bridges":              strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[],"matrix_bridges":[]`, 1),
		"listeners[0].github_webhook": strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"x","protocol":"http","github_webhook":{}}]`, 1),
		"listeners[0].chirpstack":     strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"x","protocol":"http","chirpstack":{}}]`, 1),
		"listeners[0].protocol":       strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"x","protocol":"chirpstack","address":"127.0.0.1:1"}]`, 1),
	} {
		_, err := Parse([]byte(input))
		if err == nil || !strings.Contains(err.Error(), `"`+field+`"`) || !strings.Contains(err.Error(), RemovedModesDoc) {
			t.Errorf("%s: %v", field, err)
		}
	}
}
