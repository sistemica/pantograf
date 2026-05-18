package yamlstore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sistemica/pantograf/connector"
	"github.com/sistemica/pantograf/storage"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPutGetRoundTrip(t *testing.T) {
	s := newStore(t)
	cred := connector.Credential{
		Type:   "email",
		Name:   "work",
		Values: connector.Values{"username": "me@example.com", "password": "secret"},
	}
	if err := s.Put(cred); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("email", "work")
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != "email" || got.Name != "work" {
		t.Errorf("identity wrong: %+v", got)
	}
	if got.Values.String("username") != "me@example.com" {
		t.Errorf("username: %q", got.Values.String("username"))
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt should be stamped by Put")
	}
	if got.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should be stamped by Put")
	}
}

func TestPutRejectsEmptyKey(t *testing.T) {
	s := newStore(t)
	if err := s.Put(connector.Credential{Type: "", Name: "x"}); err == nil {
		t.Error("Put with empty Type should error")
	}
	if err := s.Put(connector.Credential{Type: "x", Name: ""}); err == nil {
		t.Error("Put with empty Name should error")
	}
}

func TestGetMissingReturnsErrNotFound(t *testing.T) {
	s := newStore(t)
	_, err := s.Get("nope", "missing")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestDeleteRemovesAndIdempotency(t *testing.T) {
	s := newStore(t)
	cred := connector.Credential{Type: "email", Name: "work", Values: connector.Values{"k": "v"}}
	_ = s.Put(cred)
	if err := s.Delete("email", "work"); err != nil {
		t.Fatal(err)
	}
	_, err := s.Get("email", "work")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("expected not found after delete")
	}
	// Second delete should return ErrNotFound, not crash.
	if err := s.Delete("email", "work"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected ErrNotFound on second delete, got %v", err)
	}
}

func TestListSortsAndExcludesDirs(t *testing.T) {
	s := newStore(t)
	creds := []connector.Credential{
		{Type: "email", Name: "personal", Values: connector.Values{"k": "v"}},
		{Type: "email", Name: "work", Values: connector.Values{"k": "v"}},
		{Type: "telegram", Name: "bot1", Values: connector.Values{"k": "v"}},
	}
	for _, c := range creds {
		_ = s.Put(c)
	}
	refs, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 3 {
		t.Fatalf("List = %v (want 3)", refs)
	}
	// Sort: by Type then by Name.
	want := []storage.InstanceRef{
		{Type: "email", Name: "personal"},
		{Type: "email", Name: "work"},
		{Type: "telegram", Name: "bot1"},
	}
	for i, r := range refs {
		if r != want[i] {
			t.Errorf("[%d] = %+v, want %+v", i, r, want[i])
		}
	}
}

func TestListEmptyRoot(t *testing.T) {
	root := t.TempDir()
	// Don't even create the dir via New — point to a non-existent path
	// and exercise the ENOENT branch in List.
	s := &Store{root: filepath.Join(root, "ghost")}
	refs, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 0 {
		t.Fatalf("expected no refs from non-existent root, got %v", refs)
	}
}

func TestFilePermissions(t *testing.T) {
	root := t.TempDir()
	s, _ := New(root)
	cred := connector.Credential{Type: "x", Name: "y", Values: connector.Values{"k": "v"}}
	if err := s.Put(cred); err != nil {
		t.Fatal(err)
	}
	// The yaml file must be 0600 — it holds (currently plaintext) creds.
	info, err := os.Stat(filepath.Join(root, "x", "y.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("yaml file perm = %o, want 0600", info.Mode().Perm())
	}
	// And the type dir must be 0700.
	dinfo, err := os.Stat(filepath.Join(root, "x"))
	if err != nil {
		t.Fatal(err)
	}
	if dinfo.Mode().Perm() != 0o700 {
		t.Errorf("type dir perm = %o, want 0700", dinfo.Mode().Perm())
	}
}

func TestPutAtomicOverwrite(t *testing.T) {
	s := newStore(t)
	c1 := connector.Credential{Type: "x", Name: "y", Values: connector.Values{"v": "1"}}
	c2 := connector.Credential{Type: "x", Name: "y", Values: connector.Values{"v": "2"}}
	_ = s.Put(c1)
	_ = s.Put(c2)
	got, _ := s.Get("x", "y")
	if got.Values.String("v") != "2" {
		t.Fatalf("expected overwritten value, got %q", got.Values.String("v"))
	}
	// No leftover .tmp file.
	matches, _ := filepath.Glob(filepath.Join(s.root, "x", "*.tmp"))
	if len(matches) > 0 {
		t.Fatalf("expected no .tmp leftover, got %v", matches)
	}
}

func TestEnvOverridesDefaultRoot(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PGF_STORE_DIR", tmp)
	s, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	if s.Root() != tmp {
		t.Fatalf("Root = %q, want %q", s.Root(), tmp)
	}
}

func TestPutPreservesCreatedAtOnUpdate(t *testing.T) {
	s := newStore(t)
	_ = s.Put(connector.Credential{Type: "x", Name: "y", Values: connector.Values{"v": "1"}})
	first, _ := s.Get("x", "y")
	firstCreated := first.CreatedAt
	// Second Put: explicitly include the prior CreatedAt to simulate an
	// update flow. The store should leave CreatedAt intact and only bump
	// UpdatedAt.
	first.Values["v"] = "2"
	if err := s.Put(first); err != nil {
		t.Fatal(err)
	}
	second, _ := s.Get("x", "y")
	if !second.CreatedAt.Equal(firstCreated) {
		t.Errorf("CreatedAt should be preserved: was %v, now %v", firstCreated, second.CreatedAt)
	}
	if !second.UpdatedAt.After(firstCreated) && !second.UpdatedAt.Equal(firstCreated) {
		t.Errorf("UpdatedAt should advance: %v vs %v", second.UpdatedAt, firstCreated)
	}
}
