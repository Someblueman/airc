package config

import (
	"errors"
	"net"
	"net/url"
	"slices"
	"strings"

	"github.com/Someblueman/airc/internal/gateway/core"
)

type PostmarkEmail struct {
	Username     string               `json:"username"`
	PasswordFile string               `json:"password_file"`
	WebhookURL   string               `json:"webhook_url"`
	Routes       []PostmarkEmailRoute `json:"routes"`
}

type PostmarkEmailRoute struct {
	From      string      `json:"from"`
	To        string      `json:"to"`
	Principal string      `json:"principal"`
	Target    core.Target `json:"target"`
}

func postmarkUsername(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for i := 0; i < len(value); i++ {
		b := value[i]
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '-') {
			return false
		}
	}
	return true
}

func postmarkWebhookURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.Path != "/v1/postmark/email" || u.RawPath != "" {
		return false
	}
	host := u.Hostname()
	if host == "" || host != strings.ToLower(host) || strings.HasSuffix(host, ".") || u.Port() != "" || u.Host != host || net.ParseIP(host) != nil || !redisServerName(host) {
		return false
	}
	return u.String() == raw
}

// ValidPostmarkAddress accepts an ASCII mailbox spelling without normalizing
// its local part, so routing remains an exact case-sensitive comparison.
func ValidPostmarkAddress(value string) bool {
	if len(value) > 254 || strings.Count(value, "@") != 1 {
		return false
	}
	local, domain, _ := strings.Cut(value, "@")
	if len(local) < 1 || len(local) > 64 || strings.HasPrefix(local, ".") || strings.HasSuffix(local, ".") || strings.Contains(local, "..") {
		return false
	}
	for i := 0; i < len(local); i++ {
		b := local[i]
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '.' || b == '_' || b == '%' || b == '+' || b == '-') {
			return false
		}
	}
	if domain == "" || domain != strings.ToLower(domain) || !strings.Contains(domain, ".") || strings.HasSuffix(domain, ".") || net.ParseIP(domain) != nil || !redisServerName(domain) {
		return false
	}
	return true
}

// ValidatePostmarkEmail validates the provider authentication and fixed route map.
func (c Config) ValidatePostmarkEmail(options PostmarkEmail) error {
	if !postmarkUsername(options.Username) || !absolute(options.PasswordFile) || !postmarkWebhookURL(options.WebhookURL) || len(options.Routes) < 1 || len(options.Routes) > 128 || len(c.RecipientKeys) == 0 {
		return errors.New("invalid Postmark email credentials, webhook URL or route count")
	}
	type routeKey struct{ from, to string }
	seen := make(map[routeKey]bool, len(options.Routes))
	principals := make(map[string]Principal, len(c.Principals))
	for _, principal := range c.Principals {
		principals[principal.ID] = principal
	}
	for _, route := range options.Routes {
		key := routeKey{from: route.From, to: route.To}
		if !ValidPostmarkAddress(route.From) || !ValidPostmarkAddress(route.To) || !id(route.Principal) || seen[key] {
			return errors.New("invalid or duplicate Postmark email address route")
		}
		seen[key] = true
		principal, exists := principals[route.Principal]
		if !exists || principal.Revoked || len(principal.SigningKeys) == 0 || !slices.Contains(principal.Operations, core.Send) {
			return errors.New("postmark email route requires an active signed Send principal")
		}
		target := route.Target
		if err := target.Normalize(); err != nil || target != route.Target {
			return errors.New("postmark email route has a noncanonical target")
		}
		granted := false
		for _, candidate := range principal.Targets {
			if candidate.Normalize() == nil && candidate == target {
				granted = true
				break
			}
		}
		if !granted {
			return errors.New("postmark email route target is not granted to its principal")
		}
	}
	return nil
}

func (c Config) validatePostmarkEmailListeners() error {
	listeners := make(map[string]bool)
	for _, listener := range c.Listeners {
		if listener.Protocol != "postmark_email" {
			if listener.PostmarkEmail != nil {
				return errors.New("postmark_email options require a postmark_email listener")
			}
			continue
		}
		if listener.PostmarkEmail == nil {
			return errors.New("postmark_email listener requires options")
		}
		if err := c.ValidatePostmarkEmail(*listener.PostmarkEmail); err != nil {
			return err
		}
		if len(listener.AllowedOrigins) != 0 || listener.AnonymousPrincipal != "" {
			return errors.New("postmark_email cannot use origins or anonymous access")
		}
		if listener.Enabled {
			if listener.TLSCertFile == "" || listener.TLSKeyFile == "" {
				return errors.New("enabled postmark_email requires TLS certificate and key")
			}
			if len(listener.Operations) != 1 || listener.Operations[0] != core.Send {
				return errors.New("postmark_email supports only the send operation")
			}
		}
		listeners[listener.ID] = true
	}
	for _, route := range c.ReturnRoutes {
		if route.Protocol == "postmark_email" || listeners[route.Listener] {
			return errors.New("postmark_email uses configured address routes and cannot use return routes")
		}
	}
	return nil
}
