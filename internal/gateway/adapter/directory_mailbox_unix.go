//go:build darwin || linux

package adapter

import (
	"errors"
	"io"
	"os"
	"slices"
	"strings"
)

const MaxDirectoryMailboxFiles = 128

// DirectoryMailbox owns a private root and its existing inbox, processing and
// results directories. Calls are sequential. Content authentication is separate.
type DirectoryMailbox struct{ files *FileMailbox }

func OpenDirectoryMailbox(path string) (*DirectoryMailbox, error) {
	files, err := OpenFileMailbox(path)
	if err != nil {
		return nil, err
	}
	m := &DirectoryMailbox{files: files}
	if err := m.private(); err != nil {
		_ = files.Close()
		return nil, err
	}
	return m, nil
}

func (m *DirectoryMailbox) Close() error { return m.files.Close() }

func directoryMailboxName(name string) bool {
	if !strings.HasSuffix(name, ".json") {
		return false
	}
	stem := strings.TrimSuffix(name, ".json")
	if len(stem) < 1 || len(stem) > 64 {
		return false
	}
	for _, c := range []byte(stem) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func (m *DirectoryMailbox) private() error {
	if err := m.files.private(); err != nil {
		return err
	}
	for _, name := range []string{"inbox", "processing", "results"} {
		info, err := m.files.root.Lstat(name)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode().Perm() != 0700 || !unixPathOwner(info) {
			return errors.New("directory mailbox requires private owned subdirectories")
		}
	}
	return nil
}

func (m *DirectoryMailbox) entries(name string, limit int) ([]string, error) {
	directory, err := m.files.root.Open(name)
	if err != nil {
		return nil, err
	}
	entries, readErr := directory.ReadDir(limit + 1)
	closeErr := directory.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(entries) > limit {
		return nil, errors.New("directory mailbox file quota exceeded")
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !directoryMailboxName(entry.Name()) {
			return nil, errors.New("directory mailbox requires bounded JSON file names")
		}
		names = append(names, entry.Name())
	}
	slices.Sort(names)
	return names, nil
}

// Claim resumes one processing file before selecting a lexically ordered inbox
// entry. Unread results block name reuse; a full results directory stops intake.
func (m *DirectoryMailbox) Claim() (string, []byte, error) {
	if err := m.private(); err != nil {
		return "", nil, err
	}
	processing, err := m.entries("processing", 1)
	if err != nil {
		return "", nil, err
	}
	results, err := m.entries("results", MaxDirectoryMailboxFiles)
	if err != nil {
		return "", nil, err
	}
	if len(results) == MaxDirectoryMailboxFiles {
		return "", nil, ErrMailboxResultPending
	}
	if len(processing) == 1 {
		name := processing[0]
		if slices.Contains(results, name) {
			return "", nil, ErrMailboxResultPending
		}
		body, err := m.files.read("processing/" + name)
		return name, body, err
	}
	inbox, err := m.entries("inbox", MaxDirectoryMailboxFiles)
	if err != nil {
		return "", nil, err
	}
	for _, name := range inbox {
		if slices.Contains(results, name) {
			continue
		}
		body, err := m.files.read("inbox/" + name)
		if err != nil {
			return "", nil, err
		}
		if err := m.files.root.Rename("inbox/"+name, "processing/"+name); err != nil {
			clear(body)
			return "", nil, err
		}
		for _, directory := range []string{"processing", "inbox"} {
			if err := m.files.syncDirectory(directory); err != nil {
				clear(body)
				return "", nil, err
			}
		}
		clear(body)
		claimed, err := m.files.read("processing/" + name)
		return name, claimed, err
	}
	return "", nil, os.ErrNotExist
}

// Publish syncs a named complete result before removing the claimed input.
func (m *DirectoryMailbox) Publish(name string, data []byte) error {
	if err := m.private(); err != nil {
		return err
	}
	if !directoryMailboxName(name) {
		return errors.New("invalid directory mailbox file name")
	}
	processing, err := m.entries("processing", 1)
	if err != nil {
		return err
	}
	if len(processing) != 1 || processing[0] != name {
		return errors.New("directory mailbox publication does not match claimed file")
	}
	results, err := m.entries("results", MaxDirectoryMailboxFiles)
	if err != nil {
		return err
	}
	if len(results) >= MaxDirectoryMailboxFiles {
		return ErrMailboxResultPending
	}
	return m.files.publish("processing/"+name, "results/"+name, data)
}
