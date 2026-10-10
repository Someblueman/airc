package config

import (
	"errors"
	"net"
	"net/url"
	"slices"
	"strings"

	"github.com/Someblueman/airc/internal/gateway/core"
)

type TwilioSMS struct {
	AccountSID    string           `json:"account_sid"`
	AuthTokenFile string           `json:"auth_token_file"`
	WebhookURL    string           `json:"webhook_url"`
	Routes        []TwilioSMSRoute `json:"routes"`
}

type TwilioSMSRoute struct {
	From      string      `json:"from"`
	To        string      `json:"to"`
	Principal string      `json:"principal"`
	Target    core.Target `json:"target"`
}

func twilioAccountSID(value string) bool {
	if len(value) != 34 || !strings.HasPrefix(value, "AC") {
		return false
	}
	for i := 2; i < len(value); i++ {
		b := value[i]
		if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
			return false
		}
	}
	return true
}

func twilioPhone(value string) bool {
	if len(value) < 3 || len(value) > 16 || value[0] != '+' || value[1] < '1' || value[1] > '9' {
		return false
	}
	for i := 2; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

func twilioWebhookURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.Path != "/v1/twilio/sms" || u.RawPath != "" {
		return false
	}
	host := u.Hostname()
	if host == "" || host != strings.ToLower(host) || strings.HasSuffix(host, ".") || u.Port() != "" || u.Host != host || net.ParseIP(host) != nil || !redisServerName(host) {
		return false
	}
	return u.String() == raw
}

// ValidateTwilioSMS checks webhook routes against configured signed Send grants.
func (c Config) ValidateTwilioSMS(options TwilioSMS) error {
	if !twilioAccountSID(options.AccountSID) || !absolute(options.AuthTokenFile) || !twilioWebhookURL(options.WebhookURL) || len(options.Routes) < 1 || len(options.Routes) > 128 || len(c.RecipientKeys) == 0 {
		return errors.New("invalid Twilio SMS account, credential, webhook URL or route count")
	}
	principals := make(map[string]Principal, len(c.Principals))
	for _, principal := range c.Principals {
		principals[principal.ID] = principal
	}
	type routeKey struct{ from, to string }
	seen := make(map[routeKey]bool, len(options.Routes))
	for _, route := range options.Routes {
		key := routeKey{from: route.From, to: route.To}
		if !twilioPhone(route.From) || !twilioPhone(route.To) || !id(route.Principal) || seen[key] {
			return errors.New("invalid or duplicate Twilio SMS address route")
		}
		seen[key] = true
		principal, exists := principals[route.Principal]
		if !exists || principal.Revoked || len(principal.SigningKeys) == 0 || !slices.Contains(principal.Operations, core.Send) {
			return errors.New("Twilio SMS route requires an active signed Send principal")
		}
		target := route.Target
		if err := target.Normalize(); err != nil || target != route.Target {
			return errors.New("Twilio SMS route has a noncanonical target")
		}
		granted := false
		for _, candidate := range principal.Targets {
			if candidate.Normalize() == nil && candidate == target {
				granted = true
				break
			}
		}
		if !granted {
			return errors.New("Twilio SMS route target is not granted to its principal")
		}
	}
	return nil
}

func (c Config) validateTwilioSMSListeners() error {
	listeners := make(map[string]bool)
	for _, listener := range c.Listeners {
		if listener.Protocol != "twilio_sms" {
			if listener.TwilioSMS != nil {
				return errors.New("twilio_sms options require a twilio_sms listener")
			}
			continue
		}
		if listener.TwilioSMS == nil {
			return errors.New("twilio_sms listener requires options")
		}
		if err := c.ValidateTwilioSMS(*listener.TwilioSMS); err != nil {
			return err
		}
		if len(listener.AllowedOrigins) != 0 || listener.AnonymousPrincipal != "" {
			return errors.New("twilio_sms cannot use origins or anonymous access")
		}
		if listener.Enabled {
			if listener.TLSCertFile == "" || listener.TLSKeyFile == "" {
				return errors.New("enabled twilio_sms requires TLS certificate and key")
			}
			if len(listener.Operations) != 1 || listener.Operations[0] != core.Send {
				return errors.New("twilio_sms supports only the send operation")
			}
		}
		listeners[listener.ID] = true
	}
	for _, route := range c.ReturnRoutes {
		if route.Protocol == "twilio_sms" || listeners[route.Listener] {
			return errors.New("twilio_sms uses configured phone routes and cannot use return routes")
		}
	}
	return nil
}
