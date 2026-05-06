// Package storage abstracts persistence of connector instances (credentials
// + metadata). Backends live in subpackages: yamlstore, sqlitestore, ...
package storage

import (
	"errors"

	"github.com/sistemica/pantograf/connector"
)

// ErrNotFound is returned by Get/Delete when no instance matches.
var ErrNotFound = errors.New("instance not found")

// InstanceRef is a lightweight pointer to a stored instance, returned by
// List(). Use Get() to load the full Credential.
type InstanceRef struct {
	Type string
	Name string
}

// Store persists named credentials. Implementations must be safe for
// concurrent calls — the CLI is single-process today but a future HTTP
// server will share the same Store.
type Store interface {
	Put(cred connector.Credential) error
	Get(typ, name string) (connector.Credential, error)
	List() ([]InstanceRef, error)
	Delete(typ, name string) error
}
