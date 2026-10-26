package core

import (
	"encoding/json"
	"errors"
)

const MaxPinAckBytes = 4096

type PinAcknowledgement struct {
	Version   int       `json:"version"`
	Operation Operation `json:"operation"`
	Target    Target    `json:"target"`
	ParentID  string    `json:"parent_id"`
}

func PinAckBytes(ack *PinAcknowledgement, request Request) ([]byte, error) {
	if ack == nil || ack.Version != 1 || (ack.Operation != PinsAdd && ack.Operation != PinsRemove) || request.Operation != ack.Operation || request.Target == nil || request.Target.Kind != "room" || !messageID(request.ParentID) || ack.Target != *request.Target || ack.ParentID != request.ParentID {
		return nil, errors.New("invalid pin acknowledgement")
	}
	originalTarget := *request.Target
	target := originalTarget
	parent := request.ParentID
	normalized := request
	normalized.Target = &target
	normalized.ParentID = parent
	if err := normalized.Normalize(); err != nil || originalTarget != *normalized.Target || parent != normalized.ParentID {
		return nil, errors.New("invalid pin acknowledgement")
	}
	data, err := json.Marshal(ack)
	if err != nil || len(data) > MaxPinAckBytes {
		return nil, errors.New("invalid pin acknowledgement")
	}
	return data, nil
}
