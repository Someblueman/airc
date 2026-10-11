package config

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
