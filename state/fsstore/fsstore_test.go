package fsstore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPutGetRoundTrip(t *testing.T) {
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := m.For("widget", "alpha")
	ctx := context.Background()

	if err := s.Put(ctx, "k", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	v, ok, err := s.Get(ctx, "k")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected ok=true after Put")
	}
	if string(v) != "hello" {
		t.Fatalf("got %q want %q", v, "hello")
	}
}

func TestGetMissingReturnsFalseNoError(t *testing.T) {
	m, _ := New(t.TempDir())
	s := m.For("widget", "alpha")
	v, ok, err := s.Get(context.Background(), "missing")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for missing key")
	}
	if v != nil {
		t.Fatalf("expected nil value, got %v", v)
	}
}

func TestPutOverwritesAtomically(t *testing.T) {
	m, _ := New(t.TempDir())
	s := m.For("widget", "alpha")
	ctx := context.Background()
	if err := s.Put(ctx, "k", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "k", []byte("v2")); err != nil {
		t.Fatal(err)
	}
	v, _, _ := s.Get(ctx, "k")
	if string(v) != "v2" {
		t.Fatalf("overwrite: got %q want v2", v)
	}
}

func TestDeleteRemovesKey(t *testing.T) {
	m, _ := New(t.TempDir())
	s := m.For("widget", "alpha")
	ctx := context.Background()
	_ = s.Put(ctx, "k", []byte("v"))
	if err := s.Delete(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	_, ok, _ := s.Get(ctx, "k")
	if ok {
		t.Fatal("expected key to be gone after Delete")
	}
	// Delete of missing key is a no-op.
	if err := s.Delete(ctx, "missing"); err != nil {
		t.Fatalf("delete of missing should not error, got %v", err)
	}
}

func TestKeysListsWithPrefix(t *testing.T) {
	m, _ := New(t.TempDir())
	s := m.For("widget", "alpha")
	ctx := context.Background()
	_ = s.Put(ctx, "alpha:1", []byte("a"))
	_ = s.Put(ctx, "alpha:2", []byte("b"))
	_ = s.Put(ctx, "beta:1", []byte("c"))

	all, err := s.Keys(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("Keys('') = %v, want 3 entries", all)
	}

	alpha, _ := s.Keys(ctx, "alpha:")
	if len(alpha) != 2 {
		t.Fatalf("Keys('alpha:') = %v, want 2", alpha)
	}
	for _, k := range alpha {
		if !strings.HasPrefix(k, "alpha:") {
			t.Errorf("key %q doesn't have prefix alpha:", k)
		}
	}
}

func TestInstancesAreIsolated(t *testing.T) {
	m, _ := New(t.TempDir())
	alpha := m.For("widget", "alpha")
	beta := m.For("widget", "beta")
	ctx := context.Background()

	_ = alpha.Put(ctx, "key", []byte("alpha-value"))
	_ = beta.Put(ctx, "key", []byte("beta-value"))

	va, _, _ := alpha.Get(ctx, "key")
	vb, _, _ := beta.Get(ctx, "key")
	if string(va) != "alpha-value" || string(vb) != "beta-value" {
		t.Fatalf("instance isolation broken: alpha=%q beta=%q", va, vb)
	}
}

func TestFilePermissions(t *testing.T) {
	root := t.TempDir()
	m, _ := New(root)
	s := m.For("widget", "alpha")
	if err := s.Put(context.Background(), "k", []byte("v")); err != nil {
		t.Fatal(err)
	}
	// Walk under the instance dir, every file should be 0600.
	dir := filepath.Join(root, "widget", "alpha")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one persisted file")
	}
	for _, e := range entries {
		info, _ := e.Info()
		if info.Mode().Perm()&0o077 != 0 {
			t.Errorf("file %s is group/other-readable: %o", e.Name(), info.Mode().Perm())
		}
	}
}

func TestEnvOverridesRoot(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PGF_STATE_DIR", tmp)
	m, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	if m.Root() != tmp {
		t.Fatalf("expected root %q, got %q", tmp, m.Root())
	}
}
