package config

import (
	"errors"
	"slices"

	"github.com/Someblueman/airc/internal/gateway/core"
)

type GitHubWebhook struct {
	SecretFile       string               `json:"secret_file"`
	FreshnessSeconds int64                `json:"freshness_seconds"`
	Routes           []GitHubWebhookRoute `json:"routes"`
}

type GitHubWebhookRoute struct {
	RepositoryID int64       `json:"repository_id"`
	AuthorID     int64       `json:"author_id"`
	Principal    string      `json:"principal"`
	Target       core.Target `json:"target"`
}

// ValidateGitHubWebhook checks webhook route grants against the configured
// active principals. The secret itself is loaded separately from SecretFile.
func (c Config) ValidateGitHubWebhook(options GitHubWebhook) error {
	if !absolute(options.SecretFile) || options.FreshnessSeconds < 1 || options.FreshnessSeconds > 300 || len(options.Routes) < 1 || len(options.Routes) > 128 {
		return errors.New("invalid GitHub webhook secret, freshness or route count")
	}
	principals := make(map[string]Principal, len(c.Principals))
	for _, principal := range c.Principals {
		principals[principal.ID] = principal
	}
	type repositoryAuthor struct {
		repository int64
		author     int64
	}
	seen := make(map[repositoryAuthor]bool, len(options.Routes))
	for _, route := range options.Routes {
		key := repositoryAuthor{repository: route.RepositoryID, author: route.AuthorID}
		if route.RepositoryID <= 0 || route.AuthorID <= 0 || !id(route.Principal) || seen[key] {
			return errors.New("invalid or duplicate GitHub webhook repository and author route")
		}
		seen[key] = true
		principal, exists := principals[route.Principal]
		if !exists || principal.Revoked || !slices.Contains(principal.Operations, core.Send) {
			return errors.New("GitHub webhook route requires an active send principal")
		}
		target := route.Target
		if err := target.Normalize(); err != nil {
			return errors.New("GitHub webhook route has an invalid target")
		}
		granted := false
		for _, candidate := range principal.Targets {
			if candidate.Normalize() == nil && candidate == target {
				granted = true
				break
			}
		}
		if !granted {
			return errors.New("GitHub webhook route target is not granted to its principal")
		}
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
