package main

import (
	"github.com/Someblueman/airc/internal/admin"
	"github.com/Someblueman/airc/internal/server"
)

// configureAdmin enables administration with the credential in tokenFile
// and restores moderation from moderationFile, which resolveState derived
// from the token and main checked is apart from the credential and the other
// state files.
func configureAdmin(srv *server.Server, tokenFile, moderationFile string) error {
	if tokenFile == "" {
		return nil
	}
	token, err := admin.ReadToken(tokenFile)
	if err != nil {
		return err
	}
	if err := srv.EnableAdmin(token); err != nil {
		return err
	}
	return srv.RestoreModeration(moderationFile)
}
