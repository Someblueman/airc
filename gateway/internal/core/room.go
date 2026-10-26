package core

const MaxRoomBytes = 4096

type RoomResult struct {
	Target       Target `json:"target"`
	SlowSeconds  int64  `json:"slow_seconds"`
	HistoryLimit int    `json:"history_limit"`
}
