package internal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	shipapi "github.com/enbility/ship-go/api"
)

// FileRingBuffer implements shipapi.RingBufferPersistence by serializing the
// SHIP Pairing ring buffer to a JSON file on disk.
//
// The ring buffer is required whenever PairingConfig.Mode is Listener or Both
// (see api/configuration.go validation). It provides replay protection per
// SHIP Pairing Service specification section 11, so the data must survive
// process restarts.
type FileRingBuffer struct {
	mu       sync.Mutex
	filename string
}

// NewFileRingBuffer returns a ring-buffer persistence backed by the given file.
// The file is created on first Save if it does not exist.
func NewFileRingBuffer(filename string) *FileRingBuffer {
	return &FileRingBuffer{filename: filename}
}

type ringBufferState struct {
	Entries   []shipapi.DigestEntry `json:"entries"`
	NextIndex int                   `json:"nextIndex"`
}

// LoadRingBuffer restores previously persisted ring-buffer state.
// Missing file is not an error: we return a fresh 100-slot buffer
// and let the library manage it.
func (r *FileRingBuffer) LoadRingBuffer() ([]shipapi.DigestEntry, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	data, err := os.ReadFile(r.filename)
	if os.IsNotExist(err) {
		AppLog.Infof("ring buffer: no file at %s, starting fresh", r.filename)
		return make([]shipapi.DigestEntry, 100), 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("ring buffer read %s: %w", r.filename, err)
	}

	var state ringBufferState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, 0, fmt.Errorf("ring buffer parse %s: %w", r.filename, err)
	}
	if len(state.Entries) == 0 {
		state.Entries = make([]shipapi.DigestEntry, 100)
	}
	AppLog.Debugf("ring buffer: loaded %d slots, nextIndex=%d", len(state.Entries), state.NextIndex)
	return state.Entries, state.NextIndex, nil
}

// SaveRingBuffer atomically persists the current ring-buffer state.
// Uses a temp-file + rename to avoid leaving a truncated file on crash.
func (r *FileRingBuffer) SaveRingBuffer(entries []shipapi.DigestEntry, nextIndex int) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(r.filename), 0o700); err != nil {
		return fmt.Errorf("ring buffer mkdir: %w", err)
	}

	state := ringBufferState{Entries: entries, NextIndex: nextIndex}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("ring buffer marshal: %w", err)
	}

	tmp := r.filename + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("ring buffer write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, r.filename); err != nil {
		return fmt.Errorf("ring buffer rename %s: %w", r.filename, err)
	}
	AppLog.Debugf("ring buffer: saved %d slots, nextIndex=%d", len(entries), nextIndex)
	return nil
}
