package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Someblueman/airc/internal/gateway/jsoninput"
)

// OSCOREOwner labels a security context: the gateway realm and the
// coap_oscore listener it belongs to. OSCORE is transport security; it says
// nothing about which AIRC author a request posts as.
type OSCOREOwner struct {
	Realm    string `json:"realm"`
	Listener string `json:"listener"`
}

type oscoreContextFile struct {
	Version      int         `json:"version"`
	Owner        OSCOREOwner `json:"owner"`
	MasterSecret string      `json:"master_secret"`
	MasterSalt   string      `json:"master_salt"`
	IDContext    string      `json:"id_context"`
}

// OSCOREContext is owner-private provisioning material, never a request body.
// The server and peer use fixed, distinct IDs 01 and 02 with a fresh secret.
type OSCOREContext struct{ record oscoreContextFile }

// CreateOSCOREContext generates fresh material and exclusively writes a synced
// owner-only file. It never imports keys, overwrites a file, or initializes the
// sequence journal. The caller must provision that journal before using keys.
func CreateOSCOREContext(path string, owner OSCOREOwner) (*OSCOREContext, error) {
	c := &OSCOREContext{record: oscoreContextFile{Version: 1, Owner: owner}}
	if !validOSCOREOwner(owner) {
		return nil, ErrCredential
	}
	for _, field := range []struct {
		target *string
		bytes  int
	}{{&c.record.MasterSecret, 32}, {&c.record.MasterSalt, 16}, {&c.record.IDContext, 16}} {
		material := make([]byte, field.bytes)
		if _, err := rand.Read(material); err != nil {
			clear(material)
			return nil, err
		}
		*field.target = hex.EncodeToString(material)
		clear(material)
	}
	data, err := json.Marshal(c.record)
	if err != nil {
		return nil, ErrCredential
	}
	defer clear(data)
	if err := createPrivate(path, data); err != nil {
		return nil, err
	}
	return c, nil
}

// ReadOSCOREContext performs a strict bounded read through the existing native
// private credential-file checks. Loading material never creates missing state.
func ReadOSCOREContext(path string) (*OSCOREContext, error) {
	f, err := openPrivate(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return nil, ErrCredential
	}
	defer clear(data)
	c := &OSCOREContext{}
	if jsoninput.Decode(data, &c.record, 4096) != nil || !c.valid() {
		return nil, ErrCredential
	}
	return c, nil
}

func (c *OSCOREContext) Owner() OSCOREOwner { return c.record.Owner }

// NativeConfig returns caller-owned secret bytes for the pinned library.
// The caller must clear the result. It must supply durable sender reservation
// and receiver callbacks when creating the actual native context.
func (c *OSCOREContext) NativeConfig(server bool) ([]byte, error) {
	if !c.valid() {
		return nil, ErrCredential
	}
	sender, recipient := "01", "02"
	if !server {
		sender, recipient = recipient, sender
	}
	return []byte(fmt.Sprintf("master_secret,hex,\"%s\"\nmaster_salt,hex,\"%s\"\nid_context,hex,\"%s\"\nsender_id,hex,\"%s\"\nrecipient_id,hex,\"%s\"\naead_alg,integer,10\nhkdf_alg,integer,-10\nreplay_window,integer,64\nssn_freq,integer,8\nrfc8613_b_1_2,bool,true\nrfc8613_b_2,bool,false\n", c.record.MasterSecret, c.record.MasterSalt, c.record.IDContext, sender, recipient)), nil
}

// Digest excludes owner labels and uses the canonical server-role crypto profile.
// File formatting and the peer's reversed orientation cannot change its identity.
func (c *OSCOREContext) Digest() ([32]byte, error) {
	data, err := c.NativeConfig(true)
	if err != nil {
		return [32]byte{}, err
	}
	defer clear(data)
	return sha256.Sum256(data), nil
}

func (c *OSCOREContext) valid() bool {
	if c == nil || c.record.Version != 1 || !validOSCOREOwner(c.record.Owner) {
		return false
	}
	for _, field := range []struct {
		value string
		bytes int
	}{{c.record.MasterSecret, 32}, {c.record.MasterSalt, 16}, {c.record.IDContext, 16}} {
		if len(field.value) != field.bytes*2 || field.value != strings.ToLower(field.value) {
			return false
		}
		decoded, err := hex.DecodeString(field.value)
		clear(decoded)
		if err != nil {
			return false
		}
	}
	return true
}

func validOSCOREOwner(owner OSCOREOwner) bool {
	return owner.Realm != "" && len(owner.Realm) <= 128 && owner.Listener != "" && len(owner.Listener) <= 128
}
