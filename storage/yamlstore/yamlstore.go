// Package yamlstore is a filesystem-backed Store: one YAML file per instance
// at <root>/<type>/<name>.yaml. Default root is ~/.config/pgf/instances.
//
// Secret-field values are written in plaintext for now. A follow-up will
// transparently encrypt fields whose Schema marks them FieldSecret.
package yamlstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sistemica/pantograf/connector"
	"github.com/sistemica/pantograf/storage"

	"gopkg.in/yaml.v3"
)

type Store struct {
	root string
	mu   sync.Mutex // serialises writes; reads are stat+open and OS-safe
}

// New creates a YAML-backed Store. Pass "" to use the default location.
func New(root string) (*Store, error) {
	if root == "" {
		var err error
		root, err = defaultRoot()
		if err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("yamlstore: mkdir %s: %w", root, err)
	}
	return &Store{root: root}, nil
}

func defaultRoot() (string, error) {
	if v := os.Getenv("PGF_STORE_DIR"); v != "" {
		return v, nil
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "pgf", "instances"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "pgf", "instances"), nil
}

// Root returns the absolute storage root. Useful for the CLI to print.
func (s *Store) Root() string { return s.root }

func (s *Store) path(typ, name string) string {
	return filepath.Join(s.root, typ, name+".yaml")
}

func (s *Store) Put(cred connector.Credential) error {
	if cred.Type == "" || cred.Name == "" {
		return errors.New("yamlstore: Put requires Type and Name")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	if cred.CreatedAt.IsZero() {
		cred.CreatedAt = now
	}
	cred.UpdatedAt = now

	dir := filepath.Join(s.root, cred.Type)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	data, err := yaml.Marshal(cred)
	if err != nil {
		return err
	}

	dst := s.path(cred.Type, cred.Name)
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func (s *Store) Get(typ, name string) (connector.Credential, error) {
	data, err := os.ReadFile(s.path(typ, name))
	if errors.Is(err, os.ErrNotExist) {
		return connector.Credential{}, storage.ErrNotFound
	}
	if err != nil {
		return connector.Credential{}, err
	}
	var c connector.Credential
	if err := yaml.Unmarshal(data, &c); err != nil {
		return connector.Credential{}, fmt.Errorf("yamlstore: parse %s/%s: %w", typ, name, err)
	}
	return c, nil
}

func (s *Store) Delete(typ, name string) error {
	err := os.Remove(s.path(typ, name))
	if errors.Is(err, os.ErrNotExist) {
		return storage.ErrNotFound
	}
	// Best-effort: prune the type directory if empty.
	_ = os.Remove(filepath.Join(s.root, typ))
	return err
}

func (s *Store) List() ([]storage.InstanceRef, error) {
	var refs []storage.InstanceRef
	types, err := os.ReadDir(s.root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	for _, t := range types {
		if !t.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(s.root, t.Name()))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
				continue
			}
			refs = append(refs, storage.InstanceRef{
				Type: t.Name(),
				Name: strings.TrimSuffix(e.Name(), ".yaml"),
			})
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Type != refs[j].Type {
			return refs[i].Type < refs[j].Type
		}
		return refs[i].Name < refs[j].Name
	})
	return refs, nil
}
