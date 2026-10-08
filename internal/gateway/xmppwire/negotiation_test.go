package xmppwire

import (
	"encoding/base64"
	"strings"
	"testing"
)

func parsed(t *testing.T, s string) *Element {
	t.Helper()
	e, err := Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

const authFeatures = `<s:features xmlns:s="http://etherx.jabber.org/streams"><mechanisms xmlns="urn:ietf:params:xml:ns:xmpp-sasl"><mechanism>PLAIN</mechanism></mechanisms></s:features>`
const bindFeatures = `<s:features xmlns:s="http://etherx.jabber.org/streams"><bind xmlns="urn:ietf:params:xml:ns:xmpp-bind"/></s:features>`

func TestXMPPWireNegotiation(t *testing.T) {
	h, err := Header("bridge@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewStream(strings.NewReader(string(h))).Open(); err != nil {
		t.Fatal(err)
	}
	w, err := Plain(parsed(t, authFeatures), "bridge@example.com", []byte("private-secret"))
	if err != nil {
		t.Fatal(err)
	}
	defer clear(w)
	a := parsed(t, string(w))
	body, err := a.Text()
	if err != nil {
		t.Fatal(err)
	}
	data, err := base64.StdEncoding.DecodeString(body)
	defer clear(data)
	if err != nil || string(data) != "bridge@example.com\x00bridge\x00private-secret" {
		t.Fatal("incorrect SASL encoding")
	}
	if !Success(parsed(t, `<success xmlns="urn:ietf:params:xml:ns:xmpp-sasl"/>`)) {
		t.Fatal("success refused")
	}
	w, err = Bind(parsed(t, bindFeatures), "bridge@example.com", "Resource", "bind-1")
	if err != nil {
		t.Fatal(err)
	}
	if e := parsed(t, string(w)); e.Name.Local != "iq" {
		t.Fatal("incorrect bind request")
	}
	response := `<iq xmlns="jabber:client" type="result" id="bind-1"><bind xmlns="urn:ietf:params:xml:ns:xmpp-bind"><jid>bridge@example.com/Resource</jid></bind></iq>`
	if !Bound(parsed(t, response), "bridge@example.com", "Resource", "bind-1") {
		t.Fatal("bound account refused")
	}
	for _, r := range []string{
		strings.Replace(response, "result", "error", 1), strings.Replace(response, "bind-1", "bind-2", 1),
		strings.Replace(response, "bridge@example.com/Resource", "other@example.com/Resource", 1),
		strings.Replace(response, "bridge@example.com/Resource", "bridge@example.com/resource", 1),
		strings.Replace(response, `type="result"`, `from="other.example.com" type="result"`, 1),
		strings.Replace(response, "</bind>", "<jid>bridge@example.com/Resource</jid></bind>", 1),
	} {
		if Bound(parsed(t, r), "bridge@example.com", "Resource", "bind-1") {
			t.Fatal("invalid binding accepted")
		}
	}
}

func TestXMPPWireNegotiationRefusals(t *testing.T) {
	for _, f := range []string{strings.Replace(authFeatures, "PLAIN", "SCRAM-SHA-256", 1), strings.ReplaceAll(authFeatures, sasl, "urn:fake"), strings.Replace(authFeatures, "</mechanisms>", "<mechanism>PLAIN</mechanism></mechanisms>", 1)} {
		if w, err := Plain(parsed(t, f), "bridge@example.com", []byte("secret")); w != nil || err == nil {
			t.Fatal("unadvertised/ambiguous PLAIN accepted")
		}
	}
	for _, pw := range [][]byte{nil, []byte("bad\x00secret"), []byte(strings.Repeat("x", 1025)), {0xff}} {
		if w, err := Plain(parsed(t, authFeatures), "bridge@example.com", pw); w != nil || err == nil {
			t.Fatal("invalid password accepted")
		}
	}
	for _, s := range []string{`<challenge xmlns="urn:ietf:params:xml:ns:xmpp-sasl"/>`, `<failure xmlns="urn:ietf:params:xml:ns:xmpp-sasl"/>`, `<success xmlns="urn:fake"/>`, `<success xmlns="urn:ietf:params:xml:ns:xmpp-sasl">extra</success>`} {
		if Success(parsed(t, s)) {
			t.Fatal("false SASL success")
		}
	}
	if _, err := Bind(parsed(t, bindFeatures), "bridge@example.com", "resource", "bad\"id"); err == nil {
		t.Fatal("ID injection accepted")
	}
}
