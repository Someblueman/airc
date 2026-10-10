package config

import (
	"errors"
	"slices"
	"strings"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/s3wire"
)

// S3 binds one object-prefix mailbox to a fixed result route and AIRC grant.
type S3 struct {
	ID             string  `json:"id"`
	Enabled        bool    `json:"enabled"`
	Region         string  `json:"region"`
	Bucket         string  `json:"bucket"`
	AccountID      string  `json:"account_id"`
	Prefix         string  `json:"prefix"`
	CredentialFile string  `json:"credential_file"`
	Route          S3Route `json:"route"`
}

type S3Route struct {
	Principal              string           `json:"principal"`
	Target                 core.Target      `json:"target"`
	Operations             []core.Operation `json:"operations"`
	RecipientKeyID         string           `json:"recipient_key_id"`
	RecipientPublicKeyFile string           `json:"recipient_public_key_file"`
}

func s3PrefixesOverlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func (c Config) validateS3() error {
	if len(c.S3) > 16 {
		return errors.New("S3 owner count limit exceeded")
	}
	ids := make(map[string]bool, len(c.S3))
	type prefixOwner struct{ bucket, prefix string }
	var prefixes []prefixOwner
	for _, owner := range c.S3 {
		if !id(owner.ID) || ids[owner.ID] || !absolute(owner.CredentialFile) {
			return errors.New("invalid or duplicate S3 owner")
		}
		ids[owner.ID] = true
		if err := s3wire.ValidateLocation(owner.Region, owner.Bucket, owner.AccountID, owner.Prefix); err != nil {
			return errors.New("invalid S3 object mailbox location")
		}
		if owner.Enabled {
			for _, listener := range c.Listeners {
				if listener.Enabled && slices.Contains([]string{"offline_bundle", "file_mailbox", "stdio", "socketpair", "inherited", "fifo", "uart"}, listener.Protocol) {
					return errors.New("S3 cannot accompany an exclusive command-owned listener")
				}
			}
		}
		for _, prior := range prefixes {
			if prior.bucket == owner.Bucket && s3PrefixesOverlap(prior.prefix, owner.Prefix) {
				return errors.New("S3 mailbox prefixes overlap within a bucket")
			}
		}
		prefixes = append(prefixes, prefixOwner{bucket: owner.Bucket, prefix: owner.Prefix})

		route := owner.Route
		if !id(route.Principal) || !id(route.RecipientKeyID) || !absolute(route.RecipientPublicKeyFile) || len(route.Operations) == 0 || operations(route.Operations) != nil || c.ResponseSigningKey == nil || len(c.RecipientKeys) == 0 {
			return errors.New("S3 requires a fixed route, response signer and ingress recipient keys")
		}
		target := route.Target
		if err := target.Normalize(); err != nil || target != route.Target {
			return errors.New("S3 route has an invalid target")
		}
		principalFound := false
		for _, principal := range c.Principals {
			if principal.ID != route.Principal || principal.Revoked || len(principal.SigningKeys) == 0 {
				continue
			}
			grantedTarget := false
			for _, granted := range principal.Targets {
				if granted.Normalize() == nil && granted == target {
					grantedTarget = true
					break
				}
			}
			if !grantedTarget {
				continue
			}
			principalFound = true
			for _, op := range route.Operations {
				if !s3Operation(op) || !slices.Contains(principal.Operations, op) {
					return errors.New("S3 route exceeds the signed principal grant")
				}
				if (op == core.HistoryRead || op == core.Search) && c.CursorKeyFile == "" {
					return errors.New("S3 history and search routes require a cursor key")
				}
			}
			break
		}
		if !principalFound {
			return errors.New("S3 route requires an active signing principal with the exact target grant")
		}
	}
	return nil
}

func s3Operation(op core.Operation) bool {
	return op == core.Send || op == core.ContextRead || op == core.HistoryRead || op == core.Search
}
