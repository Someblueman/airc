package ircwire

import (
	"encoding/hex"
	"strconv"
	"strings"
)

const ResponseMarker = "AIRC-RESULT/1"
const ResponseChunkBytes = 3000
const MaxResponseTokenBytes = 64 << 10
const MaxResponseChunks = (MaxResponseTokenBytes + ResponseChunkBytes - 1) / ResponseChunkBytes

// ResponseLines fragments one complete protected token, never native plaintext.
// The hash ID is gateway correlation, not the unbounded provider msgid.
func ResponseLines(nickname, requestID, token string) ([][]byte, error) {
	if !responseNickname(nickname) || !responseRequestID(requestID) || !responseToken(token) {
		return nil, ErrLine
	}
	count := (len(token) + ResponseChunkBytes - 1) / ResponseChunkBytes
	lines := make([][]byte, 0, count)
	for index := range count {
		start := index * ResponseChunkBytes
		part := token[start:min(start+ResponseChunkBytes, len(token))]
		value := requestID + "/" + strconv.Itoa(index) + "/" + strconv.Itoa(count) + "/" + part
		line, err := clientTagLine("+airc/result", value, "NOTICE", nickname, ResponseMarker)
		if err != nil {
			return nil, err
		}
		lines = append(lines, line)
	}
	return lines, nil
}

// JoinResponseFragments refuses missing, repeated, mixed or oversized chunks.
// Callers bound collection to MaxResponseChunks and the response lifetime.
// The returned token must still be decrypted and its signature, scope, payload
// request ID and lifetime verified before exposing any native result.
func JoinResponseFragments(tags []string) (requestID, token string, err error) {
	if len(tags) < 1 || len(tags) > MaxResponseChunks {
		return "", "", ErrLine
	}
	parts := make([]string, len(tags))
	total := 0
	for _, tag := range tags {
		if len(tag) > ResponseChunkBytes+80 {
			return "", "", ErrLine
		}
		fields := strings.Split(tag, "/")
		if len(fields) != 4 || !responseRequestID(fields[0]) {
			return "", "", ErrLine
		}
		index, e1 := strconv.Atoi(fields[1])
		count, e2 := strconv.Atoi(fields[2])
		if e1 != nil || e2 != nil || count != len(tags) || index < 0 || index >= count || strconv.Itoa(index) != fields[1] || strconv.Itoa(count) != fields[2] || parts[index] != "" {
			return "", "", ErrLine
		}
		if requestID == "" {
			requestID = fields[0]
		}
		part := fields[3]
		if requestID != fields[0] || len(part) == 0 || len(part) > ResponseChunkBytes || index < count-1 && len(part) != ResponseChunkBytes {
			return "", "", ErrLine
		}
		total += len(part)
		if total > MaxResponseTokenBytes {
			return "", "", ErrLine
		}
		parts[index] = part
	}
	token = strings.Join(parts, "")
	if !responseToken(token) {
		return "", "", ErrLine
	}
	return requestID, token, nil
}

func responseRequestID(id string) bool {
	if len(id) != 64 || strings.ToLower(id) != id {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func responseToken(token string) bool {
	if len(token) == 0 || len(token) > MaxResponseTokenBytes || strings.Count(token, ".") != 4 {
		return false
	}
	for _, c := range token {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-_.", c)) {
			return false
		}
	}
	return true
}

func responseNickname(nick string) bool {
	if len(nick) == 0 || len(nick) > 30 || nick[0] >= '0' && nick[0] <= '9' || nick[0] == '-' {
		return false
	}
	for _, c := range nick {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-[]\\`^{}|_", c)) {
			return false
		}
	}
	return true
}
