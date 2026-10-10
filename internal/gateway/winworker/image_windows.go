//go:build windows

package winworker

import (
	"bytes"
	"crypto/sha256"
	"debug/pe"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/sys/windows"
)

// Pin locks and verifies the provisioned native worker until the caller closes it.
func Pin(path, digest string) (*os.File, error) {
	if !filepath.IsAbs(path) || len(filepath.VolumeName(path)) != 2 || filepath.Clean(path) != path || len(digest) != 64 || digest != strings.ToLower(digest) {
		return nil, errors.New("worker requires a canonical local absolute path and SHA-256 pin")
	}
	want, err := hex.DecodeString(digest)
	if err != nil {
		return nil, errors.New("invalid native executable digest")
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, errors.New("cannot lock native executable against writes and replacement")
	}
	file := os.NewFile(uintptr(handle), path)
	keep := false
	defer func() {
		if !keep {
			_ = file.Close()
		}
	}()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		return nil, errors.New("worker must be a regular executable without reparse points")
	}
	var final [32768]uint16
	n, err := windows.GetFinalPathNameByHandle(handle, &final[0], uint32(len(final)), 0)
	if err != nil || n == 0 || n >= uint32(len(final)) || !strings.EqualFold(strings.TrimPrefix(windows.UTF16ToString(final[:n]), `\\?\`), path) {
		return nil, errors.New("worker path must name its canonical file directly")
	}
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() < 1 || stat.Size() > 16<<20 {
		return nil, errors.New("native executable must be regular and at most 16 MiB")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, io.LimitReader(file, (16<<20)+1)); err != nil || !bytes.Equal(hash.Sum(nil), want) {
		return nil, errors.New("native executable digest mismatch")
	}
	image, err := pe.NewFile(file)
	if err != nil {
		return nil, errors.New("invalid PE executable")
	}
	defer image.Close()
	machine := uint16(pe.IMAGE_FILE_MACHINE_AMD64)
	if runtime.GOARCH == "arm64" {
		machine = pe.IMAGE_FILE_MACHINE_ARM64
	} else if runtime.GOARCH != "amd64" {
		return nil, errors.New("native worker supports Windows AMD64 and ARM64")
	}
	if image.Machine != machine || image.Characteristics&pe.IMAGE_FILE_EXECUTABLE_IMAGE == 0 || image.Characteristics&pe.IMAGE_FILE_DLL != 0 {
		return nil, errors.New("native executable architecture or image type mismatch")
	}
	keep = true
	return file, nil
}
