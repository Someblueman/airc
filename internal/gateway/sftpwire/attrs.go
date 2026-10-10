package sftpwire

import (
	"errors"
	"fmt"
)

const (
	attrSize        = 0x00000001
	attrUIDGID      = 0x00000002
	attrPermissions = 0x00000004
	attrACModTime   = 0x00000008
	attrExtended    = 0x80000000
	fileTypeMask    = 0170000
	fileTypeRegular = 0100000
	fileTypeDir     = 0040000
	statusEOF       = 1
	statusNoSuch    = 2
	statusOK        = 0
)

var (
	ErrNotExist = errors.New("SFTP path does not exist")
	errEOF      = errors.New("SFTP end of file")
)

// Attrs contains the complete v3 attributes required by this client.
type Attrs struct {
	Size        uint64
	UID, GID    uint32
	Permissions uint32
}

func (a Attrs) Regular() bool   { return a.Permissions&fileTypeMask == fileTypeRegular }
func (a Attrs) Directory() bool { return a.Permissions&fileTypeMask == fileTypeDir }

func parseAttrs(d *decoder) (Attrs, error) {
	flags, err := d.u32()
	if err != nil || flags&^(attrSize|attrUIDGID|attrPermissions|attrACModTime|attrExtended) != 0 ||
		flags&(attrSize|attrUIDGID|attrPermissions) != attrSize|attrUIDGID|attrPermissions {
		return Attrs{}, errPacket
	}
	a := Attrs{}
	if flags&attrSize != 0 {
		a.Size, err = d.u64()
		if err != nil {
			return Attrs{}, err
		}
	}
	if flags&attrUIDGID != 0 {
		a.UID, err = d.u32()
		if err != nil {
			return Attrs{}, err
		}
		a.GID, err = d.u32()
		if err != nil {
			return Attrs{}, err
		}
	}
	if flags&attrPermissions != 0 {
		a.Permissions, err = d.u32()
		if err != nil {
			return Attrs{}, err
		}
	}
	if flags&attrACModTime != 0 {
		if _, err = d.u32(); err != nil {
			return Attrs{}, err
		}
		if _, err = d.u32(); err != nil {
			return Attrs{}, err
		}
	}
	if flags&attrExtended != 0 {
		count, err := d.u32()
		if err != nil || count > 64 {
			return Attrs{}, errPacket
		}
		for range count {
			if _, err := d.string(4096); err != nil {
				return Attrs{}, err
			}
			if _, err := d.string(maxPacketBytes); err != nil {
				return Attrs{}, err
			}
		}
	}
	if err := d.done(); err != nil {
		return Attrs{}, err
	}
	return a, nil
}

func sameAttrs(a, b Attrs) bool {
	return a.Size == b.Size && a.UID == b.UID && a.GID == b.GID && a.Permissions == b.Permissions
}

func parseStatus(d *decoder) error {
	code, err := d.u32()
	if err != nil {
		return err
	}
	if _, err := d.string(4096); err != nil { // Server diagnostics are bounded but not trusted.
		return err
	}
	if _, err := d.string(256); err != nil {
		return err
	}
	if err := d.done(); err != nil {
		return err
	}
	switch code {
	case statusOK:
		return nil
	case statusEOF:
		return errEOF
	case statusNoSuch:
		return ErrNotExist
	default:
		return fmt.Errorf("SFTP status %d", code)
	}
}
