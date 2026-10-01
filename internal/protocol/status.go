package protocol

// ServerStatus is the additive STATUS extension, advertised as STATUS=1.
type ServerStatus struct {
	Version          string `json:"version"`
	PID              int    `json:"pid"`
	Connections      int    `json:"connections"`
	MaxConnections   int    `json:"max_connections"`
	HistoryLimit     int    `json:"history_limit"`
	HistorySize      int    `json:"history_size"`
	HistoryFile      bool   `json:"history_file"`
	PersistenceError string `json:"persistence_error,omitempty"`
}
