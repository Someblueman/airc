package xmppwire

import (
	"encoding/xml"
	"strings"
)

// Message is bounded parsed data, not an author or freshness proof. The verified
// owner must validate discovery, current membership and application admission.
type Message struct {
	Occupant string
	Room     string
	ID       string
	Body     string
}

func GroupMessage(e *Element, room, recipient string) (Message, error) {
	if e == nil || e.Name != (xml.Name{Space: "jabber:client", Local: "message"}) || !BareJID(room) {
		return Message{}, ErrElement
	}
	if _, _, ok := FullJID(recipient); !ok {
		return Message{}, ErrElement
	}
	attrs := map[string]string{}
	for _, a := range e.Attributes {
		if a.Name.Space == "" {
			attrs[a.Name.Local] = a.Value
		}
	}
	bare, _, ok := FullJID(attrs["from"])
	if !ok || bare != room || attrs["type"] != "groupchat" || attrs["to"] != "" && attrs["to"] != recipient {
		return Message{}, ErrElement
	}
	var body *Element
	id := ""
	for _, c := range e.Content {
		if c.Element == nil {
			continue
		}
		n := c.Element
		if n.Name == (xml.Name{Space: "jabber:client", Local: "body"}) {
			if body != nil || len(n.Attributes) != 0 {
				return Message{}, ErrElement
			}
			body = n
		}
		if n.Name == (xml.Name{Space: "urn:xmpp:sid:0", Local: "stanza-id"}) {
			fields := map[string]string{}
			for _, a := range n.Attributes {
				if a.Name.Space == "" {
					fields[a.Name.Local] = a.Value
				}
			}
			if fields["by"] != room {
				continue
			}
			if id != "" || len(n.Content) != 0 || len(fields["id"]) < 1 || len(fields["id"]) > 256 || !validText(fields["id"]) || strings.ContainsAny(fields["id"], "\x00\r\n") {
				return Message{}, ErrElement
			}
			id = fields["id"]
		}
	}
	if body == nil || id == "" {
		return Message{}, ErrElement
	}
	text, err := body.Text()
	if err != nil || strings.TrimSpace(text) == "" {
		return Message{}, ErrElement
	}
	return Message{Occupant: attrs["from"], Room: room, ID: id, Body: text}, nil
}
