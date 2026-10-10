package xmppwire

import (
	"encoding/xml"
	"strings"
)

const xmppPingNamespace = "urn:xmpp:ping"

// PingReply accepts only a server XEP-0199 ping addressed to this XMPP client.
// Other IQ requests remain outside the bridge's response surface.
func PingReply(e *Element, domain, recipient string) ([]byte, bool) {
	if e == nil || e.Name != (xml.Name{Space: "jabber:client", Local: "iq"}) ||
		!BareJID("airc@"+domain) || len(recipient) == 0 {
		return nil, false
	}
	bare, resource, validRecipient := FullJID(recipient)
	if !validRecipient || bare+"/"+resource != recipient {
		return nil, false
	}

	attrs := make(map[string]string, len(e.Attributes))
	for _, attr := range e.Attributes {
		if attr.Name.Space != "" || attrs[attr.Name.Local] != "" {
			return nil, false
		}
		attrs[attr.Name.Local] = attr.Value
	}
	if len(attrs) != len(e.Attributes) {
		return nil, false
	}
	for name := range attrs {
		if name != "id" && name != "type" && name != "from" && name != "to" {
			return nil, false
		}
	}
	id, hasID := attrs["id"]
	if !hasID || len(id) < 1 || len(id) > 256 || !validText(id) || attrs["type"] != "get" {
		return nil, false
	}
	if from, ok := attrs["from"]; ok && from != domain {
		return nil, false
	}
	if to, ok := attrs["to"]; ok && to != recipient {
		return nil, false
	}
	var ping *Element
	for _, content := range e.Content {
		if strings.TrimSpace(content.Text) != "" {
			return nil, false
		}
		if content.Element != nil {
			if ping != nil {
				return nil, false
			}
			ping = content.Element
		}
	}
	if ping == nil || ping.Name != (xml.Name{Space: xmppPingNamespace, Local: "ping"}) || len(ping.Attributes) != 0 || len(ping.Content) != 0 {
		return nil, false
	}

	result := &Element{
		Name: xml.Name{Space: "jabber:client", Local: "iq"},
		Attributes: []xml.Attr{
			{Name: xml.Name{Local: "type"}, Value: "result"},
			{Name: xml.Name{Local: "id"}, Value: id},
		},
	}
	if from, ok := attrs["from"]; ok {
		result.Attributes = append(result.Attributes, xml.Attr{Name: xml.Name{Local: "to"}, Value: from})
	}
	encoded, err := Encode(result)
	if err != nil || len(encoded) > MaxElement {
		return nil, false
	}
	return encoded, true
}
