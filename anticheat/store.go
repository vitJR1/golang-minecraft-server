package anticheat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"sync"
)

// MemoryStore keeps statuses and records in RAM. The zero value is not
// usable; build it with NewMemoryStore.
type MemoryStore struct {
	mu     sync.Mutex
	status map[string]Status
	log    []Record // append order
	nextID int64

	// save, when set, is called with the lock held after every change
	// (FileStore persists through it).
	save func() error
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{status: map[string]Status{}}
}

func (m *MemoryStore) Status(_ context.Context, uuid string) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status[uuid], nil
}

func (m *MemoryStore) Record(_ context.Context, rec Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	rec.ID = m.nextID
	m.log = append(m.log, rec)
	m.status[rec.UUID] = rec.After
	if m.save != nil {
		return m.save()
	}
	return nil
}

func (m *MemoryStore) History(_ context.Context, uuid string, limit int) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Record
	for i := len(m.log) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
		if m.log[i].UUID == uuid {
			out = append(out, m.log[i])
		}
	}
	return out, nil
}

func (m *MemoryStore) UUIDByName(_ context.Context, name string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.log) - 1; i >= 0; i-- {
		if strings.EqualFold(m.log[i].Name, name) {
			return m.log[i].UUID, true, nil
		}
	}
	return "", false, nil
}

// fileData is FileStore's on-disk JSON.
type fileData struct {
	Status  map[string]Status `json:"status"`
	Records []Record          `json:"records"`
}

// FileStore is a MemoryStore saved to a JSON file after every change — the
// STORAGE=memory backend, like ban.FileStore for bans.
type FileStore struct {
	*MemoryStore
	path string
}

// NewFileStore loads path (a missing file is an empty store).
func NewFileStore(path string) (*FileStore, error) {
	m := NewMemoryStore()
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("reading %s: %w", path, err)
	default:
		var d fileData
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
		if d.Status != nil {
			m.status = d.Status
		}
		sort.Slice(d.Records, func(i, j int) bool { return d.Records[i].ID < d.Records[j].ID })
		m.log = d.Records
		if n := len(d.Records); n > 0 {
			m.nextID = d.Records[n-1].ID
		}
	}
	f := &FileStore{MemoryStore: m, path: path}
	m.save = f.write
	return f, nil
}

// write rewrites the file atomically (temp file + rename). Called with the
// MemoryStore lock held.
func (f *FileStore) write() error {
	raw, err := json.MarshalIndent(fileData{Status: f.status, Records: f.log}, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding anticheat store: %w", err)
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, f.path); err != nil {
		return fmt.Errorf("replacing %s: %w", f.path, err)
	}
	return nil
}
