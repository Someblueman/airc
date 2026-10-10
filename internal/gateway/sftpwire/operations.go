package sftpwire

import "errors"

const (
	maxFileBytes  = 64 << 10
	ioChunkBytes  = 32 << 10
	openRead      = 1
	openWrite     = 2
	openCreate    = 8
	openExclusive = 32
)

// Lstat returns complete v3 attributes without following the final symlink.
func (c *Client) Lstat(name string) (Attrs, error) {
	if !validPath(name) {
		return Attrs{}, ErrInvalid
	}
	if err := c.lock(); err != nil {
		return Attrs{}, err
	}
	defer c.unlock()
	var fields encoder
	fields.text(name)
	typ, d, err := c.exchange(packetLstat, fields)
	if err != nil {
		return Attrs{}, err
	}
	if typ == packetStatus {
		if err := parseStatus(d); err != nil {
			return Attrs{}, err
		}
		return Attrs{}, ErrInvalid
	}
	if typ != packetAttrs {
		return Attrs{}, ErrInvalid
	}
	attrs, err := parseAttrs(d)
	return attrs, err
}

// ReadFile opens one bounded regular file and checks its opened attributes both
// before and after reading. max is a caller-selected bound no larger than 64 KiB.
func (c *Client) ReadFile(name string, max int) ([]byte, Attrs, error) {
	if !validPath(name) || max < 1 || max > maxFileBytes {
		return nil, Attrs{}, ErrInvalid
	}
	if err := c.lock(); err != nil {
		return nil, Attrs{}, err
	}
	defer c.unlock()
	handle, attrs, err := c.open(name, openRead)
	if err != nil {
		return nil, Attrs{}, err
	}
	closed := false
	closeHandle := func() error {
		if closed {
			return nil
		}
		closed = true
		return c.closeHandle(handle)
	}
	defer closeHandle()
	if !attrs.Regular() || attrs.Size > uint64(max) {
		return nil, attrs, ErrInvalid
	}
	data := make([]byte, 0, int(attrs.Size))
	for offset := uint64(0); ; {
		remaining := max + 1 - len(data)
		if remaining <= 0 {
			clear(data)
			return nil, attrs, ErrInvalid
		}
		want := min(ioChunkBytes, remaining)
		chunk, eof, err := c.readChunk(handle, offset, want)
		if err != nil {
			clear(data)
			return nil, attrs, err
		}
		if len(chunk) > want || len(chunk) > max+1-len(data) {
			clear(data)
			return nil, attrs, ErrInvalid
		}
		data = append(data, chunk...)
		offset += uint64(len(chunk))
		if len(data) > max {
			clear(data)
			return nil, attrs, ErrInvalid
		}
		if eof {
			break
		}
		if len(chunk) == 0 {
			clear(data)
			return nil, attrs, ErrInvalid
		}
	}
	after, err := c.fstat(handle)
	if err != nil {
		clear(data)
		return nil, attrs, err
	}
	if !after.Regular() || !sameAttrs(attrs, after) || uint64(len(data)) != after.Size {
		clear(data)
		return nil, after, ErrInvalid
	}
	if err := closeHandle(); err != nil {
		clear(data)
		return nil, after, err
	}
	return data, after, nil
}

