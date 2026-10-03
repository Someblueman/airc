package irc

import (
	"encoding/json"
	"errors"

	"github.com/Someblueman/airc/internal/tokenfmt"
)

// AuthenticateAdmin sends OPER; success is reported by numeric 381, failure by
// 464. Privileges belong to this connection and are lost on reconnect.
func (c *Client) AuthenticateAdmin(token string) error {
	if !c.Supports("ADMIN") {
		return errors.New("administration needs a daemon configured with ADMIN")
	}
	if !tokenfmt.Valid(token) {
		return errors.New("invalid admin credential")
	}
	return c.Raw("OPER :" + token)
}

// Moderate sends an administrative request. Read AdminEvent and
// EndOfAdminEvent, or an error numeric; sending alone does not confirm success.
func (c *Client) Moderate(request AdminRequest) error {
	if !c.Supports("ADMIN") {
		return errors.New("administration needs a daemon configured with ADMIN")
	}
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	return c.Raw("ADMIN :" + string(data))
}

type AdminEvent struct {
	Type string `json:"type"`
	AdminResult
}

func (*AdminEvent) ircEvent() {}

type EndOfAdminEvent struct {
	Type string `json:"type"`
}

func (*EndOfAdminEvent) ircEvent() {}
