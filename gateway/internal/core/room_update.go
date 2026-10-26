package core

import (
	"encoding/json"
	"errors"
)

type RoomPatch struct {
	SlowSeconds  *int64 `json:"slow_seconds,omitempty"`
	HistoryLimit *int   `json:"history_limit,omitempty"`
}

type RoomAcknowledgement struct {
	Version   int        `json:"version"`
	Operation Operation  `json:"operation"`
	Room      RoomResult `json:"room"`
}

func (p *RoomPatch) valid() bool {
	return p != nil && (p.SlowSeconds != nil || p.HistoryLimit != nil) && (p.SlowSeconds == nil || *p.SlowSeconds >= 0 && *p.SlowSeconds <= 3600) && (p.HistoryLimit == nil || *p.HistoryLimit >= 0)
}

func RoomAckBytes(ack *RoomAcknowledgement, request Request) ([]byte, error) {
	if ack == nil || ack.Version != 1 || ack.Operation != RoomUpdate || request.Operation != RoomUpdate || request.Target == nil || request.Target.Kind != "room" || !request.Room.valid() {
		return nil, errors.New("invalid room acknowledgement")
	}
	target := *request.Target
	normalized := request
	normalized.Target = &target
	if err := normalized.Normalize(); err != nil || target != *normalized.Target || ack.Room.Target != *normalized.Target || ack.Room.SlowSeconds < 0 || ack.Room.SlowSeconds > 3600 || ack.Room.HistoryLimit < 0 {
		return nil, errors.New("invalid room acknowledgement")
	}
	if request.Room.SlowSeconds != nil && ack.Room.SlowSeconds != *request.Room.SlowSeconds || request.Room.HistoryLimit != nil && ack.Room.HistoryLimit != *request.Room.HistoryLimit {
		return nil, errors.New("room acknowledgement mismatch")
	}
	data, err := json.Marshal(ack)
	if err != nil || len(data) > MaxRoomBytes {
		return nil, errors.New("invalid room acknowledgement")
	}
	return data, nil
}
