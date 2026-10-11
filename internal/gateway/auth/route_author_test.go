package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/protocol"
)

const routeAuthorToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func routeAuthorTokenFile(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "account.token")
	if err := os.WriteFile(path, []byte(routeAuthorToken+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadBoundAuthorAndGuestNick(t *testing.T) {
	root := privateTemp(t)
	guest, err := LoadBoundAuthor(config.BoundAuthor{Nick: "alerts"})
	if err != nil || !guest.Valid() || guest.Author != (core.Author{Nick: "alerts"}) {
		t.Fatal(guest, err)
	}
	registered, err := LoadBoundAuthor(config.BoundAuthor{Nick: "alerts", AccountTokenFile: routeAuthorTokenFile(t, root)})
	if err != nil || !registered.Valid() || registered.Author != (core.Author{Nick: "alerts", Registered: true}) || registered.Credential.Token() != routeAuthorToken {
		t.Fatal(registered, err)
	}
	for _, b := range []config.BoundAuthor{{Nick: ""}, {Nick: "9lives"}, {Nick: "alerts", AccountTokenFile: filepath.Join(root, "missing")}} {
		if _, err := LoadBoundAuthor(b); !errors.Is(err, ErrCredential) {
			t.Fatal("invalid bound author loaded", b, err)
		}
	}
	// Simple provider IDs keep their name; anything lossy gets a hash suffix,
	// so distinct GitHub logins or devices never share a guest nickname.
	for id, want := range map[string]string{"octocat": "gh-octocat", "a-b": "gh-a-b"} {
		if got := GuestNick("gh-", id, id); got != want || !protocol.ValidNick(got) {
			t.Fatal(id, got)
		}
	}
	for _, id := range []string{"dot.name", strings.Repeat("x", 40)} {
		got := GuestNick("gh-", id, id)
		if !protocol.ValidNick(got) || len(got) > 30 || !strings.HasPrefix(got, "gh-") || !strings.Contains(got, "|") {
			t.Fatal(id, got)
		}
	}
	if GuestNick("gh-", strings.Repeat("x", 40), strings.Repeat("x", 40)) == GuestNick("gh-", strings.Repeat("x", 41), strings.Repeat("x", 41)) {
		t.Fatal("long logins share a guest nickname")
	}
	if got := GuestNick("lora-", "70b3d57ed0000001", "70b3d57ed0000001"); got != "lora-70b3d57ed0000001" || !protocol.ValidNick(got) {
		t.Fatal(got)
	}
}

func githubTestWebhook(t *testing.T, guests bool, accounts ...config.GitHubAccount) (*GitHubWebhook, []byte) {
	t.Helper()
	root := privateTemp(t)
	secret := []byte(strings.Repeat("s", 32))
	path := filepath.Join(root, "secret")
	if err := os.WriteFile(path, secret, 0600); err != nil {
		t.Fatal(err)
	}
	options := config.GitHubWebhook{SecretFile: path, FreshnessSeconds: 300, Routes: []config.GitHubWebhookRoute{{RepositoryID: 7, Target: core.Target{Kind: "room", Name: "#repo"}}}, Accounts: accounts, Guests: &guests}
	w, err := NewGitHubWebhook(config.Defaults(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	return w, secret
}

func githubTestComment(secret []byte, repository, user int64, login string, created time.Time) (string, []byte) {
	stamp := created.UTC().Format(time.RFC3339Nano)
	payload := []byte(fmt.Sprintf(`{"action":"created","repository":{"id":%d},"issue":{"number":3},"comment":{"id":99,"user":{"id":%d,"login":%q},"created_at":%q,"updated_at":%q,"body":"looks good"},"sender":{"id":%d}}`, repository, user, login, stamp, stamp, user))
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil)), payload
}

func TestGitHubWebhookCommenterAuthors(t *testing.T) {
	root := privateTemp(t)
	token := routeAuthorTokenFile(t, root)
	now := time.Now().UTC().Truncate(time.Second)
	w, secret := githubTestWebhook(t, true, config.GitHubAccount{Login: "Octocat", AuthorID: 42, Nick: "octo", AccountTokenFile: token})
	// A mapped login with the matching immutable ID posts as its account.
	signature, payload := githubTestComment(secret, 7, 42, "octocat", now)
	proof, err := w.Verify("issue_comment", signature, payload, now)
	if err != nil || proof.Author().Author != (core.Author{Nick: "octo", Registered: true}) || proof.Target().Name != "#repo" || proof.Body() != "looks good" {
		t.Fatal(proof.Author(), err)
	}
	// A renamed-and-reused login (another ID) and any other commenter are
	// guests "gh-<login>".
	for _, c := range []struct {
		id    int64
		login string
	}{{43, "octocat"}, {50, "hubot"}} {
		signature, payload = githubTestComment(secret, 7, c.id, c.login, now)
		proof, err = w.Verify("issue_comment", signature, payload, now)
		if err != nil || proof.Author().Author != (core.Author{Nick: "gh-" + c.login}) {
			t.Fatal(c, proof.Author(), err)
		}
	}
	// An unrouted repository is refused.
	signature, payload = githubTestComment(secret, 8, 50, "hubot", now)
	if _, err := w.Verify("issue_comment", signature, payload, now); !errors.Is(err, ErrAuthentication) {
		t.Fatal("unrouted repository", err)
	}
	// Without guests only mapped commenters post.
	closed, secret := githubTestWebhook(t, false, config.GitHubAccount{Login: "octocat", Nick: "octo"})
	signature, payload = githubTestComment(secret, 7, 50, "hubot", now)
	if _, err := closed.Verify("issue_comment", signature, payload, now); !errors.Is(err, ErrForbidden) {
		t.Fatal("unmapped commenter without guests", err)
	}
	signature, payload = githubTestComment(secret, 7, 51, "octocat", now)
	if proof, err := closed.Verify("issue_comment", signature, payload, now); err != nil || proof.Author().Author != (core.Author{Nick: "octo"}) {
		t.Fatal("mapped login without author_id", err)
	}
}

func chirpStackTestProvider(t *testing.T, guests bool, accounts ...config.ChirpStackAccount) (*ChirpStack, string) {
	t.Helper()
	root := privateTemp(t)
	secret := strings.Repeat("b", 32)
	path := filepath.Join(root, "secret")
	if err := os.WriteFile(path, []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	options := config.ChirpStack{SecretFile: path, ServerID: "11111111-1111-1111-1111-111111111111", FreshnessSeconds: 300, Guests: &guests, Accounts: accounts, Routes: []config.ChirpStackRoute{
		{TenantID: "22222222-2222-2222-2222-222222222222", ApplicationID: "33333333-3333-3333-3333-333333333333", DevEUI: "70b3d57ed0000001", FPort: 1, Target: core.Target{Kind: "room", Name: "#lora"}},
		{TenantID: "22222222-2222-2222-2222-222222222222", ApplicationID: "33333333-3333-3333-3333-333333333333", DevEUI: "70b3d57ed0000002", FPort: 1, Target: core.Target{Kind: "room", Name: "#lora"}},
	}}
	c, err := NewChirpStack(config.Defaults(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c, "Bearer " + secret
}

func chirpStackTestUplink(device string, now time.Time) []byte {
	return []byte(fmt.Sprintf(`{"deduplicationId":"44444444-4444-4444-4444-44444444444%s","time":%q,"fCnt":1,"fPort":1,"data":%q,"deviceInfo":{"tenantId":"22222222-2222-2222-2222-222222222222","applicationId":"33333333-3333-3333-3333-333333333333","devEui":%q}}`, device[len(device)-1:], now.UTC().Format(time.RFC3339Nano), base64.StdEncoding.EncodeToString([]byte("door open")), device))
}

func TestChirpStackDeviceAuthors(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	c, authorSpec := chirpStackTestProvider(t, true, config.ChirpStackAccount{DevEUI: "70b3d57ed0000001", Nick: "front-door"})
	proof, err := c.Verify(authorSpec, chirpStackTestUplink("70b3d57ed0000001", now), now)
	if err != nil || proof.Author().Author != (core.Author{Nick: "front-door"}) || proof.Body() != "door open" {
		t.Fatal(proof.Author(), err)
	}
	proof, err = c.Verify(authorSpec, chirpStackTestUplink("70b3d57ed0000002", now), now)
	if err != nil || proof.Author().Author != (core.Author{Nick: "lora-70b3d57ed0000002"}) {
		t.Fatal(proof.Author(), err)
	}
	closed, authorSpec := chirpStackTestProvider(t, false, config.ChirpStackAccount{DevEUI: "70b3d57ed0000001", Nick: "front-door"})
	if _, err := closed.Verify(authorSpec, chirpStackTestUplink("70b3d57ed0000002", now), now); !errors.Is(err, ErrForbidden) {
		t.Fatal("unmapped device without guests", err)
	}
	if _, err := c.Verify("Bearer "+strings.Repeat("x", 32), chirpStackTestUplink("70b3d57ed0000001", now), now); !errors.Is(err, ErrAuthentication) {
		t.Fatal("wrong authorSpec", err)
	}
}
