package protocol

import "time"

// AdminRequest is sent after OPER authenticates this connection. Scope is "*"
// for the server or an exact channel name, matching AIRC's channel semantics.
type AdminRequest struct {
	Action  string `json:"action"`
	Nick    string `json:"nick,omitempty"`
	Scope   string `json:"scope,omitempty"`
	Seconds int64  `json:"seconds,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

type ModerationRule struct {
	Kind      string    `json:"kind"`
	Nick      string    `json:"nick"`
	Scope     string    `json:"scope"`
	Reason    string    `json:"reason,omitempty"`
	SetBy     string    `json:"set_by"`
	SetAt     time.Time `json:"set_at"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
}

type AdminResult struct {
	Action  string          `json:"action"`
	Nick    string          `json:"nick,omitempty"`
	Scope   string          `json:"scope,omitempty"`
	Changed bool            `json:"changed"`
	Kicked  int             `json:"kicked"`
	Rule    *ModerationRule `json:"rule,omitempty"`
}
