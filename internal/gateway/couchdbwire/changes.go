package couchdbwire

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/Someblueman/airc/internal/gateway/jsoninput"
)

const maxTokenBytes = 16 << 10

var namespacePattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type Page struct {
	LastSequence string
	Changes      []Change
}

type Change struct {
	ID        string
	Revision  string
	Namespace string
	Token     string
	Control   bool
}

func ValidNamespace(s string) bool { return namespacePattern.MatchString(s) }

func ValidSequence(s string) bool {
	// This profile uses numeric positions or CouchDB 3 numeric-prefix
	// cluster tokens. In particular, never pass the special live-only
	// position "now" (or JSON/whitespace variants) back to the provider.
	if len(s) == 0 || len(s) > 2048 || s[0] < '0' || s[0] > '9' {
		return false
	}
	for i := range s {
		b := s[i]
		if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b == '-' || b == '_') {
			return false
		}
	}
	return true
}

func parsePage(data []byte) (Page, error) {
	if jsoninput.ValidateDepth(data, maxPageBytes, 16) != nil {
		return Page{}, ErrRequest
	}
	root, err := object(data)
	if err != nil || onlyFields(root, []string{"results", "last_seq", "pending"}) != nil {
		return Page{}, ErrRequest
	}
	last, ok := root["last_seq"]
	if !ok {
		return Page{}, ErrRequest
	}
	lastSeq, err := sequence(last)
	if err != nil {
		return Page{}, ErrRequest
	}
	pending, ok := root["pending"]
	if !ok {
		return Page{}, ErrRequest
	}
	if _, err := nonnegativeInteger(pending); err != nil {
		return Page{}, ErrRequest
	}
	var rows []json.RawMessage
	if raw, ok := root["results"]; !ok || json.Unmarshal(raw, &rows) != nil || rows == nil || len(rows) > 16 {
		return Page{}, ErrRequest
	}
	page := Page{LastSequence: lastSeq, Changes: make([]Change, 0, len(rows))}
	seen := make(map[string]struct{}, len(rows))
	for _, raw := range rows {
		change, err := parseRow(raw)
		if err != nil {
			return Page{}, ErrRequest
		}
		if !change.Control {
			if _, duplicate := seen[change.ID]; duplicate {
				return Page{}, ErrRequest
			}
			seen[change.ID] = struct{}{}
		}
		page.Changes = append(page.Changes, change)
	}
	return page, nil
}

func parseRow(raw []byte) (Change, error) {
	row, err := object(raw)
	if err != nil || onlyFields(row, []string{"seq", "id", "changes", "doc"}, []string{"deleted"}) != nil {
		return Change{}, ErrRequest
	}
	seq, ok := row["seq"]
	if !ok {
		return Change{}, ErrRequest
	}
	if _, err := sequence(seq); err != nil {
		return Change{}, ErrRequest
	}
	id, err := stringValue(row["id"])
	if err != nil || len(id) == 0 || len(id) > 128 {
		return Change{}, ErrRequest
	}
	if rawDeleted, exists := row["deleted"]; exists {
		var deleted bool
		if json.Unmarshal(rawDeleted, &deleted) != nil || deleted {
			return Change{}, ErrRequest
		}
	}
	var revisions []json.RawMessage
	if rawChanges, ok := row["changes"]; !ok || json.Unmarshal(rawChanges, &revisions) != nil || len(revisions) != 1 {
		return Change{}, ErrRequest
	}
	revObject, err := object(revisions[0])
	if err != nil || onlyFields(revObject, []string{"rev"}) != nil {
		return Change{}, ErrRequest
	}
	revision, err := stringValue(revObject["rev"])
	if err != nil || !validRevision(revision) {
		return Change{}, ErrRequest
	}
	doc, err := object(row["doc"])
	if err != nil {
		return Change{}, ErrRequest
	}
	if strings.HasPrefix(id, "_design/") {
		if !validDesignID(id) || docString(doc, "_id") != id || docString(doc, "_rev") != revision {
			return Change{}, ErrRequest
		}
		return Change{ID: id, Revision: revision, Control: true}, nil
	}
	if strings.HasPrefix(id, "_local/") || !validRequestID(id) {
		return Change{}, ErrRequest
	}
	if !strings.HasPrefix(revision, "1-") {
		return Change{}, ErrRequest
	}
	if onlyFields(doc, []string{"_id", "_rev", "type", "namespace", "airc_jwe"}) != nil || docString(doc, "_id") != id || docString(doc, "_rev") != revision || docString(doc, "type") != "airc_request_v1" {
		return Change{}, ErrRequest
	}
	namespace := docString(doc, "namespace")
	if !ValidNamespace(namespace) {
		return Change{}, ErrRequest
	}
	token := docString(doc, "airc_jwe")
	if !validCompactJWE(token) || requestID(token) != id {
		return Change{}, ErrRequest
	}
	return Change{ID: id, Revision: revision, Namespace: namespace, Token: token}, nil
}

