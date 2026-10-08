package xmppwire

import "encoding/xml"

const muc = "http://jabber.org/protocol/muc"
const mucUser = "http://jabber.org/protocol/muc#user"

// Join requests a provisioned room under the exact configured resource and
// disables historical stanzas. The server must independently confirm entry.
func Join(room, resource string) ([]byte, error) {
	if _, _, ok := FullJID(room + "/" + resource); !ok {
		return nil, ErrElement
	}
	return []byte(`<presence xmlns="jabber:client" to="` + room + `/` + resource + `"><x xmlns="` + muc + `"><history maxstanzas="0"/></x></presence>`), nil
}

// OwnJoined checks server-supplied self presence, including explicit room
// non-anonymity. It is wire validation, not independent sender authentication.
// The caller must use its verified session and provisioned room before trusting
// these fields. Real-JID mapping for other occupants is separate work.
func OwnJoined(e *Element, room, account, resource string) bool {
	if _, _, ok := FullJID(room + "/" + resource); !ok || !BareJID(account) {
		return false
	}
	if e == nil || e.Name != (xml.Name{Space: "jabber:client", Local: "presence"}) {
		return false
	}
	attrs := map[string]string{}
	for _, a := range e.Attributes {
		if a.Name.Space == "" {
			attrs[a.Name.Local] = a.Value
		}
	}
	if attrs["from"] != room+"/"+resource || attrs["type"] != "" || attrs["to"] != "" && attrs["to"] != account+"/"+resource {
		return false
	}
	x, err := child(e, "jabber:client", "presence", mucUser, "x")
	if err != nil {
		return false
	}
	var item *Element
	status := map[string]bool{}
	for _, c := range x.Content {
		if c.Element == nil {
			continue
		}
		n := c.Element
		switch n.Name {
		case xml.Name{Space: mucUser, Local: "item"}:
			if item != nil {
				return false
			}
			item = n
		case xml.Name{Space: mucUser, Local: "status"}:
			code := ""
			for _, a := range n.Attributes {
				if a.Name == (xml.Name{Local: "code"}) {
					code = a.Value
				}
			}
			if code == "" || status[code] {
				return false
			}
			status[code] = true
		}
	}
	if item == nil || !status["100"] || !status["110"] || status["201"] || status["210"] {
		return false
	}
	fields := map[string]string{}
	for _, a := range item.Attributes {
		if a.Name.Space == "" {
			fields[a.Name.Local] = a.Value
		}
	}
	if fields["jid"] != "" && fields["jid"] != account+"/"+resource {
		return false
	}
	return (fields["role"] == "participant" || fields["role"] == "moderator") &&
		(fields["affiliation"] == "none" || fields["affiliation"] == "member" || fields["affiliation"] == "admin" || fields["affiliation"] == "owner")
}
