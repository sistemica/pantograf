// Package secrets manages the master key and field-level encryption used by
// the storage backends. Format: a NaCl secretbox sealed with a 32-byte key
// derived once and persisted at ~/.config/pgf/master.key (overridable via
// MW_MASTER_KEY env var holding base64).
//
// Sealed values are base64-encoded ciphertext prefixed with "sealed:" so a
// reader can tell at a glance and so the same field can hold legacy
// plaintext during migration.
package secrets

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/nacl/secretbox"
)

const (
	sealedPrefix = "sealed:"
	keyEnv       = "MW_MASTER_KEY"
	keyDirEnv    = "PGF_KEY_DIR"
)

// Vault holds the symmetric master key in memory.
type Vault struct {
	key [32]byte
}

// Load returns a Vault, creating the master key file on first run.
func Load() (*Vault, error) {
	// 1. Env override (base64-encoded 32 bytes).
	if raw := os.Getenv(keyEnv); raw != "" {
		k, err := decodeKey(raw)
		if err != nil {
			return nil, fmt.Errorf("secrets: $%s: %w", keyEnv, err)
		}
		return &Vault{key: k}, nil
	}

	// 2. File, default ~/.config/pgf/master.key.
	path, err := keyPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return create(path)
	case err != nil:
		return nil, fmt.Errorf("secrets: read %s: %w", path, err)
	}
	k, err := decodeKey(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, fmt.Errorf("secrets: parse %s: %w", path, err)
	}
	return &Vault{key: k}, nil
}

func keyPath() (string, error) {
	if v := os.Getenv(keyDirEnv); v != "" {
		return filepath.Join(v, "master.key"), nil
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "pgf", "master.key"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "pgf", "master.key"), nil
}

func create(path string) (*Vault, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	var k [32]byte
	if _, err := io.ReadFull(rand.Reader, k[:]); err != nil {
		return nil, err
	}
	encoded := base64.StdEncoding.EncodeToString(k[:])
	if err := os.WriteFile(path, []byte(encoded+"\n"), 0o600); err != nil {
		return nil, err
	}
	return &Vault{key: k}, nil
}

func decodeKey(s string) ([32]byte, error) {
	var k [32]byte
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return k, err
	}
	if len(raw) != 32 {
		return k, fmt.Errorf("master key must be 32 bytes, got %d", len(raw))
	}
	copy(k[:], raw)
	return k, nil
}

// IsSealed reports whether a string carries the sealed-value marker.
func IsSealed(s string) bool { return strings.HasPrefix(s, sealedPrefix) }

// Seal encrypts plaintext and returns the marked, base64-encoded form.
func (v *Vault) Seal(plaintext string) (string, error) {
	if v == nil {
		return "", errors.New("secrets: nil vault")
	}
	var nonce [24]byte
	if _, err := io.ReadFull(rand.Reader, nonce[:]); err != nil {
		return "", err
	}
	box := secretbox.Seal(nonce[:], []byte(plaintext), &nonce, &v.key)
	return sealedPrefix + base64.StdEncoding.EncodeToString(box), nil
}

// Open decrypts a sealed value. If s lacks the sealed prefix it is returned
// unchanged — supports a clean upgrade path from legacy plaintext.
func (v *Vault) Open(s string) (string, error) {
	if !IsSealed(s) {
		return s, nil
	}
	if v == nil {
		return "", errors.New("secrets: nil vault")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(s, sealedPrefix))
	if err != nil {
		return "", fmt.Errorf("secrets: base64: %w", err)
	}
	if len(raw) < 24 {
		return "", errors.New("secrets: sealed value too short")
	}
	var nonce [24]byte
	copy(nonce[:], raw[:24])
	out, ok := secretbox.Open(nil, raw[24:], &nonce, &v.key)
	if !ok {
		return "", errors.New("secrets: decryption failed (wrong key?)")
	}
	return string(out), nil
}
