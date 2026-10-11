package config

import (
	"errors"
	"strings"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// GitHubWebhook posts new issue comments on configured repositories. The
// webhook HMAC is the provider check. A commenter mapped in Accounts posts
// as that bound author; any other commenter posts as the guest
// "gh-<login>" when Guests (default true) allows.
type GitHubWebhook struct {
	SecretFile       string               `json:"secret_file"`
	FreshnessSeconds int64                `json:"freshness_seconds"`
	Routes           []GitHubWebhookRoute `json:"routes"`
	Accounts         []GitHubAccount      `json:"accounts,omitempty"`
	Guests           *bool                `json:"guests,omitempty"`
}

type GitHubWebhookRoute struct {
	RepositoryID int64       `json:"repository_id"`
	Target       core.Target `json:"target"`
}

// GitHubAccount maps a GitHub login to an AIRC author. GitHub logins can be
// renamed and later reused by someone else; AuthorID, when set, must also
// match the commenter's immutable numeric user ID.
type GitHubAccount struct {
	Login    string `json:"login"`
	AuthorID int64  `json:"author_id,omitempty"`
	BoundAuthor
}

// GitHubGuests reports whether unmapped commenters post as guests.
func (g GitHubWebhook) GitHubGuests() bool { return g.Guests == nil || *g.Guests }

// ValidGitHubLogin accepts GitHub's login grammar: 1-39 alphanumerics or
// single inner hyphens.
func ValidGitHubLogin(login string) bool {
	if len(login) < 1 || len(login) > 39 || login[0] == '-' || login[len(login)-1] == '-' || strings.Contains(login, "--") {
		return false
	}
	for i := 0; i < len(login); i++ {
		b := login[i]
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-') {
			return false
		}
	}
	return true
}

// ValidateGitHubWebhook checks webhook routes and the account map. The
// secret itself is loaded separately from SecretFile.
func (c Config) ValidateGitHubWebhook(options GitHubWebhook) error {
	if !absolute(options.SecretFile) || options.FreshnessSeconds < 1 || options.FreshnessSeconds > 300 || len(options.Routes) < 1 || len(options.Routes) > 128 || len(options.Accounts) > 1024 {
		return errors.New("invalid GitHub webhook secret, freshness, route or account count")
	}
	seen := make(map[int64]bool, len(options.Routes))
	for _, route := range options.Routes {
		target := route.Target
		if route.RepositoryID <= 0 || seen[route.RepositoryID] || target.Normalize() != nil || target != route.Target {
			return errors.New("invalid or duplicate GitHub webhook repository route")
		}
		seen[route.RepositoryID] = true
	}
	logins := make(map[string]bool, len(options.Accounts))
	for _, account := range options.Accounts {
		key := strings.ToLower(account.Login)
		if !ValidGitHubLogin(account.Login) || logins[key] || account.AuthorID < 0 || !protocolNick(account.BoundAuthor) {
			return errors.New("invalid or duplicate GitHub webhook account")
		}
		logins[key] = true
	}
	if !options.GitHubGuests() && len(options.Accounts) == 0 {
		return ErrRouteAuthor
	}
	return nil
}

func (c Config) validateGitHubWebhookListeners() error {
	listeners := make(map[string]bool)
	for _, listener := range c.Listeners {
		if listener.Protocol != "github_webhook" {
			if listener.GitHubWebhook != nil {
				return errors.New("GitHub webhook options require a github_webhook listener")
			}
			continue
		}
		if listener.GitHubWebhook == nil {
			return errors.New("github_webhook listener requires webhook options")
		}
		if err := c.ValidateGitHubWebhook(*listener.GitHubWebhook); err != nil {
			return err
		}
		if len(listener.AllowedOrigins) != 0 || listener.AnonymousPrincipal != "" {
			return errors.New("github_webhook cannot use origins or anonymous access")
		}
		if listener.Enabled {
			if listener.TLSCertFile == "" || listener.TLSKeyFile == "" {
				return errors.New("enabled github_webhook requires TLS certificate and key")
			}
			if len(listener.Operations) != 1 || listener.Operations[0] != core.Send {
				return errors.New("github_webhook supports only the send operation")
			}
		}
		listeners[listener.ID] = true
	}
	for _, route := range c.ReturnRoutes {
		if route.Protocol == "github_webhook" || listeners[route.Listener] {
			return errors.New("github_webhook uses configured repository routes, not return routes")
		}
	}
	return nil
}
