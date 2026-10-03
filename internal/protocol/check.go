package protocol

// CheckRequest is one bounded snapshot of room headers and history pages.
// Observation is established separately before a waiting client's snapshot.
type CheckRequest struct {
	Targets     []CheckTarget `json:"targets"`
	Headers     bool          `json:"headers,omitempty"`
	IncludeOwn  bool          `json:"include_own,omitempty"`
	MaxMessages int           `json:"max_messages"`
}

type CheckTarget struct {
	Target string `json:"t"`
	After  string `json:"a,omitempty"`
	Limit  int    `json:"n"`
}

type CheckEntry struct {
	Targets []int            `json:"targets,omitempty"`
	Kind    string           `json:"kind"`
	Target  string           `json:"target"`
	Message *MessageMetadata `json:"message,omitempty"`
	Topic   string           `json:"topic,omitempty"`
	Status  string           `json:"status,omitempty"`
	Cursor  string           `json:"cursor,omitempty"`
	Gap     bool             `json:"gap,omitempty"`
	Warning string           `json:"warning,omitempty"`
}
