package config

import (
	"fmt"
	"strings"

	"github.com/Someblueman/airc/internal/protocol"
)

// validateCarriers checks the advertised carrier list: the ordered ways a
// client can reach this AIRC service, published to gateway clients by
// capabilities.get exactly as aircd publishes them in 005 CARRIERS.
func (c Config) validateCarriers() error {
	if len(c.Carriers) == 0 {
		return nil
	}
	if _, err := protocol.ParseCarriers(strings.Join(c.Carriers, ",")); err != nil {
		return fmt.Errorf("carriers: %w", err)
	}
	return nil
}
