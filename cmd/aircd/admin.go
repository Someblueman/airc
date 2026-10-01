package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Someblueman/airc/internal/admin"
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
		if path != "" && samePath(moderationFile, path) {
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

func samePath(left, right string) bool {
	canonical := func(path string) string {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return path
		}
		if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
			return resolved
		}
		if parent, err := filepath.EvalSymlinks(filepath.Dir(absolute)); err == nil {
			return filepath.Join(parent, filepath.Base(absolute))
		}
		return absolute
	}
	if canonical(left) == canonical(right) {
		return true
	}
	l, le := os.Stat(left)
	r, re := os.Stat(right)
	return le == nil && re == nil && os.SameFile(l, r)
}

func distinctDataFiles(paths ...string) error {
	for i, left := range paths {
		if left == "" {
			continue
		}
		for _, right := range paths[i+1:] {
			if right != "" && samePath(left, right) {
				return fmt.Errorf("credential and data files must have distinct paths: %s and %s", left, right)
			}
		}
	}
	return nil
}