func object(data []byte) (map[string]json.RawMessage, error) {
	var value map[string]json.RawMessage
	if json.Unmarshal(data, &value) != nil || value == nil {
		return nil, ErrRequest
	}
	return value, nil
}

func onlyFields(value map[string]json.RawMessage, required []string, optional ...[]string) error {
	allowed := make(map[string]bool, len(required)+len(optional))
	for _, name := range required {
		allowed[name] = true
		if raw, ok := value[name]; !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return ErrRequest
		}
	}
	for _, group := range optional {
		for _, name := range group {
			allowed[name] = true
			if raw, ok := value[name]; ok && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				return ErrRequest
			}
		}
	}
	for name := range value {
		if !allowed[name] {
			return ErrRequest
		}
	}
	return nil
}

func stringValue(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	var s string
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &s) != nil {
		return "", ErrRequest
	}
	return s, nil
}

func docString(doc map[string]json.RawMessage, name string) string {
	s, _ := stringValue(doc[name])
	return s
}

func sequence(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", ErrRequest
	}
	if raw[0] == '"' {
		s, err := stringValue(raw)
		if err != nil || !ValidSequence(s) {
			return "", ErrRequest
		}
		return s, nil
	}
	n, err := nonnegativeInteger(raw)
	if err != nil {
		return "", err
	}
	return n, nil
}

func nonnegativeInteger(raw json.RawMessage) (string, error) {
	s := string(bytes.TrimSpace(raw))
	if s == "" || len(s) > 19 || (len(s) > 1 && s[0] == '0') {
		return "", ErrRequest
	}
	for i := range s {
		if s[i] < '0' || s[i] > '9' {
			return "", ErrRequest
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return "", ErrRequest
	}
	return strconv.FormatInt(n, 10), nil
}

func validRevision(s string) bool {
	separator := strings.IndexByte(s, '-')
	if separator < 1 || separator > 10 || len(s)-separator-1 != 32 || s[0] == '0' {
		return false
	}
	for i := 0; i < separator; i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	for i := separator + 1; i < len(s); i++ {
		if !(s[i] >= '0' && s[i] <= '9' || s[i] >= 'a' && s[i] <= 'f') {
			return false
		}
	}
	return true
}

func validRequestID(id string) bool {
	if len(id) != 69 || !strings.HasPrefix(id, "airc-") {
		return false
	}
	for i := 5; i < len(id); i++ {
		if !(id[i] >= '0' && id[i] <= '9' || id[i] >= 'a' && id[i] <= 'f') {
			return false
		}
	}
	return true
}

func requestID(token string) string {
	h := sha256.Sum256([]byte(token))
	return "airc-" + hex.EncodeToString(h[:])
}

func validCompactJWE(token string) bool {
	if len(token) == 0 || len(token) > maxTokenBytes {
		return false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 5 {
		return false
	}
	if parts[0] == "" || parts[2] == "" || parts[3] == "" || parts[4] == "" {
		return false
	}
	for _, part := range parts {
		for i := range part {
			b := part[i]
			if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_') {
				return false
			}
		}
	}
	return true
}

func validDesignID(id string) bool {
	if !strings.HasPrefix(id, "_design/") || len(id) > 128 || len(id) == len("_design/") {
		return false
	}
	for i := len("_design/"); i < len(id); i++ {
		b := id[i]
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.ContainsRune("_-.", rune(b))) {
			return false
		}
	}
	return true
}
