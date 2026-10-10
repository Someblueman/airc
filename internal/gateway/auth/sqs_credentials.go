package auth

import (
	"bytes"
	"encoding/json"

	"github.com/Someblueman/airc/internal/gateway/jsoninput"
	"github.com/Someblueman/airc/internal/gateway/sqswire"
)

const maxSQSCredentialsBytes = 8192

// ReadSQSCredentials reads the three explicit SQS credentials from a private
// JSON file. It does not consult environment variables, metadata services, or
// any other ambient credential source. The caller owns and must clear the
// returned byte slices after the client has cloned them.
func ReadSQSCredentials(path string) (sqswire.Credentials, error) {
	data, err := keyFile(path, true)
	if err != nil || len(data) == 0 || len(data) > maxSQSCredentialsBytes || jsoninput.ValidateDepth(data, maxSQSCredentialsBytes, 4) != nil {
		clear(data)
		return sqswire.Credentials{}, ErrCredential
	}
	defer clear(data)
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil || len(raw) < 2 || len(raw) > 3 {
		return sqswire.Credentials{}, ErrCredential
	}
	for key := range raw {
		if key != "access_key_id" && key != "secret_access_key" && key != "session_token" {
			return sqswire.Credentials{}, ErrCredential
		}
	}
	var creds sqswire.Credentials
	if decodeCredentialString(raw["access_key_id"], &creds.AccessKeyID) != nil ||
		decodeCredentialBytes(raw["secret_access_key"], &creds.SecretAccessKey) != nil {
		clear(creds.SecretAccessKey)
		return sqswire.Credentials{}, ErrCredential
	}
	if token, ok := raw["session_token"]; ok {
		if decodeCredentialBytes(token, &creds.SessionToken) != nil || len(creds.SessionToken) == 0 {
			clear(creds.SecretAccessKey)
			clear(creds.SessionToken)
			return sqswire.Credentials{}, ErrCredential
		}
	}
	if !validSQSAccessKey(creds.AccessKeyID) || !validSQSSecret(creds.SecretAccessKey) || !validSQSToken(creds.SessionToken) {
		clear(creds.SecretAccessKey)
		clear(creds.SessionToken)
		return sqswire.Credentials{}, ErrCredential
	}
	return creds, nil
}

func decodeCredentialString(raw json.RawMessage, dst *string) error {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, dst) != nil {
		return ErrCredential
	}
	return nil
}

func decodeCredentialBytes(raw json.RawMessage, dst *[]byte) error {
	var value string
	if err := decodeCredentialString(raw, &value); err != nil {
		return err
	}
	*dst = []byte(value)
	return nil
}

func validSQSAccessKey(value string) bool {
	if len(value) < 16 || len(value) > 128 {
		return false
	}
	for _, b := range value {
		if !(b >= 'A' && b <= 'Z' || b >= '0' && b <= '9') {
			return false
		}
	}
	return true
}

func validSQSSecret(value []byte) bool {
	if len(value) < 32 || len(value) > 128 {
		return false
	}
	return validCredentialBytes(value)
}

func validSQSToken(value []byte) bool {
	return len(value) <= 4096 && (len(value) == 0 || validCredentialBytes(value))
}

func validCredentialBytes(value []byte) bool {
	for _, b := range value {
		if b < 0x21 || b > 0x7e {
			return false
		}
	}
	return true
}
