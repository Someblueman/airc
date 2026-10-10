package auth

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/Someblueman/airc/internal/gateway/jsoninput"
)

type chirpStackUplink struct {
	DeduplicationID string `json:"deduplicationId"`
	Time            string `json:"time"`
	FCnt            uint32 `json:"fCnt"`
	FPort           uint32 `json:"fPort"`
	Confirmed       bool   `json:"confirmed"`
	Data            string `json:"data"`
	DeviceInfo      struct {
		TenantID      string `json:"tenantId"`
		ApplicationID string `json:"applicationId"`
		DevEUI        string `json:"devEui"`
	} `json:"deviceInfo"`
}

func parseChirpStackUplink(payload []byte) (chirpStackUplink, error) {
	var event chirpStackUplink
	if err := jsoninput.ValidateDepth(payload, MaxChirpStackBytes, 16); err != nil {
		return event, err
	}
	root, err := chirpStackFields(payload, "deduplicationId", "time", "fCnt", "fPort", "confirmed", "data", "deviceInfo")
	if err != nil {
		return event, err
	}
	device, err := chirpStackFields(root["deviceInfo"], "tenantId", "applicationId", "devEui")
	if err != nil {
		return event, err
	}
	root["deviceInfo"], err = json.Marshal(device)
	if err != nil {
		return event, err
	}
	selected, err := json.Marshal(root)
	if err != nil {
		return event, err
	}
	// Protobuf JSON omits scalar defaults: absent confirmed means false and
	// absent fCnt means zero. Selected nulls and case aliases are still refused.
	err = jsoninput.Decode(selected, &event, MaxChirpStackBytes)
	return event, err
}

// Provider metadata may grow without changing authority. Retain only the
// fixed admission fields, rejecting case aliases before Go's JSON decoder.
func chirpStackFields(raw []byte, names ...string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, errors.New("expected ChirpStack event object")
	}
	selected := make(map[string]json.RawMessage, len(names))
	for key, value := range object {
		for _, name := range names {
			if strings.EqualFold(key, name) {
				if key != name {
					return nil, errors.New("case-alias ChirpStack event field")
				}
				selected[name] = value
			}
		}
	}
	return selected, nil
}
