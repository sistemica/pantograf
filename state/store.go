// Package state is a small per-instance key-value store for connector
// state — offsets, cursors, last-seen IDs. NOT a general-purpose data
// store; not for caches of arbitrary application data.
//
// Each connector instance gets a Store scoped to it (the runtime injects
// one at Open time). Triggers and actions read/write small []byte values.
// Callers do their own JSON or other encoding — keeps the interface
// neutral.
//
// Backends live as sub-packages: state/fsstore (default), state/sqlitestore
// (later if it matters).
package state

import (
	"context"
	"errors"
)

// ErrNotFound is returned by backends that prefer it over (nil, false, nil).
// The (value, ok, err) triple in Get is the canonical "not found" signal;
// callers should NOT need to check for this error.
var ErrNotFound = errors.New("state: key not found")

// Store is per-instance: every key lives in the namespace of one instance.
// Keys are arbitrary strings; backends must accept any printable ASCII.
// Concurrency: implementations must be safe for concurrent use.
type Store interface {
	// Get returns (value, true, nil) when the key exists, (nil, false, nil)
	// when it does not, and (nil, false, err) on backend error.
	Get(ctx context.Context, key string) ([]byte, bool, error)

	// Put writes value. Implementations must make the write durable
	// before returning (atomic rename / fsync / equivalent).
	Put(ctx context.Context, key string, value []byte) error

	// Delete removes a key. Idempotent — deleting a missing key returns nil.
	Delete(ctx context.Context, key string) error

	// Keys returns all keys with the given prefix. Use "" for all.
	Keys(ctx context.Context, prefix string) ([]string, error)
}

// Manager hands out per-instance Stores. The runtime owns one Manager and
// gives `Manager.For(type, name)` to each Connector.Open as part of
// OpenOptions.
type Manager interface {
	For(typ, name string) Store
}
