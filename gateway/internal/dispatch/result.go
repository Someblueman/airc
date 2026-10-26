package dispatch

import (
	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/core"
)

// DetachedResult is the plain contract result every carrier returns. It has
// exactly one operation-specific result on success.
type DetachedResult struct {
	Version      int                        `json:"version"`
	Operation    core.Operation             `json:"operation"`
	Result       *core.Result               `json:"result,omitempty"`
	Page         *backend.HistoryPage       `json:"page,omitempty"`
	Check        *core.CheckResult          `json:"check,omitempty"`
	Directory    *backend.DirectorySnapshot `json:"directory,omitempty"`
	Channels     *core.ChannelsResult       `json:"channels,omitempty"`
	Agents       *core.AgentsResult         `json:"agents,omitempty"`
	Context      *core.ContextResult        `json:"context,omitempty"`
	Pins         *core.PinsResult           `json:"pins,omitempty"`
	Room         *core.RoomResult           `json:"room,omitempty"`
	Waiting      *core.WaitingResult        `json:"waiting,omitempty"`
	Poll         *core.PollResult           `json:"poll,omitempty"`
	Topic        *core.TopicResult          `json:"topic,omitempty"`
	Members      *core.MembersResult        `json:"members,omitempty"`
	Capabilities *Capabilities              `json:"capabilities,omitempty"`
	// Account is AIRC's answer to account.register and account.identify.
	Account *backend.Account `json:"account,omitempty"`
}
