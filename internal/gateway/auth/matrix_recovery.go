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
	_, ok := s.routes[room]
	return ok
}

// RecoveryIdentity changes when the authenticated source or any room, target,
// operation or author mapping changes.
func (s *MatrixServer) RecoveryIdentity() string {
	if s == nil || s.network == "" || s.address == "" || s.tlsServerName == "" || s.account == "" || s.freshness <= 0 || s.authors == nil {
		return ""
	}
	routes := make([]string, 0, len(s.routes))
	for room, route := range s.routes {
		operations := make([]string, len(route.operations))
		for i, op := range route.operations {
			operations[i] = string(op)
		}
		sort.Strings(operations)
		parts := []string{"airc-matrix-recovery-route-v1", room, route.target.Kind, route.target.Name}
		parts = append(parts, operations...)
		routes = append(routes, ircDigest(parts...))
	}
	sort.Strings(routes)
	parts := []string{"airc-matrix-recovery-v3", s.network, s.address, s.tlsServerName, s.account, strconv.FormatInt(int64(s.freshness), 10), s.authors.Fingerprint()}
	parts = append(parts, routes...)
	return ircDigest(parts...)
}

// Rooms returns the bridge's provisioned room set in stable lexical order.
func (s *MatrixServer) Rooms() []string {
	if s == nil {
		return nil
	}
	rooms := make([]string, 0, len(s.routes))
	for room := range s.routes {
		rooms = append(rooms, room)
	}
	sort.Strings(rooms)
	return rooms
}
