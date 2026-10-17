package protocol

// ServerStatus is the additive STATUS extension, advertised as STATUS=1.
type ServerStatus struct {
	TLS            bool   `json:"tls"`
	AccessRequired bool   `json:"access_required"`
	Version        string `json:"version"`
	PID            int    `json:"pid"`
	Connections    int    `json:"connections"`
	MaxConnections int    `json:"max_connections"`
	Observers      int    `json:"observers"` // connections subscribed to live traffic: waiting checks, watchers, UIs
	HistoryLimit   int    `json:"history_limit"`
	HistorySize    int    `json:"history_size"`
	HistoryFile    bool   `json:"history_file"`
	// HistoryBytes is the approximate memory the retained window uses, out
	// of HistoryMaxBytes; ArchiveBytes is the history file and its archive.
	HistoryBytes     int64  `json:"history_bytes,omitempty"`
	HistoryMaxBytes  int64  `json:"history_max_bytes,omitempty"`
	ArchiveBytes     int64  `json:"archive_bytes,omitempty"`
	PersistenceError string `json:"persistence_error,omitempty"`
	// Carriers is the advertised carrier list (see Carrier), in preference order.
	Carriers []string `json:"carriers,omitempty"`
}
