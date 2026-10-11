package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const submissionToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestDecodeSubmissionSplitsSecretsFromIntent(t *testing.T) {
	body := []byte(`{"version":1,"request_id":"r1","operation":"send","target":{"kind":"room","name":"#ops"},"message":"hi","author":{"nick":"Alice","token":"` + submissionToken + `"},"password":"letmein"}`)
	sub, err := DecodeSubmission(body)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Author == nil || sub.Author.Nick != "Alice" || !sub.Author.Registered || sub.Credential.Token() != submissionToken {
		t.Fatalf("author %+v", sub.Author)
	}
	if !sub.Password.Matches(sha256.Sum256([]byte("letmein"))) || sub.Password.Matches(sha256.Sum256([]byte("other"))) {
		t.Fatal("password not carried")
	}
	encoded, err := json.Marshal(sub.Request)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := sub.Request.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	formatted := fmt.Sprintf("%v %+v %#v %s", sub, sub, sub, sub.Password)
	for _, leak := range [][]byte{encoded, []byte(fingerprint), []byte(formatted)} {
		if bytes.Contains(leak, []byte(submissionToken)) || bytes.Contains(leak, []byte("letmein")) || bytes.Contains(leak, []byte("author")) {
			t.Fatalf("secret or author leaked: %s", leak)
		}
	}
	// The same intent without secrets has the same fingerprint: the token
	// is not part of what a resubmission must match.
	plain, err := Decode([]byte(`{"version":1,"request_id":"r1","operation":"send","target":{"kind":"room","name":"#ops"},"message":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if other, _ := plain.Fingerprint(); other != fingerprint {
		t.Fatal("fingerprint depends on author secrets")
	}
	if _, err := json.Marshal(sub.Password); err == nil {
		t.Fatal("password marshalled")
	}
}

func TestDecodeSubmissionAuthorForms(t *testing.T) {
	guest, err := DecodeSubmission([]byte(`{"version":1,"request_id":"g","operation":"receipt.get","lookup_id":"x","author":{"nick":"bob"}}`))
	if err != nil || guest.Author == nil || guest.Author.Registered || !guest.Credential.Empty() || guest.Author.Scope() != "g:bob" {
		t.Fatalf("guest: %+v %v", guest.Author, err)
	}
	none, err := DecodeSubmission([]byte(`{"version":1,"request_id":"n","operation":"capabilities.get"}`))
	if err != nil || none.Author != nil || !none.Password.Empty() {
		t.Fatalf("no author: %v", err)
	}
	for _, body := range []string{
		`{"version":1,"request_id":"x","operation":"send","target":{"kind":"room","name":"#a"},"message":"m","author":{"nick":"bad nick"}}`,
		`{"version":1,"request_id":"x","operation":"send","target":{"kind":"room","name":"#a"},"message":"m","author":{"nick":"ok","token":"short"}}`,
		`{"version":1,"request_id":"x","operation":"send","target":{"kind":"room","name":"#a"},"message":"m","author":{"nick":"ok","extra":1}}`,
		`{"version":1,"request_id":"x","operation":"send","target":{"kind":"room","name":"#a"},"message":"m","author":{"token":"` + submissionToken + `"}}`,
		`{"version":1,"request_id":"x","operation":"send","target":{"kind":"room","name":"#a"},"message":"m","author":null}`,
		`{"version":1,"request_id":"x","operation":"send","target":{"kind":"room","name":"#a"},"message":"m","password":""}`,
		`{"version":1,"request_id":"x","operation":"send","target":{"kind":"room","name":"#a"},"message":"m","password":"a\nb"}`,
		`{"version":1,"request_id":"x","operation":"send","target":{"kind":"room","name":"#a"},"message":"m","author":{"nick":"a"},"author":{"nick":"b"}}`,
		`{"version":1,"request_id":"x","operation":"send","target":{"kind":"room","name":"#a"},"message":"m","unknown":1}`,
		`{"version":1,"request_id":"x","operation":"account.register","target":{"kind":"room","name":"#a"}}`,
	} {
		if _, err := DecodeSubmission([]byte(body)); err == nil {
			t.Errorf("accepted %s", body)
		} else if strings.Contains(err.Error(), submissionToken) {
			t.Errorf("error leaks token: %v", err)
		}
	}
	// The principal decoder never accepts an author.
	if _, err := Decode([]byte(`{"version":1,"request_id":"x","operation":"capabilities.get","author":{"nick":"a"}}`)); err == nil {
		t.Fatal("Decode accepted an author")
	}
	for _, op := range []Operation{AccountRegister, AccountIdentify} {
		r := Request{Version: 1, RequestID: "a", Operation: op}
		if err := r.Normalize(); err != nil {
			t.Fatalf("%s: %v", op, err)
		}
	}
}
