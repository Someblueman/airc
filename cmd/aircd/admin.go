package main

import (
	"errors"
	"fmt"

	"github.com/Someblueman/airc/internal/admin"
	"github.com/Someblueman/airc/internal/pathcheck"
	"github.com/Someblueman/airc/internal/server"
)

func configureAdmin(srv *server.Server, tokenFile, moderationFile, historyFile string, otherFiles ...string) error {
	if tokenFile == "" {
		if moderationFile != "" {
			return errors.New("--moderation-file requires --admin-token-file")
		}
		return nil
	}
	if moderationFile == "" {
		moderationFile = tokenFile + ".moderation.json"
		if historyFile != "" {
			moderationFile = historyFile + ".moderation.json"
		}
	}
	files := append([]string{tokenFile, historyFile}, otherFiles...)
	if historyFile != "" {
		files = append(files, historyFile+".topics.json", historyFile+".profiles.json")
	}
	for _, path := range files {
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
