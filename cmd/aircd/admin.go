package main

import (
	"errors"
	"fmt"

	"github.com/Someblueman/airc/internal/admin"
	"github.com/Someblueman/airc/internal/pathcheck"
	"github.com/Someblueman/airc/internal/server"
)

// configureAdmin enables administration with the credential in tokenFile
// and restores moderation from moderationFile, which main resolved and which
// must be its own file, apart from the credential and the chat data files.
func configureAdmin(srv *server.Server, tokenFile, moderationFile string, otherFiles ...string) error {
	if tokenFile == "" {
		if moderationFile != "" {
			return errors.New("--moderation-file requires --admin-token-file")
		}
		return nil
	}
	if moderationFile == "" {
		return errors.New("moderation needs a file; --admin-token-file resolves one")
	}
	for _, path := range append([]string{tokenFile}, otherFiles...) {
		if path != "" && pathcheck.Same(moderationFile, path) {
			return fmt.Errorf("moderation file must differ from credential and chat data files: %s", path)
		}
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
