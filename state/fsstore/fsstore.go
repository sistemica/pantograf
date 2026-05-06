// Package fsstore is the filesystem-backed default state Store.
// Layout: <root>/<type>/<name>/<keyhash>.bin (the original key is stored
// alongside in a sidecar so Keys() can list humans-readable names without
// hashing collisions).
//
// Atomic writes via tmp + rename. Mode 0o600 on every value file.
package fsstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sistemica/pantograf/state"
)

// Manager owns the root directory and creates per-instance Stores on demand.
type Manager struct {
	root string
	mu   sync.Mutex
}

// New returns a Manager rooted at root. Pass "" to use the default
// ($XDG_STATE_HOME/mw/state, falling back to ~/.local/state/mw/state).
// Override with MW_STATE_DIR.
func New(root string) (*Manager, error) {
	if root == "" {
		var err error
		root, err = defaultRoot()
		if err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	return &Manager{root: root}, nil
}

// Root returns the absolute root directory.
func (m *Manager) Root() string { return m.root }

// For returns a per-instance Store. Cheap — no I/O.
func (m *Manager) For(typ, name string) state.Store {
	return &store{root: filepath.Join(m.root, typ, name)}
}

func defaultRoot() (string, error) {
	if v := os.Getenv("MW_STATE_DIR"); v != "" {
		return v, nil
	}
	if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		return filepath.Join(v, "mw", "state"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "mw", "state"), nil
}

// ─────────────────────────────────────────────────────────────────────────

// store is one instance's filesystem namespace.
type store struct {
	root string
	mu   sync.Mutex
}

// keyFile maps a logical key to a deterministic safe filename.
// Hash to avoid filesystem-name conflicts; keep the readable key in a
// sidecar JSON alongside so Keys() can recover the original.
func (s *store) keyFile(key string) (valuePath, sidecarPath string) {
	sum := sha256.Sum256([]byte(key))
	h := hex.EncodeToString(sum[:])
	return filepath.Join(s.root, h+".bin"), filepath.Join(s.root, h+".key")
}

func (s *store) ensureRoot() error { return os.MkdirAll(s.root, 0o700) }

func (s *store) Get(_ context.Context, key string) ([]byte, bool, error) {
	vpath, _ := s.keyFile(key)
	data, err := os.ReadFile(vpath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, false, nil
	case err != nil:
		return nil, false, err
	}
	return data, true, nil
}

func (s *store) Put(_ context.Context, key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureRoot(); err != nil {
		return err
	}
	vpath, kpath := s.keyFile(key)
	tmp := vpath + ".tmp"
	if err := os.WriteFile(tmp, value, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, vpath); err != nil {
		return err
	}
	// Sidecar carries the original key; cheap to overwrite each Put.
	return os.WriteFile(kpath, []byte(key), 0o600)
}

func (s *store) Delete(_ context.Context, key string) error {
	vpath, kpath := s.keyFile(key)
	for _, p := range []string{vpath, kpath} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *store) Keys(_ context.Context, prefix string) ([]string, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".key") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.root, e.Name()))
		if err != nil {
			continue
		}
		k := string(data)
		if prefix == "" || strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out, nil
}
