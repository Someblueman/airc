package auth

import (
	"encoding/json"
	"sort"
	"strconv"

	"github.com/Someblueman/airc/internal/gateway/jsoninput"
	"github.com/Someblueman/airc/internal/gateway/matrixwire"
)

const maxMatrixSafeInteger int64 = 1<<53 - 1

type matrixRecoveryMetadata struct {
	author string
	origin int64
}

func parseMatrixRecoveryMetadata(data []byte, room, id string) (matrixRecoveryMetadata, error) {
	if jsoninput.Validate(data, 65536) != nil {
		return matrixRecoveryMetadata{}, ErrAuthentication
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return matrixRecoveryMetadata{}, ErrAuthentication
	}
	typeName, typeOK := matrixJSONString(fields["type"])
	returnedRoom, roomOK := matrixJSONString(fields["room_id"])
	returnedID, idOK := matrixJSONString(fields["event_id"])
	author, authorOK := matrixJSONString(fields["sender"])
	origin, originOK := matrixInteger(fields["origin_server_ts"])
	if !typeOK || typeName != "m.room.message" || !roomOK || returnedRoom != room || !idOK || returnedID != id ||
		!authorOK || !matrixwire.UserID(author) || !originOK {
		return matrixRecoveryMetadata{}, ErrAuthentication
	}
	return matrixRecoveryMetadata{author: author, origin: origin}, nil
}

func matrixJSONString(raw json.RawMessage) (string, bool) {
	var value string
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil || value == "" {
		return "", false
	}
	return value, true
}

func matrixInteger(raw json.RawMessage) (int64, bool) {
	value := string(raw)
	if len(value) == 0 || len(value) > 16 || value[0] == '0' && len(value) > 1 {
		return 0, false
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 || parsed > maxMatrixSafeInteger || strconv.FormatInt(parsed, 10) != value {
		return 0, false
	}
	return parsed, true
}

func (s *MatrixServer) hasRoom(room string) bool {
	if s == nil {
		return false
	}
	for pair := range s.grants {
		if pair[1] == room {
			return true
		}
	}
	return false
}

// RecoveryIdentity changes when the authenticated source or any fixed sender,
// room, principal, binding, target or operation grant changes.
func (s *MatrixServer) RecoveryIdentity() string {
	if s == nil || s.network == "" || s.address == "" || s.tlsServerName == "" || s.account == "" || s.realm == "" || s.freshness <= 0 {
		return ""
	}
	routes := make([]string, 0, len(s.grants))
	for key, grant := range s.grants {
		if grant.identity.ID() == "" || grant.identity.Binding() == "" {
			return ""
		}
		operations := make([]string, len(grant.operations))
		for i, op := range grant.operations {
			operations[i] = string(op)
		}
		sort.Strings(operations)
		parts := []string{"airc-matrix-recovery-grant-v1", key[0], key[1], grant.identity.ID(), grant.identity.Binding(), grant.target.Kind, grant.target.Name}
		parts = append(parts, operations...)
		routes = append(routes, ircDigest(parts...))
	}
	sort.Strings(routes)
	parts := []string{"airc-matrix-recovery-v2", s.network, s.address, s.tlsServerName, s.account, s.realm, strconv.FormatInt(int64(s.freshness), 10)}
	parts = append(parts, routes...)
	return ircDigest(parts...)
}

// Rooms returns the bridge's provisioned room set in stable lexical order.
func (s *MatrixServer) Rooms() []string {
	if s == nil {
		return nil
	}
	rooms := make([]string, 0, len(s.grants))
	for pair := range s.grants {
		rooms = append(rooms, pair[1])
	}
	sort.Strings(rooms)
	if len(rooms) < 2 {
		return rooms
	}
	unique := rooms[:1]
	for _, room := range rooms[1:] {
		if room != unique[len(unique)-1] {
			unique = append(unique, room)
		}
	}
	return unique
}
