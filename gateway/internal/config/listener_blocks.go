package config

import (
	"errors"
	"slices"
)

// block is one per-listener sub-block validator. A transport names the
// blocks it owns; Validate runs every block in listenerBlocks order, so the
// first error a listener reports does not depend on its protocol.
type block uint8

const (
	localREPBlock block = iota
	fileMailboxBlock
	mqttBlock
)

// listenerBlocks validate the owner's options; on any other listener a
// block whose options are set is refused with misuse.
var listenerBlocks = [...]struct {
	set      func(Listener) bool // nil: the block has no options of its own
	misuse   string
	validate func(Config, Listener) error
}{
	localREPBlock:    {validate: Config.validateLocalREP},
	fileMailboxBlock: {validate: Config.validateFileMailbox},
	mqttBlock:        {func(l Listener) bool { return l.MQTT != nil }, "MQTT options require mqtt protocol", Config.validateMQTT},
}

// validateBlocks runs every sub-block validator on one listener.
func (c Config) validateBlocks(t Transport, l Listener) error {
	for id, b := range listenerBlocks {
		var err error
		if slices.Contains(t.blocks, block(id)) {
			err = b.validate(c, l)
		} else if b.set != nil && b.set(l) {
			err = errors.New(b.misuse)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
