package auth

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/jsoninput"
)

const (
	// MaxGitHubWebhookBytes bounds signed provider payloads before JSON parsing.
	MaxGitHubWebhookBytes  = 256 << 10
	githubWebhookBodyLimit = 4096
)

type githubAccount struct {
	authorID int64
	author   RouteAuthor
}

// GitHubWebhook snapshots repository routes and the account map and owns its
// HMAC secret. Close rejects new verifications, joins active Verify calls,
// then clears the secret. HTTP owners must join handlers before calling Close.
type GitHubWebhook struct {
	freshness    time.Duration
	secret       []byte
	repositories map[int64]core.Target
	accounts     map[string]githubAccount
	guests       bool
	mu           sync.RWMutex
}

func NewGitHubWebhook(c config.Config, options config.GitHubWebhook) (*GitHubWebhook, error) {
	if c.ValidateGitHubWebhook(options) != nil {
		return nil, ErrCredential
	}
	w := &GitHubWebhook{
		freshness:    time.Duration(options.FreshnessSeconds) * time.Second,
		repositories: make(map[int64]core.Target, len(options.Routes)),
		accounts:     make(map[string]githubAccount, len(options.Accounts)),
		guests:       options.GitHubGuests(),
	}
	for _, route := range options.Routes {
		target := route.Target
		if target.Normalize() != nil {
			return nil, ErrCredential
		}
		w.repositories[route.RepositoryID] = target
	}
	for _, account := range options.Accounts {
		author, err := LoadBoundAuthor(account.BoundAuthor)
		if err != nil {
			return nil, err
		}
		w.accounts[strings.ToLower(account.Login)] = githubAccount{authorID: account.AuthorID, author: author}
	}
	secret, err := readGitHubWebhookSecret(options.SecretFile)
	if err != nil {
		return nil, err
	}
	w.secret = secret
	return w, nil
}

// author maps a verified commenter to its AIRC author: a configured account,
// else the guest "gh-<login>".
func (w *GitHubWebhook) author(login string, id int64) (RouteAuthor, error) {
	if account, ok := w.accounts[strings.ToLower(login)]; ok && (account.authorID == 0 || account.authorID == id) {
		return account.author, nil
	}
	if !w.guests || !config.ValidGitHubLogin(login) {
		return RouteAuthor{}, ErrForbidden
	}
	return RouteAuthor{Author: core.Author{Nick: GuestNick("gh-", login)}}, nil
}

func readGitHubWebhookSecret(path string) ([]byte, error) {
	f, err := openPrivate(path)
	if err != nil {
		return nil, ErrCredential
	}
	secret, readErr := io.ReadAll(io.LimitReader(f, 1025))
	closeErr := f.Close()
	if readErr != nil || closeErr != nil || len(secret) < 32 || len(secret) > 1024 {
		clear(secret)
		return nil, ErrCredential
	}
	for _, b := range secret {
		if b < 0x20 || b > 0x7e {
			clear(secret)
			return nil, ErrCredential
		}
	}
	return secret, nil
}

func (w *GitHubWebhook) Close() {
	if w == nil {
		return
	}
	w.mu.Lock()
	clear(w.secret)
	w.secret = nil
	w.mu.Unlock()
}

