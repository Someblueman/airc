package xmppwire

import "encoding/xml"

const discoInfo = "http://jabber.org/protocol/disco#info"

func Discover(room, id string) ([]byte, error) {
	if !BareJID(room) || !correlation(id) {
		return nil, ErrElement
	}
	return []byte(`<iq xmlns="jabber:client" type="get" to="` + room + `" id="` + id + `"><query xmlns="` + discoInfo + `"/></iq>`), nil
}

// Discovered validates the exact room response and selected features. These
// assertions are trusted only within the authenticated caller's ordered query.
func Discovered(e *Element, room, recipient, id string) bool {
	if !BareJID(room) || !correlation(id) {
		return false
	}
	if _, _, ok := FullJID(recipient); !ok || e == nil {
		return false
	}
	attrs := map[string]string{}
	for _, a := range e.Attributes {
		if a.Name.Space == "" {
			attrs[a.Name.Local] = a.Value
		}
	}
	if attrs["type"] != "result" || attrs["id"] != id || attrs["from"] != room || attrs["to"] != "" && attrs["to"] != recipient {
		return false
	}
	q, err := child(e, "jabber:client", "iq", discoInfo, "query")
	if err != nil || len(q.Attributes) != 0 {
		return false
	}
	features := map[string]bool{}
	identity := false
	for _, c := range q.Content {
		if c.Element == nil {
			continue
		}
		n := c.Element
		fields := map[string]string{}
		for _, a := range n.Attributes {
			if a.Name.Space == "" {
				fields[a.Name.Local] = a.Value
			}
		}
		switch n.Name {
		case xml.Name{Space: discoInfo, Local: "feature"}:
			if len(n.Content) != 0 {
				return false
			}
			v := fields["var"]
			if v == "" || features[v] {
				return false
			}
			features[v] = true
		case xml.Name{Space: discoInfo, Local: "identity"}:
			if fields["category"] == "conference" && fields["type"] == "text" {
				identity = true
			}
		}
	}
	return identity && features[muc] && features["muc_nonanonymous"] && !features["muc_semianonymous"] && !features["muc_anonymous"] && features["urn:xmpp:sid:0"]
}
