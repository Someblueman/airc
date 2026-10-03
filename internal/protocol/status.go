package protocol

// ServerStatus is the additive STATUS extension, advertised as STATUS=1.
type ServerStatus struct {
	TLS              bool   `json:"tls"`
	AccessRequired   bool   `json:"access_required"`
	Version          string `json:"version"`
	PID              int    `json:"pid"`
	Connections      int    `json:"connections"`
	MaxConnections   int    `json:"max_connections"`
	Observers        int    `json:"observers"` // connections subscribed to live traffic: waiting checks, watchers, UIs
	HistoryLimit     int    `json:"history_limit"`
	HistorySize      int    `json:"history_size"`
	HistoryFile      bool   `json:"history_file"`
	PersistenceError string `json:"persistence_error,omitempty"`
}
