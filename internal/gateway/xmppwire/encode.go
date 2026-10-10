package xmppwire

import (
	"bytes"
	"encoding/xml"
	"unicode/utf8"
)

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > MaxElement-b.Len() {
		return 0, ErrElement
	}
	return b.Buffer.Write(p)
}

// Encode emits a complete standalone element or refuses it. The output buffer
// never exceeds MaxElement; cyclic input is refused by the depth bound.
func Encode(e *Element) ([]byte, error) {
	var b boundedOutput
	x := xml.NewEncoder(&b)
	tokens := 0
	if err := encode(x, e, 1, &tokens); err != nil {
		return nil, ErrElement
	}
	if err := x.Flush(); err != nil {
		return nil, ErrElement
	}
	if _, err := Parse(b.Bytes()); err != nil {
		return nil, ErrElement
	}
	return b.Bytes(), nil
}

func encode(x *xml.Encoder, e *Element, depth int, tokens *int) error {
	if e == nil || depth > MaxDepth || !qualified(e.Name) || len(e.Attributes) > MaxAttributes {
		return ErrElement
	}
	for _, a := range e.Attributes {
		if len(a.Name.Local) > MaxElement || len(a.Name.Space) > MaxElement || !validText(a.Value) {
			return ErrElement
		}
	}
	start := xml.StartElement{Name: e.Name, Attr: e.Attributes}
	if err := emit(x, start, tokens); err != nil {
		return err
	}
	for _, c := range e.Content {
		if c.Element != nil {
			if c.Text != "" {
				return ErrElement
			}
			if err := encode(x, c.Element, depth+1, tokens); err != nil {
				return err
			}
		} else {
			if !validText(c.Text) {
				return ErrElement
			}
			if err := emit(x, xml.CharData(c.Text), tokens); err != nil {
				return err
			}
		}
	}
	return emit(x, start.End(), tokens)
}

func validText(s string) bool {
	if len(s) > MaxElement || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r != '\t' && r != '\n' && r != '\r' && !(r >= 0x20 && r <= 0xd7ff) &&
			!(r >= 0xe000 && r <= 0xfffd) && !(r >= 0x10000 && r <= 0x10ffff) {
			return false
		}
	}
	return true
}

func emit(x *xml.Encoder, t xml.Token, count *int) error {
	if *count >= MaxTokens {
		return ErrElement
	}
	*count++
	return x.EncodeToken(t)
}
