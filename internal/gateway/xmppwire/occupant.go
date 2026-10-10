package xmppwire

import "encoding/xml"

// Occupant is server-supplied data, not an identity proof. A verified session
// owner must bind it to a provisioned non-anonymous room before retaining it.
type Occupant struct {
	Address  string
	RealJID  string
	Departed bool
}

// Presence requires a canonical full real JID on arrival. Departure removes
// only the exact occupant address; a nickname-change hint never transfers trust.
func Presence(e *Element, room, recipient string) (Occupant, error) {
	if e == nil || e.Name != (xml.Name{Space: "jabber:client", Local: "presence"}) || !BareJID(room) {
		return Occupant{}, ErrElement
	}
	if _, _, ok := FullJID(recipient); !ok {
		return Occupant{}, ErrElement
	}
	attrs := map[string]string{}
	for _, a := range e.Attributes {
		if a.Name.Space == "" {
			attrs[a.Name.Local] = a.Value
		}
	}
	bare, _, ok := FullJID(attrs["from"])
	if !ok || bare != room || attrs["to"] != "" && attrs["to"] != recipient {
		return Occupant{}, ErrElement
	}
	if attrs["type"] != "" && attrs["type"] != "unavailable" {
		return Occupant{}, ErrElement
	}
	x, err := child(e, "jabber:client", "presence", mucUser, "x")
	if err != nil {
		return Occupant{}, err
	}
	var item *Element
	for _, c := range x.Content {
		if c.Element == nil {
			continue
		}
		if c.Element.Name == (xml.Name{Space: mucUser, Local: "item"}) {
			if item != nil {
				return Occupant{}, ErrElement
			}
			item = c.Element
		}
		if c.Element.Name == (xml.Name{Space: mucUser, Local: "status"}) {
			for _, a := range c.Element.Attributes {
				if a.Name == (xml.Name{Local: "code"}) && (a.Value == "110" || a.Value == "173" || a.Value == "174") {
					return Occupant{}, ErrElement
				}
			}
		}
	}
	if item == nil {
		return Occupant{}, ErrElement
	}
	fields := map[string]string{}
	for _, a := range item.Attributes {
		if a.Name.Space == "" {
			fields[a.Name.Local] = a.Value
		}
	}
	result := Occupant{Address: attrs["from"], RealJID: fields["jid"], Departed: attrs["type"] == "unavailable"}
	if result.RealJID != "" {
		if _, _, ok := FullJID(result.RealJID); !ok {
			return Occupant{}, ErrElement
		}
	}
	if !result.Departed && (result.RealJID == "" || (fields["role"] != "participant" && fields["role"] != "moderator" && fields["role"] != "visitor")) {
		return Occupant{}, ErrElement
	}
	return result, nil
}
