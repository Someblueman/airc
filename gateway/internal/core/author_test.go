package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestAuthorScopeNormalization(t *testing.T) {
	for _, c := range []struct {
		author Author
		scope  string
	}{
		{Author{Nick: "Alice"}, "g:alice"},
		{Author{Nick: "alice"}, "g:alice"},
		{Author{Nick: "ALICE", Registered: true}, "a:alice"},
		{Author{Nick: "Bot-7[x]"}, "g:bot-7[x]"},
		{Author{Nick: strings.Repeat("n", 30), Registered: true}, "a:" + strings.Repeat("n", 30)},
	} {
		if got := c.author.Scope(); got != c.scope || !ValidScope(got) {
			t.Fatalf("%+v scope %q, want %q", c.author, got, c.scope)
		}
	}
	// AIRC folds nicknames with strings.ToLower (aircd nickKey), so the guest
	// scope must equal its "guest:<nick>" retry owner, never per listener.
	if (Author{Nick: "Alice"}).Scope() != (Author{Nick: "aLiCe"}).Scope() {
		t.Fatal("guest scope is case sensitive")
	}
	if (Author{Nick: "alice"}).Scope() == (Author{Nick: "alice", Registered: true}).Scope() {
		t.Fatal("guest and registered authors share a scope")
	}
	for _, nick := range []string{"", "1alice", "-alice", "al ice", "al:ice", "alice!", strings.Repeat("n", 31), "élan"} {
		if (Author{Nick: nick}).Valid() || (Author{Nick: nick}).Scope() != "" {
			t.Fatalf("accepted invalid nickname %q", nick)
		}
	}
	for _, scope := range []string{"", "g:", "a:", "x:alice", "g:Alice", "galice", "g:1a", "g:alice ", "a:" + strings.Repeat("n", 31)} {
		if ValidScope(scope) {
			t.Fatalf("accepted invalid scope %q", scope)
		}
	}
}

func TestCredentialStaysInMemory(t *testing.T) {
	if _, err := NewCredential("short"); err == nil {
		t.Fatal("malformed token accepted")
	}
	if _, err := NewCredential(testToken + "\n"); err == nil {
		t.Fatal("token with trailing newline accepted")
	}
	c, err := NewCredential(testToken)
	if err != nil || c.Empty() || c.Token() != testToken {
		t.Fatal("valid token refused", err)
	}
	if _, err := json.Marshal(c); err == nil {
		t.Fatal("credential marshalled")
	}
	if _, err := json.Marshal(struct{ C Credential }{c}); err == nil {
		t.Fatal("embedded credential marshalled")
	}
	var decoded Credential
	if err := json.Unmarshal([]byte(`"`+testToken+`"`), &decoded); err == nil || !decoded.Empty() {
		t.Fatal("credential decoded from JSON")
	}
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
		if out := fmt.Sprintf(format, c); strings.Contains(out, testToken) || strings.Contains(out, testToken[:16]) {
			t.Fatalf("%s leaked the token: %s", format, out)
		}
		if out := fmt.Sprintf(format, struct{ C Credential }{c}); strings.Contains(out, testToken) {
			t.Fatalf("%s leaked an embedded token: %s", format, out)
		}
	}
	// Request is the only shape that reaches JSON, fingerprints and the journal.
	credential := reflect.TypeFor[Credential]()
	request := reflect.TypeFor[Request]()
	for i := range request.NumField() {
		if field := request.Field(i); field.Type == credential || strings.Contains(strings.ToLower(field.Name), "token") {
			t.Fatal("request carries a credential field", field.Name)
		}
	}
}

func TestTokenCheckVector(t *testing.T) {
	c, _ := NewCredential(testToken)
	sum := sha256.Sum256([]byte("airc-gateway-account-v1\x00alice" + testToken))
	want := hex.EncodeToString(sum[:])
	if TokenCheck("Alice", testToken) != want || c.Check("ALICE") != want {
		t.Fatal("token check digest changed")
	}
	if strings.Contains(want, testToken) || !c.MatchesCheck("alice", want) {
		t.Fatal("check does not match its own token")
	}
	other, _ := NewCredential(strings.Repeat("f", 64))
	if other.MatchesCheck("alice", want) || c.MatchesCheck("bob", want) || (Credential{}).MatchesCheck("alice", want) || c.MatchesCheck("alice", "") {
		t.Fatal("check matched a different token, nick or no token")
	}
}
