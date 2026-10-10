package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"slices"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

const MaxChirpStackBytes = 64 << 10

type chirpStackRouteKey struct {
	tenant, application, device string
	port                        uint32
}

type chirpStackGrant struct {
	identity Identity
	target   core.Target
}

// ChirpStack authenticates a trusted network server and maps its verified
// device uplinks to fixed native grants. Device metadata alone is not proof.
type ChirpStack struct {
	verifier           *Verifier
	serverID           string
	freshness          time.Duration
	credentialHash     [sha256.Size]byte
	authorizationBytes int
	grants             map[chirpStackRouteKey]chirpStackGrant
	mu                 sync.RWMutex
}

func NewChirpStack(v *Verifier, c config.Config, options config.ChirpStack) (*ChirpStack, error) {
	if v == nil || c.ValidateChirpStack(options) != nil {
		return nil, ErrCredential
	}
	grants := make(map[chirpStackRouteKey]chirpStackGrant, len(options.Routes))
	for _, route := range options.Routes {
		var identity Identity
		for i := range v.principals {
			candidate := Identity{&v.principals[i]}
			if candidate.ID() == route.Principal && v.OwnsIdentity(candidate) &&
				slices.Contains(candidate.principal.operations, core.Send) && slices.Contains(candidate.principal.targets, route.Target) {
				identity = candidate
				break
			}
		}
		if identity.ID() == "" {
			return nil, ErrCredential
		}
		key := chirpStackRouteKey{route.TenantID, route.ApplicationID, route.DevEUI, route.FPort}
		grants[key] = chirpStackGrant{identity: identity, target: route.Target}
	}
	secret, err := keyFile(options.SecretFile, true)
	defer clear(secret)
	if err != nil || len(secret) < 32 || len(secret) > 128 {
		return nil, ErrCredential
	}
	for _, b := range secret {
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_') {
			return nil, ErrCredential
		}
	}
	header := append([]byte("Bearer "), secret...)
	defer clear(header)
	return &ChirpStack{verifier: v, serverID: options.ServerID,
		freshness:      time.Duration(options.FreshnessSeconds) * time.Second,
		credentialHash: sha256.Sum256(header), authorizationBytes: len(header), grants: grants}, nil
}

func (c *ChirpStack) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	clear(c.credentialHash[:])
	c.authorizationBytes = 0
	c.mu.Unlock()
}

type VerifiedChirpStackUplink struct {
	provider       *ChirpStack
	identity       Identity
	target         core.Target
	body           string
	occurred       time.Time
	replayDeadline time.Time
	requestID      string
	fingerprint    string
}

func (e VerifiedChirpStackUplink) Valid() bool {
	return e.provider != nil && e.identity.ID() != "" && e.requestID != "" && e.fingerprint != ""
}
func (e VerifiedChirpStackUplink) Identity() Identity        { return e.identity }
func (e VerifiedChirpStackUplink) Target() core.Target       { return e.target }
func (e VerifiedChirpStackUplink) Body() string              { return e.body }
func (e VerifiedChirpStackUplink) Occurred() time.Time       { return e.occurred }
func (e VerifiedChirpStackUplink) ReplayDeadline() time.Time { return e.replayDeadline }
func (e VerifiedChirpStackUplink) RequestID() string         { return e.requestID }
func (e VerifiedChirpStackUplink) Fingerprint() string       { return e.fingerprint }

func (c *ChirpStack) Verify(authorization string, payload []byte, now time.Time) (VerifiedChirpStackUplink, error) {
	if c == nil || len(payload) == 0 || len(payload) > MaxChirpStackBytes {
		return VerifiedChirpStackUplink{}, ErrAuthentication
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.authorizationBytes == 0 || len(authorization) != c.authorizationBytes {
		return VerifiedChirpStackUplink{}, ErrAuthentication
	}
	provided := sha256.Sum256([]byte(authorization))
	if subtle.ConstantTimeCompare(c.credentialHash[:], provided[:]) != 1 {
		return VerifiedChirpStackUplink{}, ErrAuthentication
	}
	event, err := parseChirpStackUplink(payload)
	if err != nil || !config.ValidChirpStackUUID(event.DeduplicationID) || event.Confirmed || event.FPort < 1 || event.FPort > 223 {
		return VerifiedChirpStackUplink{}, ErrAuthentication
	}
	created, err := time.Parse(time.RFC3339Nano, event.Time)
	if err != nil || created.Unix() < 0 || now.Before(created) || !now.Before(created.Add(c.freshness)) {
		return VerifiedChirpStackUplink{}, ErrAuthentication
	}
	body, err := base64.StdEncoding.Strict().DecodeString(event.Data)
	if err != nil || len(body) < 1 || len(body) > 242 || !utf8.Valid(body) || base64.StdEncoding.EncodeToString(body) != event.Data {
		return VerifiedChirpStackUplink{}, ErrAuthentication
	}
	key := chirpStackRouteKey{event.DeviceInfo.TenantID, event.DeviceInfo.ApplicationID, event.DeviceInfo.DevEUI, event.FPort}
	grant, ok := c.grants[key]
	if !ok || !c.verifier.OwnsIdentity(grant.identity) || grant.identity.principal.revoked ||
		!slices.Contains(grant.identity.principal.operations, core.Send) || !slices.Contains(grant.identity.principal.targets, grant.target) {
		return VerifiedChirpStackUplink{}, ErrAuthentication
	}
	requestID := chirpStackDigest("airc-chirpstack-uplink-v1", c.serverID, event.DeduplicationID)
	request := core.Request{Version: 1, RequestID: requestID, Operation: core.Send, Target: &grant.target, Message: string(body)}
	if request.Normalize() != nil || request.Message != string(body) || *request.Target != grant.target {
		return VerifiedChirpStackUplink{}, ErrAuthentication
	}
	fingerprint := chirpStackDigest(c.serverID, event.DeduplicationID, key.tenant, key.application, key.device,
		strconv.FormatUint(uint64(key.port), 10), strconv.FormatUint(uint64(event.FCnt), 10), created.UTC().Format(time.RFC3339Nano),
		string(body), grant.identity.ID(), grant.identity.Binding(), grant.target.Kind, grant.target.Name)
	return VerifiedChirpStackUplink{provider: c, identity: grant.identity, target: grant.target, body: string(body),
		occurred: created, replayDeadline: created.Add(c.freshness), requestID: requestID, fingerprint: fingerprint}, nil
}

func chirpStackDigest(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		_, _ = h.Write(length[:])
		_, _ = h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}
