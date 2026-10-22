package notify

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// base64URL is the URL-safe, unpadded encoding used by the callback data.
func base64URL(data []byte) string { return base64.RawURLEncoding.EncodeToString(data) }

func base64URLDecode(value string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(value) }

// FileChats persists the paired chat IDs as one small JSON document with
// owner-only permissions, replaced atomically. IDs re-reads the file when
// its modification time changed, so a hand edit (removing a chat) reaches
// a running process without a restart; pairing itself runs in the process
// (Pairer). A missing file simply means nothing is paired yet.
type FileChats struct {
	path string

	mu      sync.Mutex
	ids     []int64
	modTime time.Time
}

func OpenFileChats(path string) (*FileChats, error) {
	if path == "" {
		return nil, errors.New("notify: a chats file path is required")
	}
	chats := &FileChats{path: path}
	if err := chats.reload(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return chats, nil
}

// IDs returns the paired chat IDs, re-reading the file when it changed.
// A read or parse failure keeps the last good set: losing alerts is bad,
// but sending them to a wrong chat would be worse.
func (c *FileChats) IDs() []int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if info, err := os.Stat(c.path); err == nil && !info.ModTime().Equal(c.modTime) {
		if err := c.reloadLocked(); err != nil {
			slog.Default().Warn("paired chats file could not be re-read; keeping the last good set", "error", err)
		}
	}
	return slices.Clone(c.ids)
}

// Add records one chat ID, deduplicated, and replaces the file atomically.
func (c *FileChats) Add(id int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.reloadLocked(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !slices.Contains(c.ids, id) {
		c.ids = append(c.ids, id)
	}
	data, err := json.Marshal(c.ids)
	if err != nil {
		return fmt.Errorf("notify: encode chats: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return fmt.Errorf("notify: create chats directory: %w", err)
	}
	if err := writeAtomic(c.path, data); err != nil {
		return fmt.Errorf("notify: write chats file: %w", err)
	}
	return nil
}

func (c *FileChats) reload() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reloadLocked()
}

func (c *FileChats) reloadLocked() error {
	info, err := os.Stat(c.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			c.ids, c.modTime = nil, time.Time{}
		}
		return err
	}
	data, err := os.ReadFile(c.path)
	if err != nil {
		return err
	}
	var ids []int64
	if err := json.Unmarshal(data, &ids); err != nil {
		return fmt.Errorf("notify: parse chats file: %w", err)
	}
	c.ids, c.modTime = ids, info.ModTime()
	return nil
}

// writeAtomic replaces path through a temporary file; readers see either
// the old or the new document, never half of one.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".chats-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
