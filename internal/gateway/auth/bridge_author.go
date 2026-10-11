package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/protocol"
)

// maxNickLength is AIRC's nickname limit (protocol.ValidNick).
const maxNickLength = 30

// BridgeAuthor is who a bridged external user posts as. It is derived only
// from the provider-verified identifier (never a display name or nickname):
// a mapped registered AIRC account with its in-memory token, else a guest
// nickname. Source is the label forwarded to AIRC with WEBIRC, so AIRC's
// per-source limits apply to each external user separately.
type BridgeAuthor struct {
	Author     core.Author
	Credential core.Credential
	Source     string
	External   string
}

// BridgeAuthors maps one bridge's verified external identities to authors.
// It is immutable after construction.
type BridgeAuthors struct {
	network string
	prefix  string
	local   string // the bridge account's own domain or homeserver
	guests  bool
	mapped  map[string]BridgeAuthor
}

// NewBridgeAuthors reads every mapped account token now, so a bad token file
// fails startup rather than a later post. Keys are the external identifiers
// compared without case: IRC services accounts, XMPP JIDs and Matrix user IDs
// are all case-insensitive (or lowercase-only) at their providers.
func NewBridgeAuthors(network, prefix, local string, guests bool, accounts []config.BridgeAccount) (*BridgeAuthors, error) {
	if network == "" || prefix == "" || len(prefix) > 16 || !protocol.ValidNick(prefix) {
		return nil, ErrCredential
	}
	b := &BridgeAuthors{network: network, prefix: prefix, local: strings.ToLower(local), guests: guests, mapped: make(map[string]BridgeAuthor, len(accounts))}
	for _, a := range accounts {
		token, err := ReadToken(a.AccountTokenFile)
		if err != nil {
			return nil, ErrCredential
		}
		author, credential, err := core.NewAuthor(a.Nick, token)
		if err != nil {
			return nil, ErrCredential
		}
		external := strings.ToLower(a.External)
		b.mapped[external] = BridgeAuthor{Author: author, Credential: credential, Source: bridgeSource(network, author.Nick, external), External: external}
	}
	return b, nil
}

// Resolve returns the author for a provider-verified external identifier, or
// false when it is unmapped and the bridge admits no guests.
func (b *BridgeAuthors) Resolve(external string) (BridgeAuthor, bool) {
	if b == nil || external == "" {
		return BridgeAuthor{}, false
	}
	external = strings.ToLower(external)
	if a, ok := b.mapped[external]; ok {
		return a, true
	}
	if !b.guests {
		return BridgeAuthor{}, false
	}
	nick := GuestNick(b.prefix, b.shortName(external), external)
	if !protocol.ValidNick(nick) {
		return BridgeAuthor{}, false
	}
	return BridgeAuthor{Author: core.Author{Nick: nick}, Source: bridgeSource(b.network, nick, external), External: external}, true
}

// Fingerprint names the mapping for recovery identities: it changes when the
// prefix, guest policy or any mapping changes, never with token contents.
func (b *BridgeAuthors) Fingerprint() string {
	if b == nil {
		return ""
	}
	parts := []string{"airc-bridge-authors-v1", b.network, b.prefix, b.local}
	if b.guests {
		parts = append(parts, "guests")
	}
	for external, a := range b.mapped {
		parts = append(parts, ircDigest(external, a.Author.Nick))
	}
	slices.Sort(parts[4:])
	return ircDigest(parts...)
}

// shortName drops the bridge's own domain: alice@example.org on an
// example.org bridge is "alice", and @alice:example.org on an example.org
// homeserver is "alice". Foreign users keep their whole identifier.
func (b *BridgeAuthors) shortName(external string) string {
	if rest, ok := strings.CutPrefix(external, "@"); ok {
		if local, server, ok := strings.Cut(rest, ":"); ok && b.local != "" && server == b.local {
			return local // Matrix user ID on the bridge's homeserver
		}
		return rest // other Matrix user IDs, without the sigil
	}
	if local, domain, ok := strings.Cut(external, "@"); ok && local != "" && b.local != "" && domain == b.local {
		return local // XMPP bare JID on the bridge's domain
	}
	return external
}

// GuestNick derives a guest nickname within AIRC's grammar (at most 30
// bytes): prefix + the lowercased identifier when every byte is a lowercase
// letter, digit, '-' or '_'. Otherwise (or when too long) other bytes become
// '-', the result is shortened, and "|" plus a hash of the whole external
// identifier is appended. A plain nickname never contains '|', so the two
// forms never collide, and different identifiers get different hashes.
func GuestNick(prefix, short, external string) string {
	var b strings.Builder
	lossless := short != ""
	for _, c := range []byte(strings.ToLower(short)) {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('-')
		lossless = false
	}
	nick := prefix + b.String()
	if lossless && len(nick) <= maxNickLength {
		return nick
	}
	sum := sha256.Sum256([]byte("airc-bridge-guest-v1\x00" + strings.ToLower(external)))
	suffix := "|" + hex.EncodeToString(sum[:])[:10]
	base := b.String()
	if room := maxNickLength - len(prefix) - len(suffix); len(base) > room {
		base = base[:max(room, 0)]
	}
	return prefix + base + suffix
}

// bridgeSource is "bridge:<id>:<nick>" in AIRC's source grammar, or a hash of
// the external identifier in place of a nickname it cannot carry or fit.
func bridgeSource(network, nick, external string) string {
	label := strings.ToLower(nick)
	plain := true
	for _, c := range []byte(label) {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			plain = false
		}
	}
	source := "bridge:" + network + ":" + label
	if plain && len(source) <= protocol.MaxSourceLength && protocol.ValidSource(source) {
		return source
	}
	sum := sha256.Sum256([]byte("airc-bridge-source-v1\x00" + network + "\x00" + external))
	source = "bridge:" + network + ":" + hex.EncodeToString(sum[:])[:16]
	if len(source) <= protocol.MaxSourceLength && protocol.ValidSource(source) {
		return source
	}
	return "bridge:" + hex.EncodeToString(sum[:])[:32]
}
