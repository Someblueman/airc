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
		"principals":                                 strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[],"principals":[]`, 1),
		"bindings":                                   strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[],"bindings":[]`, 1),
		"return_routes":                              strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[],"return_routes":[]`, 1),
		"recipient_keys":                             strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[],"recipient_keys":[]`, 1),
		"response_signing_key":                       strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[],"response_signing_key":{}`, 1),
		"limits.sessions_per_principal":              strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[],"limits":{"sessions_per_principal":4}`, 1),
		"listeners[0].anonymous_principal":           strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"x","protocol":"udp","anonymous_principal":"a"}]`, 1),
		"listeners[0].fifo":                          strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"x","protocol":"fifo","fifo":{}}]`, 1),
		"listeners[0].oscore.principal":              strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"x","protocol":"coap_oscore","oscore":{"principal":"a"}}]`, 1),
		"listeners[0].mach_ipc.callers[0].principal": strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"x","protocol":"mach_ipc","mach_ipc":{"callers":[{"uid":1,"principal":"a"}]}}]`, 1),
		"listeners[0].mqtt.routes[0].target":         strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"x","protocol":"mqtt","mqtt":{"routes":[{"topic":"t","target":{"kind":"room","name":"#r"}}]}}]`, 1),
		"listeners[0].amqp.routes[0].target":         strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"x","protocol":"amqp","amqp":{"routes":[{"request_address":"a","response_address":"b","target":{"kind":"room","name":"#r"}}]}}]`, 1),
		"listeners[0].mqtt_sn.routes[0].principal":   strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"x","protocol":"mqtt_sn","mqtt_sn":{"routes":[{"principal":"a","topic_id":1,"target":{"kind":"room","name":"#r"}}]}}]`, 1),
	} {
		_, err := Parse([]byte(input))
		if err == nil || !strings.Contains(err.Error(), `"`+field+`"`) || !strings.Contains(err.Error(), RemovedIdentityDoc) {
			t.Errorf("%s: %v", field, err)
		}
	}
	// MQTT-SN topic routes keep their fixed target.
	sn := strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"x","protocol":"mqtt_sn","operations":["send"],"mqtt_sn":{"routes":[{"topic_id":1,"target":{"kind":"room","name":"#r"}}]}}]`, 1)
	if _, err := Parse([]byte(sn)); err != nil {
		t.Errorf("MQTT-SN topic target refused: %v", err)
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
