package xmppwire

import (
	"encoding/xml"
	"errors"
	"strings"
)

var ErrResponseCapacity = errors.New("XMPP response exceeds element capacity")

// ResponseMessage encodes one encrypted result as a complete XMPP chat stanza.
// The recipient is the verified occupant JID and the id is the signed request
// identifier; neither value is derived from caller-controlled XML.
func ResponseMessage(to, id, token string) ([]byte, error) {
	if len(token) > 64<<10 {
		return nil, ErrResponseCapacity
	}
	bare, resource, ok := FullJID(to)
	if !ok || bare+"/"+resource != to || !canonicalRequestID(id) || !compactJWE(token) {
		return nil, ErrElement
	}
	body := &Element{
		Name:    xml.Name{Space: "jabber:client", Local: "body"},
		Content: []Content{{Text: token}},
	}
	message := &Element{
		Name: xml.Name{Space: "jabber:client", Local: "message"},
		Attributes: []xml.Attr{
			{Name: xml.Name{Local: "to"}, Value: to},
			{Name: xml.Name{Local: "id"}, Value: id},
			{Name: xml.Name{Local: "type"}, Value: "chat"},
		},
		Content: []Content{{Element: body}},
	}
	encoded, err := Encode(message)
	if err != nil {
		return nil, ErrResponseCapacity
	}
	return encoded, nil
}

func canonicalRequestID(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for _, c := range value {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func compactJWE(token string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 5 || parts[0] == "" || parts[2] == "" || parts[3] == "" || parts[4] == "" {
		return false
	}
	for i, part := range parts {
		if i == 1 && part == "" { // ECDH-ES direct uses an empty encrypted-key segment.
			continue
		}
		for _, c := range part {
			if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				return false
			}
		}
	}
	return true
}
