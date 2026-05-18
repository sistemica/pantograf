package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadVaultFromDir is a test helper: creates / loads a Vault at the
// given dir. Uses the MW_KEY_DIR override (the existing env-var path).
func loadVaultFromDir(t *testing.T, dir string) *Vault {
	t.Helper()
	t.Setenv("PGF_KEY_DIR", dir)
	v, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return v
}

func TestVaultRoundTrip(t *testing.T) {
	v := loadVaultFromDir(t, t.TempDir())

	plaintext := "hunter2"
	sealed, err := v.Seal(plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if !strings.HasPrefix(sealed, "sealed:") {
		t.Fatalf("expected 'sealed:' prefix, got %q", sealed)
	}
	if !IsSealed(sealed) {
		t.Fatal("IsSealed should be true")
	}

	opened, err := v.Open(sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if opened != plaintext {
		t.Fatalf("round-trip mismatch: got %q want %q", opened, plaintext)
	}
}

func TestVaultOpenPassesPlaintext(t *testing.T) {
	v := loadVaultFromDir(t, t.TempDir())
	out, err := v.Open("not-sealed")
	if err != nil {
		t.Fatalf("Open(plain) should not error, got %v", err)
	}
	if out != "not-sealed" {
		t.Fatalf("plain passthrough got %q", out)
	}
}

func TestVaultOpenWithWrongKeyFails(t *testing.T) {
	d1 := t.TempDir()
	v1 := loadVaultFromDir(t, d1)
	sealed, _ := v1.Seal("secret")

	d2 := t.TempDir()
	v2 := loadVaultFromDir(t, d2)
	_, err := v2.Open(sealed)
	if err == nil {
		t.Fatal("Open with a different key should fail")
	}
	if !strings.Contains(err.Error(), "decryption") {
		t.Fatalf("expected 'decryption' in error, got %v", err)
	}
}

func TestVaultPersistsKey(t *testing.T) {
	d := t.TempDir()
	v1 := loadVaultFromDir(t, d)
	sealed, _ := v1.Seal("persistent")

	// Load again from the same dir — should pick up the same key.
	v2 := loadVaultFromDir(t, d)
	got, err := v2.Open(sealed)
	if err != nil {
		t.Fatalf("Open after reload: %v", err)
	}
	if got != "persistent" {
		t.Fatalf("got %q, want 'persistent'", got)
	}

	// Key file should exist with 0600.
	info, err := os.Stat(filepath.Join(d, "master.key"))
	if err != nil {
		t.Fatalf("master.key stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("master.key perm = %o, want 0600", info.Mode().Perm())
	}
}

func TestVaultEnvOverride(t *testing.T) {
	// PGF_MASTER_KEY env should win over disk.
	keyBytes := make([]byte, 32)
	for i := range keyBytes {
		keyBytes[i] = byte(i)
	}
	t.Setenv("PGF_KEY_DIR", t.TempDir()) // would otherwise create one
	// Use base64 of the 32 bytes.
	const b64 = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
	t.Setenv("PGF_MASTER_KEY", b64)

	v, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Quick smoke: round-trip works.
	sealed, _ := v.Seal("hello")
	got, err := v.Open(sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got != "hello" {
		t.Fatalf("env-key round-trip got %q", got)
	}
}

func TestVaultMalformedSealedRejected(t *testing.T) {
	v := loadVaultFromDir(t, t.TempDir())
	_, err := v.Open("sealed:not-base64!!!")
	if err == nil {
		t.Fatal("expected error on malformed sealed value")
	}
}

func TestVaultShortSealedRejected(t *testing.T) {
	v := loadVaultFromDir(t, t.TempDir())
	// "sealed:" + 4 base64 chars = ciphertext less than the 24-byte nonce
	_, err := v.Open("sealed:AAAA")
	if err == nil {
		t.Fatal("expected error on too-short sealed value")
	}
}
