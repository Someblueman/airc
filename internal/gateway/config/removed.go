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

// removedModes are the configuration keys (and listener protocols) of the
// removed Matrix bridge, GitHub webhook and ChirpStack carriers.
var (
	removedModeTop      = []string{"matrix_bridges"}
	removedModeListener = []string{"github_webhook", "chirpstack"}
)

// removedTop, removedListener and removedLimits name fields of the removed
// gateway identity model (principals, bindings, keys, return routes) and of
// broker routes that pinned a fixed target. Strict decoding would reject them
// anyway; this names the field and the migration note instead.
var (
	removedTop      = []string{"principals", "bindings", "return_routes", "signing_keys", "recipient_keys", "response_signing_key"}
	removedListener = []string{"anonymous_principal", "fifo", "append_log", "directory_mailbox"}
	removedLimits   = []string{"sessions_per_principal", "watches_per_principal", "pending_per_principal"}
	// removedNested are listener option objects whose fields are checked for
	// "principal" (and, for broker routes, a pinned "target").
	removedNested = []string{"oscore", "mach_ipc", "mqtt_sn", "bundle_protocol", "mqtt", "amqp", "stomp"}
)

// RemovedFields reports the first field of the removed identity model or of
// a removed connection mode in a configuration document, with one error that
// points to the matching note.
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

// removedNestedField finds "principal" in an option object, its routes or
// its callers, and "target" in a broker route.
func removedNestedField(raw json.RawMessage) string {
	var option map[string]json.RawMessage
	if json.Unmarshal(raw, &option) != nil {
		return ""
	}
	if _, ok := option["principal"]; ok {
		return "principal"
	}
	for _, list := range []string{"routes", "callers"} {
		var entries []map[string]json.RawMessage
		if json.Unmarshal(option[list], &entries) != nil {
			continue
		}
		for j, entry := range entries {
			for _, key := range []string{"principal", "target"} {
				if _, ok := entry[key]; ok && (key == "principal" || list == "routes" && entry["topic_id"] == nil) {
					return fmt.Sprintf("%s[%d].%s", list, j, key)
				}
			}
		}
	}
	return ""
}

func removed(path string) error {
	return fmt.Errorf("configuration uses removed field %q: the gateway no longer has principals, bindings, keys, return routes or fixed broker targets (requests name their author); see %s", path, RemovedIdentityDoc)
}

func removedMode(path string) error {
	return fmt.Errorf("configuration uses removed field %q: the Matrix bridge, GitHub webhook and ChirpStack carriers were removed; see %s", path, RemovedModesDoc)
}
