package core

const (
	MaxMembers      = 256
	MaxMembersBytes = 48 << 10
)

// Member preserves the native nickname spelling and room operator marker.
type Member struct {
	Nick     string `json:"nick"`
	Operator bool   `json:"operator"`
}

// MembersResult is a complete native NAMES snapshot, never a truncated list.
type MembersResult struct {
	Target  Target   `json:"target"`
	Members []Member `json:"members"`
}
