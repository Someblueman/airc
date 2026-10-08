package xmppwire

import (
	"bufio"
	"encoding/xml"
	"io"
	"regexp"
)

var streamName = xml.Name{Space: "http://etherx.jabber.org/streams", Local: "stream"}

var declaration = regexp.MustCompile(`^version[ \t\r\n]*=[ \t\r\n]*(?:"1\.0"|'1\.0')(?:[ \t\r\n]+encoding[ \t\r\n]*=[ \t\r\n]*(?:"(?i:utf-8)"|'(?i:utf-8)'))?[ \t\r\n]*$`)

// Stream owns only framing. Its caller owns deadlines, authentication, stream
// negotiation and the underlying connection. Any refusal is terminal.
type Stream struct {
	input   *budgetInput
	decoder *xml.Decoder
	opened  bool
	failed  bool
}

type budgetInput struct {
	reader    *bufio.Reader
	remaining int
}

func (b *budgetInput) ReadByte() (byte, error) {
	if b.remaining == 0 {
		return 0, ErrElement
	}
	b.remaining--
	return b.reader.ReadByte()
}

func (b *budgetInput) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	v, err := b.ReadByte()
	if err != nil {
		return 0, err
	}
	p[0] = v
	return 1, nil
}

func NewStream(r io.Reader) *Stream {
	if r == nil {
		return &Stream{failed: true}
	}
	b := &budgetInput{reader: bufio.NewReaderSize(r, 4096)}
	return &Stream{input: b, decoder: xml.NewDecoder(b)}
}

// Open reads an unclosed stream root, optionally preceded by an XML 1.0 UTF-8
// declaration. Other processing instructions and standalone flags are refused.
func (s *Stream) Open() (*Element, error) {
	if s == nil || s.failed || s.opened {
		return nil, ErrElement
	}
	s.input.remaining = MaxElement
	t, err := s.decoder.Token()
	if pi, ok := t.(xml.ProcInst); err == nil && ok {
		if pi.Target != "xml" || !declaration.Match(pi.Inst) {
			return s.refuse()
		}
		t, err = s.decoder.Token()
	}
	start, ok := t.(xml.StartElement)
	if err != nil || !ok || start.Name != streamName {
		return s.refuse()
	}
	b := elementBuilder{}
	if err := b.add(start); err != nil {
		return s.refuse()
	}
	s.opened = true
	return b.root, nil
}

// Read returns one complete child with inherited namespace bindings, or EOF
// for an explicit stream close. A truncated connection is a framing refusal.
func (s *Stream) Read() (*Element, error) {
	if s == nil || s.failed || !s.opened {
		return nil, ErrElement
	}
	s.input.remaining = MaxElement
	b := elementBuilder{}
	for count := 0; count < MaxTokens; count++ {
		t, err := s.decoder.Token()
		if err != nil {
			return s.refuse()
		}
		if end, ok := t.(xml.EndElement); ok && b.root == nil && end.Name == streamName {
			s.failed = true
			return nil, io.EOF
		}
		if err := b.add(t); err != nil {
			return s.refuse()
		}
		if b.root != nil && len(b.stack) == 0 {
			return b.root, nil
		}
	}
	return s.refuse()
}

// Restart is permitted only after the negotiation owner has validated the
// preceding TLS/SASL transition. Buffered bytes survive; old namespaces do not.
// A TLS upgrade must instead create a new Stream on the secured connection.
func (s *Stream) Restart() error {
	if s == nil || s.failed || !s.opened {
		return ErrElement
	}
	s.decoder = xml.NewDecoder(s.input)
	s.opened = false
	return nil
}

func (s *Stream) refuse() (*Element, error) {
	s.failed = true
	return nil, ErrElement
}
