package xmppwire

import (
	"encoding/base64"
	"encoding/xml"
	"strings"
)

const sasl = "urn:ietf:params:xml:ns:xmpp-sasl"
const binding = "urn:ietf:params:xml:ns:xmpp-bind"

// Header emits a client stream opening for a provisioned account. It does not
// authenticate the account or secure the transport.
func Header(account string) ([]byte, error) {
	if !BareJID(account) {
		return nil, ErrElement
	}
	_, domain, _ := strings.Cut(account, "@")
	return []byte(`<?xml version="1.0" encoding="UTF-8"?><stream:stream xmlns:stream="http://etherx.jabber.org/streams" xmlns="jabber:client" to="` + domain + `" version="1.0">`), nil
}

// Plain requires advertised PLAIN on a stream already verified by the caller's
// TLS owner. The returned credential wire must be cleared after writing.
func Plain(features *Element, account string, password []byte) ([]byte, error) {
	if !BareJID(account) || len(password) < 1 || len(password) > 1024 || !validText(string(password)) || strings.ContainsRune(string(password), '\x00') {
		return nil, ErrElement
	}
	mechanisms, err := child(features, streamName.Space, "features", sasl, "mechanisms")
	if err != nil {
		return nil, err
	}
	found := false
	for _, c := range mechanisms.Content {
		if c.Element == nil {
			if strings.TrimSpace(c.Text) != "" {
				return nil, ErrElement
			}
			continue
		}
		if c.Element.Name != (xml.Name{Space: sasl, Local: "mechanism"}) {
			return nil, ErrElement
		}
		text, err := c.Element.Text()
		if err != nil {
			return nil, err
		}
		if text == "PLAIN" {
			if found {
				return nil, ErrElement
			}
			found = true
		}
	}
	if !found {
		return nil, ErrElement
	}
	local, _, _ := strings.Cut(account, "@")
	data := append([]byte(account+"\x00"+local+"\x00"), password...)
	defer clear(data)
	encoded := make([]byte, base64.StdEncoding.EncodedLen(len(data)))
	defer clear(encoded)
	base64.StdEncoding.Encode(encoded, data)
	// Base64 is ASCII and needs no XML escaping.
	return append(append([]byte(`<auth xmlns="`+sasl+`" mechanism="PLAIN">`), encoded...), []byte(`</auth>`)...), nil
}

func Success(e *Element) bool {
	if e == nil || e.Name != (xml.Name{Space: sasl, Local: "success"}) || len(e.Attributes) != 0 {
		return false
	}
	text, err := e.Text()
	return err == nil && text == ""
}

func Bind(features *Element, account, resource, id string) ([]byte, error) {
	if _, _, ok := FullJID(account + "/" + resource); !ok || !correlation(id) {
		return nil, ErrElement
	}
	if _, err := child(features, streamName.Space, "features", binding, "bind"); err != nil {
		return nil, err
	}
	return []byte(`<iq xmlns="jabber:client" type="set" id="` + id + `"><bind xmlns="` + binding + `"><resource>` + resource + `</resource></bind></iq>`), nil
}

// Bound confirms the exact provisioned full account after SASL restart. It is
// wire validation only; the session owner must establish ordered verified TLS.
func Bound(e *Element, account, resource, id string) bool {
	if _, _, ok := FullJID(account + "/" + resource); !ok || !correlation(id) {
		return false
	}
	_, domain, _ := strings.Cut(account, "@")
	attrs := map[string]string{}
	if e == nil {
		return false
	}
	for _, a := range e.Attributes {
		if a.Name.Space != "" {
			return false
		}
		if _, ok := attrs[a.Name.Local]; ok {
			return false
		}
		attrs[a.Name.Local] = a.Value
	}
	if attrs["type"] != "result" || attrs["id"] != id || attrs["from"] != "" && attrs["from"] != domain || attrs["to"] != "" && attrs["to"] != account+"/"+resource {
		return false
	}
	b, err := child(e, "jabber:client", "iq", binding, "bind")
	if err != nil {
		return false
	}
	j, err := child(b, binding, "bind", binding, "jid")
	if err != nil {
		return false
	}
	text, err := j.Text()
	return err == nil && text == account+"/"+resource
}

func correlation(s string) bool {
	if len(s) < 1 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

func child(e *Element, ns, name, childNS, childName string) (*Element, error) {
	if e == nil || e.Name != (xml.Name{Space: ns, Local: name}) {
		return nil, ErrElement
	}
	var result *Element
	for _, c := range e.Content {
		if c.Element == nil {
			if strings.TrimSpace(c.Text) != "" {
				return nil, ErrElement
			}
			continue
		}
		if c.Element.Name == (xml.Name{Space: childNS, Local: childName}) {
			if result != nil {
				return nil, ErrElement
			}
			result = c.Element
		}
	}
	if result == nil {
		return nil, ErrElement
	}
	return result, nil
}