// VerifyPing authenticates GitHub's installation setup ping without creating
// an AIRC proof. It uses the same bounded raw-body signature contract as events.
func (w *GitHubWebhook) VerifyPing(signature string, payload []byte) error {
	if w == nil {
		return ErrAuthentication
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	if len(w.secret) == 0 {
		return ErrAuthentication
	}
	secret := w.secret
	if len(payload) == 0 || len(payload) > MaxGitHubWebhookBytes || !githubSignatureMatches(secret, signature, payload) ||
		jsoninput.ValidateDepth(payload, MaxGitHubWebhookBytes, 16) != nil {
		return ErrAuthentication
	}
	ping, err := githubSelectedObject(payload, "hook_id", "zen")
	if err != nil {
		return ErrAuthentication
	}
	if _, err := githubPositiveID(ping, "hook_id"); err != nil {
		return ErrAuthentication
	}
	zen, err := githubString(ping, "zen")
	if err != nil || len(zen) == 0 || len(zen) > githubWebhookBodyLimit || strings.TrimSpace(zen) == "" {
		return ErrAuthentication
	}
	return nil
}

type VerifiedGitHubComment struct {
	webhook        *GitHubWebhook
	author         RouteAuthor
	target         core.Target
	body           string
	occurred       time.Time
	replayDeadline time.Time
	requestID      string
	fingerprint    string
}

func (e VerifiedGitHubComment) Valid() bool {
	return e.webhook != nil && e.author.Valid() && e.requestID != "" && e.fingerprint != ""
}

func (e VerifiedGitHubComment) Author() RouteAuthor       { return e.author }
func (e VerifiedGitHubComment) Target() core.Target       { return e.target }
func (e VerifiedGitHubComment) Body() string              { return e.body }
func (e VerifiedGitHubComment) Occurred() time.Time       { return e.occurred }
func (e VerifiedGitHubComment) ReplayDeadline() time.Time { return e.replayDeadline }
func (e VerifiedGitHubComment) RequestID() string         { return e.requestID }
func (e VerifiedGitHubComment) Fingerprint() string       { return e.fingerprint }

func (w *GitHubWebhook) Verify(event, signature string, payload []byte, now time.Time) (VerifiedGitHubComment, error) {
	if w == nil {
		return VerifiedGitHubComment{}, ErrAuthentication
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	if len(w.secret) == 0 {
		return VerifiedGitHubComment{}, ErrAuthentication
	}
	secret := w.secret
	if event != "issue_comment" || len(payload) == 0 || len(payload) > MaxGitHubWebhookBytes || !validGitHubSignature(signature) {
		return VerifiedGitHubComment{}, ErrAuthentication
	}
	if !githubSignatureMatches(secret, signature, payload) {
		return VerifiedGitHubComment{}, ErrAuthentication
	}
	if jsoninput.ValidateDepth(payload, MaxGitHubWebhookBytes, 16) != nil {
		return VerifiedGitHubComment{}, ErrAuthentication
	}

	data, err := parseGitHubComment(payload)
	if err != nil || data.action != "created" || data.authorID != data.senderID ||
		!utf8.ValidString(data.body) || len(data.body) == 0 || len(data.body) > githubWebhookBodyLimit || strings.TrimSpace(data.body) == "" {
		return VerifiedGitHubComment{}, ErrAuthentication
	}
	created, err := time.Parse(time.RFC3339Nano, data.createdAt)
	if err != nil || created.Unix() < 0 || !strings.HasSuffix(data.createdAt, "Z") ||
		created.Format(time.RFC3339Nano) != data.createdAt || data.updatedAt != data.createdAt || now.Before(created) {
		return VerifiedGitHubComment{}, ErrAuthentication
	}
	deadline := created.Add(w.freshness)
	if !now.Before(deadline) {
		return VerifiedGitHubComment{}, ErrAuthentication
	}
	target, ok := w.repositories[data.repositoryID]
	if !ok {
		return VerifiedGitHubComment{}, ErrAuthentication
	}
	author, err := w.author(data.login, data.authorID)
	if err != nil {
		return VerifiedGitHubComment{}, err
	}
	requestID := githubWebhookDigest("github.com", strconv.FormatInt(data.repositoryID, 10), strconv.FormatInt(data.commentID, 10))
	request := core.Request{Version: 1, RequestID: requestID, Operation: core.Send, Target: &target, Message: data.body}
	if err := request.Normalize(); err != nil || request.Message != data.body || request.Target == nil || *request.Target != target {
		return VerifiedGitHubComment{}, ErrAuthentication
	}
	fingerprint := githubWebhookDigest("airc-github-comment-v2", strconv.FormatInt(data.repositoryID, 10), strconv.FormatInt(data.authorID, 10), strconv.FormatInt(data.commentID, 10), strconv.FormatInt(data.issueNumber, 10), data.createdAt, data.body, author.Author.Scope(), target.Kind, target.Name)
	return VerifiedGitHubComment{
		webhook: w, author: author, target: target, body: data.body,
		occurred: created, replayDeadline: deadline, requestID: requestID, fingerprint: fingerprint,
	}, nil
}

func validGitHubSignature(signature string) bool {
	if len(signature) != len("sha256=")+sha256.Size*2 || !strings.HasPrefix(signature, "sha256=") {
		return false
	}
	for _, b := range signature[len("sha256="):] {
		if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
			return false
		}
	}
	return true
}

func githubSignatureMatches(secret []byte, signature string, payload []byte) bool {
	if !validGitHubSignature(signature) {
		return false
	}
	want, err := hex.DecodeString(signature[len("sha256="):])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(payload)
	return subtle.ConstantTimeCompare(want, mac.Sum(nil)) == 1
}

type githubCommentFields struct {
	action       string
	repositoryID int64
	issueNumber  int64
	commentID    int64
	authorID     int64
	login        string
	senderID     int64
	createdAt    string
	updatedAt    string
	body         string
}

func parseGitHubComment(payload []byte) (githubCommentFields, error) {
	root, err := githubSelectedObject(payload, "action", "repository", "issue", "comment", "sender")
	if err != nil {
		return githubCommentFields{}, err
	}
	var fields githubCommentFields
	if fields.action, err = githubString(root, "action"); err != nil {
		return githubCommentFields{}, err
	}
	repository, err := githubNestedObject(root, "repository", "id")
	if err != nil {
		return githubCommentFields{}, err
	}
	if fields.repositoryID, err = githubPositiveID(repository, "id"); err != nil {
		return githubCommentFields{}, err
	}
	issue, err := githubNestedObject(root, "issue", "number")
	if err != nil {
		return githubCommentFields{}, err
	}
	if fields.issueNumber, err = githubPositiveID(issue, "number"); err != nil {
		return githubCommentFields{}, err
	}
	comment, err := githubNestedObject(root, "comment", "id", "user", "created_at", "updated_at", "body")
	if err != nil {
		return githubCommentFields{}, err
	}
	if fields.commentID, err = githubPositiveID(comment, "id"); err != nil {
		return githubCommentFields{}, err
	}
	user, err := githubNestedObject(comment, "user", "id", "login")
	if err != nil {
		return githubCommentFields{}, err
	}
	if fields.authorID, err = githubPositiveID(user, "id"); err != nil {
		return githubCommentFields{}, err
	}
	if fields.login, err = githubString(user, "login"); err != nil {
		return githubCommentFields{}, err
	}
	if fields.createdAt, err = githubString(comment, "created_at"); err != nil {
		return githubCommentFields{}, err
	}
	if fields.updatedAt, err = githubString(comment, "updated_at"); err != nil {
		return githubCommentFields{}, err
	}
	if fields.body, err = githubString(comment, "body"); err != nil {
		return githubCommentFields{}, err
	}
	sender, err := githubNestedObject(root, "sender", "id")
	if err != nil {
		return githubCommentFields{}, err
	}
	if fields.senderID, err = githubPositiveID(sender, "id"); err != nil {
		return githubCommentFields{}, err
	}
	return fields, nil
}

func githubSelectedObject(raw []byte, selected ...string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, errors.New("expected GitHub webhook object")
	}
	for key := range object {
		for _, expected := range selected {
			if strings.EqualFold(key, expected) && key != expected {
				return nil, errors.New("case-alias GitHub webhook field")
			}
		}
	}
	return object, nil
}

