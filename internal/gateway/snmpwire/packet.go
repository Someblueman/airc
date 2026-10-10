// Package snmpwire decodes bounded authenticated SNMPv3 USM trap messages.
// Parsing and MAC verification confer no AIRC identity or timeliness authority.
package snmpwire

import (
	"bytes"
	"errors"
	"unicode/utf8"
)

const MaxPacket = 8192
const MaxVariables = 18 // Mandatory uptime/trap OID plus at most sixteen fields.
var ErrPacket = errors.New("invalid or unsupported SNMPv3 trap")

type Packet struct {
	Raw, EngineID, ContextEngineID         []byte
	User, Context, Notification            string
	MessageID, RequestID                   int32
	Boots, Time, Uptime                    uint32
	Variables                              []Variable
	authenticationStart, authenticationEnd int
}
type Variable struct {
	OID      string
	Type     byte
	Bytes    []byte
	Integer  int64
	Unsigned uint64
	ObjectID string
}

func Parse(input []byte) (Packet, error) {
	if len(input) == 0 || len(input) > MaxPacket {
		return Packet{}, ErrPacket
	}
	raw := bytes.Clone(input)
	outer := reader{raw, 0, len(raw)}
	root, ok := outer.child(48)
	if !ok || !outer.done() {
		return Packet{}, ErrPacket
	}
	version, ok := root.signed()
	if !ok || version != 3 {
		return Packet{}, ErrPacket
	}
	header, ok := root.child(48)
	if !ok {
		return Packet{}, ErrPacket
	}
	id, ok := header.signed()
	if !ok || id < 0 {
		return Packet{}, ErrPacket
	}
	max, ok := header.signed()
	if !ok || max < 484 {
		return Packet{}, ErrPacket
	}
	flags, ok := header.octets(1)
	// Unconfirmed traps ignore reportable/reserved bits per RFC 3412.
	if !ok || len(flags) != 1 || flags[0]&byte(3) != 1 {
		return Packet{}, ErrPacket
	}
	model, ok := header.signed()
	if !ok || model != 3 || !header.done() {
		return Packet{}, ErrPacket
	}
	securityOctets, ok := root.child(4)
	if !ok {
		return Packet{}, ErrPacket
	}
	security, ok := securityOctets.child(48)
	if !ok || !securityOctets.done() {
		return Packet{}, ErrPacket
	}
	engine, ok := security.octets(32)
	if !ok || !ValidEngineID(engine) {
		return Packet{}, ErrPacket
	}
	boots, ok := security.unsigned(2, 31)
	if !ok {
		return Packet{}, ErrPacket
	}
	clock, ok := security.unsigned(2, 31)
	if !ok {
		return Packet{}, ErrPacket
	}
	user, ok := security.octets(32)
	if !ok || len(user) == 0 || !utf8.Valid(user) {
		return Packet{}, ErrPacket
	}
	mac, ok := security.child(4)
	if !ok || (mac.end-mac.pos != 12 && mac.end-mac.pos != 24) {
		return Packet{}, ErrPacket
	}
	privacy, ok := security.octets(0)
	if !ok || len(privacy) != 0 || !security.done() {
		return Packet{}, ErrPacket
	}
	scoped, ok := root.child(48)
	if !ok || !root.done() {
		return Packet{}, ErrPacket
	}
	contextEngine, ok := scoped.octets(32)
	if !ok || !ValidEngineID(contextEngine) {
		return Packet{}, ErrPacket
	}
	contextName, ok := scoped.octets(32)
	if !ok || !utf8.Valid(contextName) {
		return Packet{}, ErrPacket
	}
	pdu, ok := scoped.child(167)
	if !ok || !scoped.done() {
		return Packet{}, ErrPacket
	}
	requestID, ok := pdu.signed()
	if !ok {
		return Packet{}, ErrPacket
	}
	for range 2 {
		n, ok := pdu.signed()
		if !ok || n != 0 {
			return Packet{}, ErrPacket
		}
	}
	list, ok := pdu.child(48)
	if !ok || !pdu.done() {
		return Packet{}, ErrPacket
	}
	var fields []Variable
	seen := map[string]bool{}
	for !list.done() {
		if len(fields) >= MaxVariables {
			return Packet{}, ErrPacket
		}
		binding, ok := list.child(48)
		if !ok {
			return Packet{}, ErrPacket
		}
		name, ok := binding.oid()
		if !ok || seen[name] {
			return Packet{}, ErrPacket
		}
		seen[name] = true
		tag, value, ok := binding.next()
		if !ok || !binding.done() {
			return Packet{}, ErrPacket
		}
		field, ok := parseVariable(name, tag, value)
		if !ok {
			return Packet{}, ErrPacket
		}
		fields = append(fields, field)
	}
	if len(fields) < 2 || fields[0].OID != "1.3.6.1.2.1.1.3.0" || fields[0].Type != 67 || fields[1].OID != "1.3.6.1.6.3.1.1.4.1.0" || fields[1].Type != 6 {
		return Packet{}, ErrPacket
	}
	return Packet{raw, engine, contextEngine, string(user), string(contextName), fields[1].ObjectID, id, requestID, uint32(boots), uint32(clock), uint32(fields[0].Unsigned), fields[2:], mac.pos, mac.end}, nil
}

// ValidEngineID applies the RFC 3411 identity width and reserved-value rules.
func ValidEngineID(id []byte) bool {
	if len(id) < 5 || len(id) > 32 {
		return false
	}
	var any byte
	all := byte(255)
	for _, b := range id {
		any |= b
		all &= b
	}
	return any != 0 && all != 255
}
