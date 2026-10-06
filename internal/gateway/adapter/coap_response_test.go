package adapter

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
)

func TestCoAPResponseProtectionRejectsAmbiguousOrUnsupportedRecipients(t *testing.T) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := auth.NewResponseSealer("response", private)
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wrongCurve, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	route := CoAPReturnKey{ID: "route", Principal: "principal", Binding: "binding", RecipientID: "reader", Recipient: &recipient.PublicKey}
	for name, routes := range map[string][]CoAPReturnKey{
		"valid":       {route},
		"missing":     {},
		"ambiguous":   {route, route},
		"no_key":      {{ID: "route", Principal: "principal", Binding: "binding", RecipientID: "reader"}},
		"wrong_curve": {{ID: "route", Principal: "principal", Binding: "binding", RecipientID: "reader", Recipient: &wrongCurve.PublicKey}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewCoAPResponseProtection(sealer, "realm", routes)
			if (name == "valid") != (err == nil) {
				t.Fatal("invalid response authority", err)
			}
		})
	}
}
