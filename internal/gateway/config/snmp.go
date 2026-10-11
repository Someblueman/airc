package config

import (
	"encoding/hex"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/snmpwire"
)

// SNMP grants bind an authenticated authoritative engine and user to a fixed
// context, bound author, target and notification allowlist. Keys are localized USM
// keys in private files; configuration never contains passwords or key bytes.
type SNMP struct {
	Routes []SNMPRoute `json:"routes"`
}
type SNMPRoute struct {
	EngineID              string             `json:"engine_id"`
	User                  string             `json:"user"`
	Authentication        string             `json:"authentication"`
	AuthenticationKeyFile string             `json:"authentication_key_file"`
	ContextEngineID       string             `json:"context_engine_id"`
	ContextName           string             `json:"context_name"`
	BoundAuthor           BoundAuthor        `json:"bound_author"`
	Target                core.Target        `json:"target"`
	Notifications         []SNMPNotification `json:"notifications"`
}
type SNMPNotification struct {
	OID    string      `json:"oid"`
	Name   string      `json:"name"`
	Fields []SNMPField `json:"fields,omitempty"`
}
type SNMPField struct {
	OID      string `json:"oid"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
}

func SNMPEngineID(text string) ([]byte, error) {
	if len(text) < 10 || len(text) > 64 {
		return nil, errors.New("invalid SNMP engine ID")
	}
	b, err := hex.DecodeString(text)
	if err != nil || hex.EncodeToString(b) != text || !snmpwire.ValidEngineID(b) {
		return nil, errors.New("invalid SNMP engine ID")
	}
	return b, nil
}

func snmpLabel(s string) bool {
	if len(s) == 0 || len(s) > 64 || !utf8.ValidString(s) || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func snmpText(s string, nonempty bool) bool {
	return len(s) <= 32 && (!nonempty || len(s) > 0) && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

// ValidateSNMPGrants validates syntax and bounds independently of the live
// principal snapshot. Authenticators must additionally bind private identities.
func ValidateSNMPGrants(options SNMP) error {
	if len(options.Routes) < 1 || len(options.Routes) > 128 {
		return errors.New("SNMP requires 1-128 engine/user grants")
	}
	seen := map[[2]string]bool{}
	for _, r := range options.Routes {
		if _, err := SNMPEngineID(r.EngineID); err != nil {
			return err
		}
		if _, err := SNMPEngineID(r.ContextEngineID); err != nil {
			return err
		}
		pair := [2]string{r.EngineID, r.User}
		if seen[pair] || !snmpText(r.User, true) || !snmpText(r.ContextName, false) || !protocolNick(r.BoundAuthor) {
			return errors.New("invalid or duplicate SNMP engine/user grant")
		}
		seen[pair] = true
		if r.Authentication != snmpwire.HMACSHA256192 && r.Authentication != snmpwire.HMACSHA196 {
			return errors.New("unsupported SNMP authentication protocol")
		}
		if !filepath.IsAbs(r.AuthenticationKeyFile) || filepath.Clean(r.AuthenticationKeyFile) != r.AuthenticationKeyFile {
			return errors.New("SNMP authentication key requires a normalized absolute path")
		}
		if len(r.Notifications) < 1 || len(r.Notifications) > 128 {
			return errors.New("SNMP requires 1-128 allowed notifications per grant")
		}
		notifications := map[string]bool{}
		for _, n := range r.Notifications {
			if !snmpwire.OIDText(n.OID) || notifications[n.OID] || !snmpLabel(n.Name) || len(n.Fields) > 16 {
				return errors.New("invalid SNMP notification allowlist")
			}
			notifications[n.OID] = true
			fields, names := map[string]bool{}, map[string]bool{}
			for _, f := range n.Fields {
				if !snmpwire.OIDText(f.OID) || f.OID == "1.3.6.1.2.1.1.3.0" || f.OID == "1.3.6.1.6.3.1.1.4.1.0" || fields[f.OID] || names[f.Name] || !snmpLabel(f.Name) || !slices.Contains([]string{"text", "hex", "integer", "unsigned", "oid", "ip"}, f.Type) {
					return errors.New("invalid SNMP notification field")
				}
				fields[f.OID], names[f.Name] = true, true
			}
		}
	}
	return nil
}

func (c Config) validateSNMP(l Listener) error {
	if l.Protocol != "snmp_v3" {
		if l.SNMP != nil {
			return errors.New("SNMP options require snmp_v3 protocol")
		}
		return nil
	}
	if l.SNMP == nil || len(l.Operations) != 1 || l.Operations[0] != core.Send || l.TLSCertFile != "" || l.TLSKeyFile != "" || len(l.AllowedOrigins) != 0 {
		return errors.New("SNMP requires send-only UDP grants")
	}
	if err := ValidateSNMPGrants(*l.SNMP); err != nil {
		return err
	}
	for _, r := range l.SNMP.Routes {
		target := r.Target
		if target.Normalize() != nil || target != r.Target {
			return errors.New("SNMP grant target must be canonical")
		}
	}
	return nil
}
