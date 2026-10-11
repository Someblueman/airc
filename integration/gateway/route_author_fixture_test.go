//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// fixtureAuthor is the fixture's registered AIRC account, named by plain
// requests on route-owned and connection carriers: it posts exactly as the
// former binding account did.
const fixtureAuthorNick = "fixture-author"

// authoredRequest marshals r naming the fixture's registered author.
func authoredRequest(t *testing.T, r core.Request) []byte {
	t.Helper()
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	fields["author"] = map[string]string{"nick": fixtureAuthorNick, "token": fixtureAccount}
	if body, err = json.Marshal(fields); err != nil {
		t.Fatal(err)
	}
	return body
}

// fixtureAuthorSubmission looks up the fixture author's jobs.
func fixtureAuthorSubmission(t *testing.T) store.Submission {
	t.Helper()
	credential, err := core.NewCredential(fixtureAccount)
	if err != nil {
		t.Fatal(err)
	}
	return store.Submission{Author: core.Author{Nick: fixtureAuthorNick, Registered: true}, Credential: credential}
}

// fixtureAuthorTokenFile writes the fixture author's token to its own private
// file, as a bound author's account_token_file (credential files may not
// alias the binding's).
func fixtureAuthorTokenFile(t *testing.T, c config.Config, name string) string {
	t.Helper()
	path := filepath.Join(filepath.Dir(c.StateDir), name+".token")
	if err := os.WriteFile(path, []byte(fixtureAccount+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

// authorAwaitAccepted polls an HTTP receipt as the fixture's registered
// author (AIRC-Nick/AIRC-Token) until its job is accepted.
func authorAwaitAccepted(t *testing.T, client *http.Client, address, id string) core.Result {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		request, err := http.NewRequest(http.MethodGet, "https://"+address+"/v1/requests/"+id, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("AIRC-Nick", fixtureAuthorNick)
		request.Header.Set("AIRC-Token", fixtureAccount)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var result core.Result
		err = json.NewDecoder(response.Body).Decode(&result)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK {
			t.Fatal("HTTP receipt failed", response.StatusCode, err)
		}
		if result.State == core.Accepted {
			return result
		}
		if time.Now().After(deadline) {
			t.Fatal("receipt did not reach accepted", result)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