// WriteExclusive creates a new private file, writes and fsyncs it, then returns
// attributes from the still-open handle. Existing paths are never overwritten.
func (c *Client) WriteExclusive(name string, data []byte) (Attrs, error) {
	if !validPath(name) || len(data) == 0 || len(data) > maxFileBytes {
		return Attrs{}, ErrInvalid
	}
	if err := c.lock(); err != nil {
		return Attrs{}, err
	}
	defer c.unlock()
	var fields encoder
	fields.text(name)
	fields.u32(openWrite | openCreate | openExclusive)
	fields.u32(attrPermissions)
	fields.u32(0600)
	typ, d, err := c.exchange(packetOpen, fields)
	if err != nil {
		return Attrs{}, err
	}
	if typ == packetStatus {
		if err := parseStatus(d); err != nil {
			return Attrs{}, err
		}
		return Attrs{}, ErrInvalid
	}
	if typ != packetHandle {
		return Attrs{}, ErrInvalid
	}
	handle, err := d.string(4096)
	if err != nil || len(handle) == 0 || d.done() != nil {
		return Attrs{}, ErrInvalid
	}
	closed := false
	closeHandle := func() error {
		if closed {
			return nil
		}
		closed = true
		return c.closeHandle(handle)
	}
	defer closeHandle()
	for offset := 0; offset < len(data); {
		end := min(offset+ioChunkBytes, len(data))
		var write encoder
		write.string(handle)
		write.u64(uint64(offset))
		write.string(data[offset:end])
		if err := c.statusRequest(packetWrite, write); err != nil {
			return Attrs{}, err
		}
		offset = end
	}
	var syncFields encoder
	syncFields.text("fsync@openssh.com")
	syncFields.string(handle)
	if err := c.statusRequest(packetFsync, syncFields); err != nil {
		return Attrs{}, err
	}
	attrs, err := c.fstat(handle)
	if err != nil {
		return Attrs{}, err
	}
	if !attrs.Regular() || attrs.Size != uint64(len(data)) || attrs.Permissions&07777 != 0600 {
		return Attrs{}, ErrInvalid
	}
	if err := closeHandle(); err != nil {
		return Attrs{}, err
	}
	return attrs, nil
}

// Rename uses the standard v3 operation, which must not replace an existing
// destination. No OpenSSH posix-rename extension is requested or used.
func (c *Client) Rename(oldName, newName string) error {
	if !validPath(oldName) || !validPath(newName) || oldName == newName {
		return ErrInvalid
	}
	if err := c.lock(); err != nil {
		return err
	}
	defer c.unlock()
	var fields encoder
	fields.text(oldName)
	fields.text(newName)
	return c.statusRequest(packetRename, fields)
}

func (c *Client) open(name string, flags uint32) ([]byte, Attrs, error) {
	var fields encoder
	fields.text(name)
	fields.u32(flags)
	fields.u32(0)
	typ, d, err := c.exchange(packetOpen, fields)
	if err != nil {
		return nil, Attrs{}, err
	}
	if typ == packetStatus {
		if err := parseStatus(d); err != nil {
			return nil, Attrs{}, err
		}
		return nil, Attrs{}, ErrInvalid
	}
	if typ != packetHandle {
		return nil, Attrs{}, ErrInvalid
	}
	handle, err := d.string(4096)
	if err != nil || len(handle) == 0 || d.done() != nil {
		return nil, Attrs{}, ErrInvalid
	}
	attrs, err := c.fstat(handle)
	if err != nil {
		_ = c.closeHandle(handle)
		return nil, Attrs{}, err
	}
	return handle, attrs, nil
}

func (c *Client) fstat(handle []byte) (Attrs, error) {
	var fields encoder
	fields.string(handle)
	typ, d, err := c.exchange(packetFstat, fields)
	if err != nil {
		return Attrs{}, err
	}
	if typ == packetStatus {
		if err := parseStatus(d); err != nil {
			return Attrs{}, err
		}
		return Attrs{}, ErrInvalid
	}
	if typ != packetAttrs {
		return Attrs{}, ErrInvalid
	}
	return parseAttrs(d)
}

func (c *Client) readChunk(handle []byte, offset uint64, size int) ([]byte, bool, error) {
	var fields encoder
	fields.string(handle)
	fields.u64(offset)
	fields.u32(uint32(size))
	typ, d, err := c.exchange(packetRead, fields)
	if err != nil {
		return nil, false, err
	}
	if typ == packetStatus {
		err := parseStatus(d)
		if errors.Is(err, errEOF) {
			return nil, true, nil
		}
		if err == nil {
			return nil, false, ErrInvalid
		}
		return nil, false, err
	}
	if typ != packetData {
		return nil, false, ErrInvalid
	}
	data, err := d.string(size)
	if err != nil || len(data) == 0 || d.done() != nil {
		return nil, false, ErrInvalid
	}
	return append([]byte(nil), data...), false, nil
}

func (c *Client) closeHandle(handle []byte) error {
	var fields encoder
	fields.string(handle)
	return c.statusRequest(packetClose, fields)
}

func (c *Client) statusRequest(typ byte, fields []byte) error {
	responseType, d, err := c.exchange(typ, fields)
	if err != nil {
		return err
	}
	if responseType != packetStatus {
		return ErrInvalid
	}
	if err := parseStatus(d); err != nil {
		return err
	}
	return nil
}
