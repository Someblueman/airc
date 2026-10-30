package config

import (
	"encoding/json"
	"fmt"
	"slices"
)

// RemovedIdentityDoc explains the removed gateway identity model.
const RemovedIdentityDoc = "docs/gateway/removed-identity.md"

// RemovedModesDoc lists removed connection modes.
const RemovedModesDoc = "docs/gateway/removed-modes.md"

// RemovedCarriersDoc lists the carriers removed on 2026-10-26 and 2026-10-30.
const RemovedCarriersDoc = "docs/gateway/removed-carriers.md"

// removedModes are the configuration keys (and listener protocols) of the
// removed Matrix bridge, GitHub webhook and ChirpStack carriers.
var (
	removedModeTop      = []string{"matrix_bridges"}
	removedModeListener = []string{"github_webhook", "chirpstack"}
)

// removedCarrierTop are the top-level sections of the removed bridges,
// removedCarrierProtocols the removed listener protocols (the 2026-10-26
// cut, then webrtc, dtls and coaps on 2026-10-30) and removedCarrierOptions
// the listener option objects (and reply_to) that only a removed carrier had.
var (
	removedCarrierTop       = []string{"redis_pubsub", "redis_streams", "irc_bridges", "xmpp_bridges", "sftp", "postgres_notify"}
	removedCarrierProtocols = []string{
		"fifo", "uart", "system_v_queue", "posix_mqueue", "shared_memory", "mapped_file", "mach_ipc",
		"udp_lite", "ipv4_multicast", "ipv6_multicast", "ipv4_broadcast", "icmp_v6",
		"sctp", "sctp_unordered", "sctp_pr_ttl", "coap_tcp", "coaps_tcp", "coap_oscore", "mqtt_sn",
		"amqp", "stomp", "scp_inbox", "offline_bundle", "bpv7", "syslog_tls", "syslog_signed", "snmp_v3",
		"webrtc", "dtls", "coaps",
	}
	removedCarrierOptions = []string{
		"fifo", "udp_lite", "ipv4_broadcast", "ipv6_multicast", "icmp_v6", "ipv4_multicast",
		"snmp", "signed_syslog", "syslog", "stomp", "amqp", "mqtt_sn", "bundle_protocol",
		"posix_queue", "shared_memory", "mach_ipc", "oscore", "scp_inbox", "reply_to", "webrtc",
	}
)

// removedTop, removedListener and removedLimits name fields of the removed
// gateway identity model (principals, bindings, keys, return routes) and of
// MQTT topic routes that pinned a fixed target. Strict decoding would reject
// them anyway; this names the field and the migration note instead.
var (
	removedTop      = []string{"principals", "bindings", "return_routes", "signing_keys", "recipient_keys", "response_signing_key"}
	removedListener = []string{"anonymous_principal", "append_log", "directory_mailbox"}
	removedLimits   = []string{"sessions_per_principal", "watches_per_principal", "pending_per_principal"}
	// removedNested are listener option objects whose routes are checked for
	// a pinned "target".
	removedNested = []string{"mqtt"}
)

// RemovedFields reports the first field of the removed identity model, of a
// removed connection mode or of a removed carrier in a configuration
// document, with one error that points to the matching note.
// A document that is not a JSON object is left to the strict decoder.
func RemovedFields(data []byte) error {
	var top map[string]json.RawMessage
	if json.Unmarshal(data, &top) != nil {
		return nil
	}
	for _, key := range removedTop {
		if _, ok := top[key]; ok {
			return removed(key)
		}
	}
	for _, key := range removedModeTop {
		if _, ok := top[key]; ok {
			return removedMode(key)
		}
	}
	for _, key := range removedCarrierTop {
		if _, ok := top[key]; ok {
			return removedCarrier(key, key)
		}
	}
	var limits map[string]json.RawMessage
	if json.Unmarshal(top["limits"], &limits) == nil {
		for _, key := range removedLimits {
			if _, ok := limits[key]; ok {
				return removed("limits." + key)
			}
		}
	}
	var listeners []map[string]json.RawMessage
	if json.Unmarshal(top["listeners"], &listeners) != nil {
		return nil
	}
	for i, l := range listeners {
		for _, key := range removedModeListener {
			if _, ok := l[key]; ok {
				return removedMode(fmt.Sprintf("listeners[%d].%s", i, key))
			}
		}
		var protocol string
		if json.Unmarshal(l["protocol"], &protocol) == nil && slices.Contains(removedModeListener, protocol) {
			return removedMode(fmt.Sprintf("listeners[%d].protocol", i))
		}
		if slices.Contains(removedCarrierProtocols, protocol) {
			return removedCarrier(fmt.Sprintf("listeners[%d].protocol", i), protocol)
		}
		for _, key := range removedCarrierOptions {
			if _, ok := l[key]; ok {
				return removedCarrier(fmt.Sprintf("listeners[%d].%s", i, key), key)
			}
		}
		for _, key := range removedListener {
			if _, ok := l[key]; ok {
				return removed(fmt.Sprintf("listeners[%d].%s", i, key))
			}
		}
		for _, option := range removedNested {
			if path := removedNestedField(l[option]); path != "" {
				return removed(fmt.Sprintf("listeners[%d].%s.%s", i, option, path))
			}
		}
	}
	return nil
}

// removedNestedField finds "principal" in an option object or its routes,
// and "target" in a topic route.
func removedNestedField(raw json.RawMessage) string {
	var option map[string]json.RawMessage
	if json.Unmarshal(raw, &option) != nil {
		return ""
	}
	if _, ok := option["principal"]; ok {
		return "principal"
	}
	var routes []map[string]json.RawMessage
	if json.Unmarshal(option["routes"], &routes) != nil {
		return ""
	}
	for j, route := range routes {
		for _, key := range []string{"principal", "target"} {
			if _, ok := route[key]; ok {
				return fmt.Sprintf("routes[%d].%s", j, key)
			}
		}
	}
	return ""
}

func removed(path string) error {
	return fmt.Errorf("configuration uses removed field %q: the gateway no longer has principals, bindings, keys, return routes or fixed topic targets (requests name their author); see %s", path, RemovedIdentityDoc)
}

func removedMode(path string) error {
	return fmt.Errorf("configuration uses removed field %q: the Matrix bridge, GitHub webhook and ChirpStack carriers were removed; see %s", path, RemovedModesDoc)
}

// removedCarrier names a field (a bridge section, a listener protocol or a
// listener option) of a removed carrier.
func removedCarrier(path, name string) error {
	return fmt.Errorf("configuration uses removed field %q: the %s carrier was removed from the gateway; see %s", path, name, RemovedCarriersDoc)
}
