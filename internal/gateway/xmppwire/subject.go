package xmppwire

import (
	"encoding/xml"
	"strings"
)

const xmppDelay = "urn:xmpp:delay"
const stanzaID = "urn:xmpp:sid:0"

// RoomSubject recognizes only the bounded subject notice sent by a joined MUC
// room. Its text is informational and must never be treated as an author event.
func RoomSubject(e *Element, recipient string) (string, bool) {
	if e == nil || e.Name != (xml.Name{Space: "jabber:client", Local: "message"}) {
		return "", false
	}
	if _, _, ok := FullJID(recipient); !ok {
		return "", false
	}
	attrs := make(map[string]string, len(e.Attributes))
	for _, a := range e.Attributes {
		xmlLang := xml.Name{Space: "http://www.w3.org/XML/1998/namespace", Local: "lang"}
		if a.Name.Space != "" && a.Name != xmlLang {
			return "", false
		}
		if a.Name.Space == "" && a.Name.Local != "from" && a.Name.Local != "type" && a.Name.Local != "to" && a.Name.Local != "id" {
			return "", false
		}
		if a.Name == xmlLang && !languageTag(a.Value) {
			return "", false
		}
		attrs[a.Name.Local] = a.Value
	}
	to, hasTo := attrs["to"]
	if attrs["type"] != "groupchat" || hasTo && to != recipient {
		return "", false
	}
	room := attrs["from"]
	if !BareJID(room) {
		var ok bool
		room, _, ok = FullJID(room)
		if !ok {
			return "", false
		}
	}
	if id := attrs["id"]; id != "" && (len(id) > 256 || !validText(id) || strings.ContainsAny(id, "\x00\r\n")) {
		return "", false
	}

	subjects, delays, ids := 0, 0, 0
	for _, content := range e.Content {
		if content.Element == nil {
			if strings.TrimSpace(content.Text) != "" {
				return "", false
			}
			continue
		}
		child := content.Element
		switch child.Name {
		case xml.Name{Space: "jabber:client", Local: "subject"}:
			subjects++
			if subjects != 1 || !subjectLanguage(child) {
				return "", false
			}
			if _, err := child.Text(); err != nil {
				return "", false
			}
		case xml.Name{Space: xmppDelay, Local: "delay"}:
			delays++
			if delays != 1 || !emptyMetadata(child, map[string]bool{"from": true, "stamp": true}) {
				return "", false
			}
		case xml.Name{Space: stanzaID, Local: "stanza-id"}:
			ids++
			if ids != 1 || !roomStanzaID(child, room) {
				return "", false
			}
		default:
			// In particular, a body or thread makes this a message, not a
			// subject-only control stanza.
			return "", false
		}
	}
	return room, subjects == 1
}

func subjectLanguage(e *Element) bool {
	for _, a := range e.Attributes {
		if a.Name != (xml.Name{Space: "http://www.w3.org/XML/1998/namespace", Local: "lang"}) || !languageTag(a.Value) {
			return false
		}
	}
	return true
}

func languageTag(s string) bool {
	if len(s) == 0 || len(s) > 35 {
		return false
	}
	parts := strings.Split(s, "-")
	for i, part := range parts {
		if len(part) == 0 || len(part) > 8 {
			return false
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9') {
				return false
			}
		}
	}
	return true
}

func emptyMetadata(e *Element, allowed map[string]bool) bool {
	for _, a := range e.Attributes {
		if a.Name.Space != "" || !allowed[a.Name.Local] || a.Value == "" {
			return false
		}
	}
	for _, c := range e.Content {
		if c.Element != nil || strings.TrimSpace(c.Text) != "" {
			return false
		}
	}
	return true
}

func roomStanzaID(e *Element, room string) bool {
	fields := map[string]string{}
	for _, a := range e.Attributes {
		if a.Name.Space != "" || a.Name.Local != "by" && a.Name.Local != "id" {
			return false
		}
		fields[a.Name.Local] = a.Value
	}
	return fields["by"] == room && len(fields["id"]) > 0 && len(fields["id"]) <= 256 && validText(fields["id"]) && !strings.ContainsAny(fields["id"], "\x00\r\n") && len(e.Content) == 0
}
