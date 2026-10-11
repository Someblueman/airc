package config

import (
	"errors"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// OSCORE selects an existing provisioned context and the separately built native
// worker. Configuration never imports or initializes cryptographic state.
type OSCORE struct {
	Binary      string `json:"binary"`
	ContextFile string `json:"context_file"`
	// Author optionally binds a default author to the security context: a
	// request without its own author posts as this one. Without it, each
	// request names its author in its body.
	Author *OSCOREAuthor `json:"author,omitempty"`
}

// OSCOREAuthor is a context's default author: a guest nickname, or a
// registered one with its account token read from an owner-private file.
type OSCOREAuthor struct {
	Nick      string `json:"nick"`
	TokenFile string `json:"token_file,omitempty"`
}

func (Config) validateOSCORE(l Listener) error {
	if l.OSCORE == nil || !absolute(l.OSCORE.Binary) || !absolute(l.OSCORE.ContextFile) {
		return errors.New("OSCORE listener requires an absolute binary and context file")
	}
	if l.TLSCertFile != "" || l.TLSKeyFile != "" || len(l.AllowedOrigins) != 0 {
		return errors.New("OSCORE listener cannot use TLS or origins")
	}
	if l.Enabled && !Available("coap_oscore") {
		return errors.New("native OSCORE listener is unavailable on this platform")
	}
	if a := l.OSCORE.Author; a != nil {
		if _, _, err := core.NewAuthor(a.Nick, ""); err != nil || a.TokenFile != "" && !absolute(a.TokenFile) {
			return errors.New("invalid OSCORE default author")
		}
	}
	return nil
}
