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
	bundleProtocolBlock
	amqpBlock
	stompBlock
	snmpBlock
	signedSyslogBlock
	syslogBlock
	mqttBlock
	mqttSNBlock
	offlineBundleBlock
	uartBlock
	fifoBlock
	oscoreBlock
	webRTCBlock
)

// listenerBlocks validate the owner's options; on any other listener a
// block whose options are set is refused with misuse.
var listenerBlocks = [...]struct {
	set      func(Listener) bool // nil: the block has no options of its own
	misuse   string
	validate func(Config, Listener) error
}{
	localREPBlock:       {validate: Config.validateLocalREP},
	fileMailboxBlock:    {validate: Config.validateFileMailbox},
	bundleProtocolBlock: {func(l Listener) bool { return l.BundleProtocol != nil }, "bundle_protocol requires bpv7 protocol", Config.validateBundleProtocol},
	amqpBlock:           {func(l Listener) bool { return l.AMQP != nil }, "AMQP options require amqp protocol", Config.validateAMQP},
	stompBlock:          {func(l Listener) bool { return l.STOMP != nil }, "STOMP options require stomp protocol", Config.validateSTOMP},
	snmpBlock:           {func(l Listener) bool { return l.SNMP != nil }, "SNMP options require snmp_v3 protocol", Config.validateSNMP},
	signedSyslogBlock:   {func(l Listener) bool { return l.SignedSyslog != nil }, "signed syslog options require syslog_signed protocol", Config.validateSignedSyslog},
	syslogBlock:         {func(l Listener) bool { return l.Syslog != nil }, "syslog options require a TLS syslog protocol", Config.validateSyslog},
	mqttBlock:           {func(l Listener) bool { return l.MQTT != nil }, "MQTT options require mqtt protocol", Config.validateMQTT},
	mqttSNBlock:         {func(l Listener) bool { return l.MQTTSN != nil }, "mqtt_sn options require mqtt_sn protocol", Config.validateMQTTSN},
	offlineBundleBlock:  {validate: Config.validateOfflineBundle},
	uartBlock:           {validate: Config.validateUART},
	fifoBlock:           {validate: Config.validateFIFO},
	oscoreBlock:         {func(l Listener) bool { return l.OSCORE != nil }, "OSCORE options require a coap_oscore listener", Config.validateOSCORE},
	webRTCBlock:         {func(l Listener) bool { return l.WebRTC != nil }, "WebRTC options require a WebRTC listener", Config.validateWebRTCListener},
}

func (Config) validateWebRTCListener(l Listener) error {
	if l.TLSCertFile == "" {
		return errors.New("webrtc listener requires TLS certificates")
	}
	return validateWebRTC(l.WebRTC)
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
