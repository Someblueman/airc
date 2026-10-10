package config

import (
	"errors"
	"slices"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/sqswire"
)

// SQS binds one standard request queue to a fixed result queue and AIRC grant.
type SQS struct {
	ID              string   `json:"id"`
	Enabled         bool     `json:"enabled"`
	Region          string   `json:"region"`
	RequestQueueURL string   `json:"request_queue_url"`
	CredentialFile  string   `json:"credential_file"`
	Route           SQSRoute `json:"route"`
}

type SQSRoute struct {
	Principal              string           `json:"principal"`
	ResultQueueURL         string           `json:"result_queue_url"`
	Target                 core.Target      `json:"target"`
	Operations             []core.Operation `json:"operations"`
	RecipientKeyID         string           `json:"recipient_key_id"`
	RecipientPublicKeyFile string           `json:"recipient_public_key_file"`
}

func (c Config) validateSQS() error {
	if len(c.SQS) > 16 {
		return errors.New("SQS owner count limit exceeded")
	}
	ids := make(map[string]bool, len(c.SQS))
	queues := make(map[string]bool, len(c.SQS)*2)
	for _, owner := range c.SQS {
		if !id(owner.ID) || ids[owner.ID] || !absolute(owner.CredentialFile) {
			return errors.New("invalid or duplicate SQS owner")
		}
		ids[owner.ID] = true
		requestAccount, requestName, err := sqswire.ParseQueueURL(owner.Region, owner.RequestQueueURL)
		if err != nil {
			return errors.New("SQS request queue URL is invalid for its region")
		}
		route := owner.Route
		resultAccount, resultName, err := sqswire.ParseQueueURL(owner.Region, route.ResultQueueURL)
		if err != nil || requestAccount != resultAccount || requestName == resultName {
			return errors.New("SQS result queue must be distinct in the same region and account")
		}
		for _, queue := range []string{owner.Region + "\x00" + requestAccount + "\x00" + requestName, owner.Region + "\x00" + resultAccount + "\x00" + resultName} {
			if queues[queue] {
				return errors.New("SQS request and result queue roles collide across owners")
			}
			queues[queue] = true
		}
		if !id(route.Principal) || !id(route.RecipientKeyID) || !absolute(route.RecipientPublicKeyFile) || len(route.Operations) == 0 || operations(route.Operations) != nil || c.ResponseSigningKey == nil || len(c.RecipientKeys) == 0 {
			return errors.New("SQS requires a fixed route, response signer and ingress recipient keys")
		}
		target := route.Target
		if err := target.Normalize(); err != nil || target != route.Target {
			return errors.New("SQS route has an invalid target")
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
				if !sqsOperation(op) || !slices.Contains(principal.Operations, op) {
					return errors.New("SQS route exceeds the signed principal grant")
				}
				if (op == core.HistoryRead || op == core.Search) && c.CursorKeyFile == "" {
					return errors.New("SQS history and search routes require a cursor key")
				}
			}
			break
		}
		if !principalFound {
			return errors.New("SQS route requires an active signing principal with the exact target grant")
		}
	}
	return nil
}

func sqsOperation(op core.Operation) bool {
	return op == core.Send || op == core.HistoryRead || op == core.Search || op == core.ContextRead
}
