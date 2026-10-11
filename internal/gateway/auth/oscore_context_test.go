//go:build unix

package auth

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFreshOSCOREContextCanonicalScopeAndPeer(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "oscore.json")
	owner := OSCOREOwner{Realm: "realm", Listener: "oscore"}
	context, err := CreateOSCOREContext(path, owner)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := context.Digest()
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := ReadOSCOREContext(path)
	if err != nil {
		t.Fatal(err)
	}
	loadedDigest, err := reloaded.Digest()
	if err != nil || digest != loadedDigest || reloaded.Owner() != owner {
		t.Fatal("context changed on reload", err)
	}
	server, err := reloaded.NativeConfig(true)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(server)
	peer, err := reloaded.NativeConfig(false)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(peer)
	if !bytes.Contains(server, []byte("sender_id,hex,\"01\"")) || !bytes.Contains(peer, []byte("sender_id,hex,\"02\"")) || !bytes.Contains(server, []byte("replay_window,integer,64")) || !bytes.Contains(peer, []byte("rfc8613_b_1_2,bool,true")) {
		t.Fatal("native profile mismatch")
	}
	// Owner labels and harmless JSON formatting cannot reset cryptographic identity.
	changed := reloaded.record
	changed.Owner.Listener = "renamed"
	data, err := json.MarshalIndent(changed, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	otherPath := filepath.Join(root, "reformatted.json")
	if err := os.WriteFile(otherPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	clear(data)
	reformatted, err := ReadOSCOREContext(otherPath)
	if err != nil {
		t.Fatal(err)
	}
	otherDigest, err := reformatted.Digest()
	if err != nil || otherDigest != digest {
		t.Fatal("owner labels changed crypto identity", err)
	}
	if _, err := CreateOSCOREContext(path, owner); err == nil {
		t.Fatal("overwrote existing context")
	}
	fresh, err := CreateOSCOREContext(filepath.Join(root, "fresh.json"), owner)
	if err != nil {
		t.Fatal(err)
	}
	freshDigest, err := fresh.Digest()
	if err != nil || freshDigest == digest {
		t.Fatal("fresh provisioning reused material", err)
	}
}

func TestOSCOREContextRejectsUnsafeAndMalformedFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "key.json")
	context, err := CreateOSCOREContext(path, OSCOREOwner{Realm: "r", Listener: "oscore"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(data)
	for _, malformed := range [][]byte{append(append([]byte{}, data[:len(data)-1]...), []byte(`,"version":1}`)...), append(append([]byte{}, data...), []byte(` {}`)...), []byte(`{"version":1}`), bytes.Repeat([]byte("x"), 4097)} {
		if err := os.WriteFile(path, malformed, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadOSCOREContext(path); err == nil {
			t.Fatal("accepted malformed context")
		}
	}
	bad := context.record
	bad.MasterSecret = "not a key"
	malformed, err := json.Marshal(bad)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, malformed, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadOSCOREContext(path); err == nil {
		t.Fatal("accepted malformed key material")
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadOSCOREContext(path); err == nil {
		t.Fatal("accepted public context file")
	}
	link := filepath.Join(root, "symlink")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadOSCOREContext(link); err == nil {
		t.Fatal("followed context symlink")
	}
}
