package irc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"

	"github.com/Someblueman/airc/internal/protocol"
)

// PasteInfo describes a stored paste: its content-derived ID, name,
// language, size and line count.
type PasteInfo = protocol.PasteInfo

// PasteLimit is the largest paste the connected daemon accepts, from its
// PASTE_BYTES token.
func (c *Client) PasteLimit() int {
	n, err := strconv.Atoi(c.Features()["PASTE_BYTES"])
	if err != nil || n <= 0 {
		return 64 << 10
	}
	return n
}

// SendPaste publishes message, whose body should end with the paste's
// reference line (protocol.PasteMessageBody), sharing the paste pasteID.
func (c *Client) SendPaste(target, message, requestID, pasteID string) error {
	return c.SendWith(target, message, SendOptions{RequestID: requestID, Paste: pasteID})
}

// ReplyPaste replies to parent sharing the paste pasteID.
func (c *Client) ReplyPaste(parent, message, requestID, pasteID string) error {
	return c.ReplyWith(parent, message, SendOptions{RequestID: requestID, Paste: pasteID})
}

// UploadPaste uploads content as a paste named name in language and returns
// the paste as stored; the same content uploaded again is the same paste.
// other sees unrelated events meanwhile.
func UploadPaste(ctx context.Context, c *Client, name, language string, content []byte, other func(Event)) (PasteInfo, error) {
	if limit := c.PasteLimit(); len(content) > limit {
		return PasteInfo{}, fmt.Errorf("the paste is %d bytes; this aircd accepts at most %d", len(content), limit)
	}
	if len(content) == 0 || !protocol.ValidPasteText(content) {
		return PasteInfo{}, errors.New("a paste is nonempty UTF-8 text without NUL bytes")
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return PasteInfo{}, err
	}
	token := hex.EncodeToString(random[:])
	begin := ChatRequest{Action: "paste-begin", Token: token, Name: name, Language: language, Size: len(content), Lines: protocol.PasteLines(content)}
	if _, err := RequestChat(ctx, c, begin, other); err != nil {
		return PasteInfo{}, err
	}
	for seq, offset := 0, 0; offset < len(content); seq, offset = seq+1, offset+protocol.PasteChunkBytes {
		end := min(offset+protocol.PasteChunkBytes, len(content))
		chunk := ChatRequest{Action: "paste-chunk", Token: token, Seq: seq, Data: base64.StdEncoding.EncodeToString(content[offset:end])}
		if _, err := RequestChat(ctx, c, chunk, other); err != nil {
			return PasteInfo{}, err
		}
	}
	entries, err := RequestChat(ctx, c, ChatRequest{Action: "paste-end", Token: token}, other)
	if err != nil {
		return PasteInfo{}, err
	}
	if len(entries) != 1 || entries[0].Paste == nil {
		return PasteInfo{}, errors.New("the server did not describe the paste")
	}
	return *entries[0].Paste, nil
}

// FetchPaste reads paste id: its description, as the server holds it, and
// its content. other sees unrelated events meanwhile.
func FetchPaste(ctx context.Context, c *Client, id string, other func(Event)) (ChatEntry, []byte, error) {
	if !protocol.ValidMessageID(id) {
		return ChatEntry{}, nil, errors.New("a paste ID is 32 hex digits")
	}
	var meta ChatEntry
	var content []byte
	limit := c.PasteLimit()
	for offset := 0; ; {
		entries, err := RequestChat(ctx, c, ChatRequest{Action: "paste-get", ID: id, Offset: offset}, other)
		if err != nil {
			return ChatEntry{}, nil, err
		}
		if len(entries) == 0 || entries[0].Paste == nil {
			return ChatEntry{}, nil, errors.New("the server did not describe the paste")
		}
		if offset == 0 {
			meta = entries[0]
			meta.Data, meta.Offset = "", 0
		}
		for _, e := range entries {
			if e.Offset != len(content) {
				return ChatEntry{}, nil, fmt.Errorf("paste data out of order at %d", e.Offset)
			}
			data, err := base64.StdEncoding.DecodeString(e.Data)
			if err != nil || len(content)+len(data) > max(limit, meta.Paste.Size) {
				return ChatEntry{}, nil, errors.New("invalid paste data")
			}
			content = append(content, data...)
		}
		if len(content) >= meta.Paste.Size {
			break
		}
		if len(entries[len(entries)-1].Data) == 0 {
			return ChatEntry{}, nil, errors.New("the paste ended early")
		}
		offset = len(content)
	}
	if len(content) != meta.Paste.Size || protocol.PasteID(content) != id {
		return ChatEntry{}, nil, errors.New("paste content does not match its ID")
	}
	return meta, content, nil
}