func githubNestedObject(parent map[string]json.RawMessage, name string, selected ...string) (map[string]json.RawMessage, error) {
	raw, ok := parent[name]
	if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, errors.New("missing GitHub webhook object")
	}
	return githubSelectedObject(raw, selected...)
}

func githubField(object map[string]json.RawMessage, name string) (json.RawMessage, error) {
	raw, ok := object[name]
	if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, errors.New("missing or null GitHub webhook field")
	}
	return raw, nil
}

func githubString(object map[string]json.RawMessage, name string) (string, error) {
	raw, err := githubField(object, name)
	if err != nil {
		return "", err
	}
	var value string
	if json.Unmarshal(raw, &value) != nil || !utf8.ValidString(value) {
		return "", errors.New("invalid GitHub webhook string field")
	}
	return value, nil
}

func githubPositiveID(object map[string]json.RawMessage, name string) (int64, error) {
	raw, err := githubField(object, name)
	if err != nil {
		return 0, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return 0, errors.New("invalid GitHub webhook numeric field")
	}
	number, ok := value.(json.Number)
	if !ok {
		return 0, errors.New("GitHub webhook ID must be an integer")
	}
	parsed, err := strconv.ParseInt(number.String(), 10, 64)
	if err != nil || parsed <= 0 || strconv.FormatInt(parsed, 10) != number.String() {
		return 0, errors.New("GitHub webhook ID must be a positive integer")
	}
	return parsed, nil
}

func githubWebhookDigest(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		_, _ = h.Write(length[:])
		_, _ = h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}
