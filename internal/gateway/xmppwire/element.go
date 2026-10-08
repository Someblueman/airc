// Package xmppwire provides bounded XML elements for the XMPP bridge. Elements
// carry data only; parsing does not authenticate their sender or provenance.
package xmppwire

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

const MaxElement = 64 << 10
const MaxDepth = 16
const MaxTokens = 2048
const MaxAttributes = 32

var ErrElement = errors.New("invalid or oversized XMPP XML element")

// Element preserves mixed content in order, including significant whitespace.
// Names use expanded namespaces; lexical prefixes are not identity.
type Element struct {
	Name       xml.Name
	Attributes []xml.Attr
	Content    []Content
}

type Content struct {
	Text    string
	Element *Element
}

// Parse accepts one standalone, namespace-qualified element. A stream owner
// must supply inherited namespace declarations when framing stream children.
// DTDs, processing instructions and comments are outside this strict profile.
func Parse(raw []byte) (*Element, error) {
	if len(raw) == 0 || len(raw) > MaxElement || !utf8.Valid(raw) {
		return nil, ErrElement
	}
	d := xml.NewDecoder(bytes.NewReader(raw))
	b := elementBuilder{}
	for count := 0; ; count++ {
		t, err := d.Token()
		if err == io.EOF {
			if b.root != nil && len(b.stack) == 0 {
				return b.root, nil
			}
			return nil, ErrElement
		}
		if err != nil || count >= MaxTokens {
			return nil, ErrElement
		}
		if err := b.add(t); err != nil {
			return nil, err
		}
	}
}

func qualified(n xml.Name) bool {
	return n.Local != "" && len(n.Local) <= MaxElement && len(n.Space) <= MaxElement &&
		utf8.ValidString(n.Local) && utf8.ValidString(n.Space) && strings.Contains(n.Space, ":")
}

// Text returns a whole simple text element, refusing nested markup.
func (e *Element) Text() (string, error) {
	if e == nil {
		return "", ErrElement
	}
	var b strings.Builder
	for _, c := range e.Content {
		if c.Element != nil {
			return "", ErrElement
		}
		b.WriteString(c.Text)
	}
	return b.String(), nil
}
