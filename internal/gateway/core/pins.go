package core

import "github.com/Someblueman/airc/pkg/irc"

const (
	MaxPins      = 5
	MaxPinsBytes = 64 << 10
)

// PinsResult is the bounded snapshot returned by pins.read.
type PinsResult struct {
	Target Target                `json:"target"`
	Pins   []irc.MessageMetadata `json:"pins"`
}
