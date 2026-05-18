package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sistemica/pantograf/connector"
)

func TestExtractPaths(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want []string
	}{
		{"nil", nil, nil},
		{"empty string", "", nil},
		{"single string", "/a/b", []string{"/a/b"}},
		{"string slice", []string{"/a", "/b"}, []string{"/a", "/b"}},
		{"any slice with strings", []any{"/a", "/b"}, []string{"/a", "/b"}},
		{"any slice mixed types skips non-strings", []any{"/a", 42, "/b"}, []string{"/a", "/b"}},
		{"any slice with empty string skipped", []any{"/a", "", "/b"}, []string{"/a", "/b"}},
		{"unsupported type returns nil", 42, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractPaths(c.in)
			if !equal(got, c.want) {
				t.Fatalf("extractPaths(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestPathUnderAny(t *testing.T) {
	// Set up a temp tree:
	//   <tmp>/allowed/ok
	//   <tmp>/secret/key
	tmp := t.TempDir()
	allowed := filepath.Join(tmp, "allowed")
	secret := filepath.Join(tmp, "secret")
	for _, d := range []string{allowed, secret} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(allowed, "ok"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secret, "key"), []byte("sekrit"), 0o600); err != nil {
		t.Fatal(err)
	}

	roots := []string{allowed}

	cases := []struct {
		name    string
		path    string
		allowed bool
	}{
		{"file inside allowed", filepath.Join(allowed, "ok"), true},
		{"non-existent file in allowed dir", filepath.Join(allowed, "future.pdf"), true},
		{"the allowed dir itself", allowed, true},
		{"file in sibling dir is rejected", filepath.Join(secret, "key"), false},
		{"path that looks like prefix is rejected", allowed + "extra/file", false},
		{"traversal via .. is resolved + rejected", filepath.Join(allowed, "..", "secret", "key"), false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := pathUnderAny(c.path, roots)
			if got != c.allowed {
				t.Fatalf("pathUnderAny(%q, %v) = %v, want %v", c.path, roots, got, c.allowed)
			}
		})
	}

	t.Run("symlink resolved before check", func(t *testing.T) {
		// Symlink inside allowed/ pointing to secret/key. With resolution
		// turned on the path must be rejected (it really IS in /secret).
		link := filepath.Join(allowed, "sneaky")
		_ = os.Remove(link)
		if err := os.Symlink(filepath.Join(secret, "key"), link); err != nil {
			t.Skipf("symlink not supported: %v", err)
		}
		if pathUnderAny(link, roots) {
			t.Fatal("symlink to secret should not be allowed (resolved target is in /secret)")
		}
	})
}

func TestValidatePaths(t *testing.T) {
	schema := connector.Schema{Fields: []connector.FieldSpec{
		{Name: "out", Kind: connector.FieldString, IsPath: true},
		{Name: "attachments", Kind: connector.FieldStringList, IsPath: true},
		{Name: "topic", Kind: connector.FieldString}, // not a path
	}}

	tmp := t.TempDir()
	t.Run("no allow-list → no enforcement", func(t *testing.T) {
		os.Unsetenv("PGF_ALLOWED_PATHS")
		err := validatePaths(schema, connector.Values{
			"out":         "/etc/shadow",
			"attachments": []string{"/root/.ssh/id_rsa"},
		}, "test")
		if err != nil {
			t.Fatalf("expected no error when PGF_ALLOWED_PATHS unset, got %v", err)
		}
	})

	t.Run("allow-list set → enforcement", func(t *testing.T) {
		os.Setenv("PGF_ALLOWED_PATHS", tmp)
		defer os.Unsetenv("PGF_ALLOWED_PATHS")

		// Inside the allowed dir → ok.
		err := validatePaths(schema, connector.Values{
			"out": filepath.Join(tmp, "report.pdf"),
		}, "test")
		if err != nil {
			t.Fatalf("expected nil, got %v", err)
		}

		// Outside → rejected.
		err = validatePaths(schema, connector.Values{
			"out": "/etc/passwd",
		}, "test")
		if err == nil {
			t.Fatal("expected error for /etc/passwd, got nil")
		}
		if !strings.Contains(err.Error(), "/etc/passwd") {
			t.Fatalf("error should mention the offending path; got %v", err)
		}

		// Non-path field with a path-shaped value is ignored.
		err = validatePaths(schema, connector.Values{
			"topic": "/etc/shadow",
			"out":   filepath.Join(tmp, "ok.pdf"),
		}, "test")
		if err != nil {
			t.Fatalf("non-path field should not be validated, got %v", err)
		}

		// One bad item in a list rejects the whole call.
		err = validatePaths(schema, connector.Values{
			"attachments": []string{filepath.Join(tmp, "ok.pdf"), "/etc/passwd"},
		}, "test")
		if err == nil {
			t.Fatal("expected error for /etc/passwd in attachments list, got nil")
		}
	})

	t.Run("ShowWhen=false skips inactive path field", func(t *testing.T) {
		os.Setenv("PGF_ALLOWED_PATHS", tmp)
		defer os.Unsetenv("PGF_ALLOWED_PATHS")
		// A path field that the discriminator switches off should NOT be
		// validated even when its value would otherwise be rejected. This
		// matches the wizard's "don't prompt for it" decision — if we
		// won't actually use the value, we shouldn't gate on it either.
		conditionalSchema := connector.Schema{Fields: []connector.FieldSpec{
			{Name: "driver", Kind: connector.FieldString},
			{Name: "log_file", Kind: connector.FieldString, IsPath: true,
				ShowWhen: func(v connector.Values) bool { return v.String("driver") == "file" }},
		}}
		err := validatePaths(conditionalSchema, connector.Values{
			"driver":   "stdout",
			"log_file": "/etc/passwd", // would be rejected if active
		}, "test")
		if err != nil {
			t.Fatalf("ShowWhen=false field should be skipped; got %v", err)
		}
		// Same schema, discriminator flipped → field active → reject.
		err = validatePaths(conditionalSchema, connector.Values{
			"driver":   "file",
			"log_file": "/etc/passwd",
		}, "test")
		if err == nil {
			t.Fatal("ShowWhen=true field should be validated and rejected")
		}
	})

	t.Run("error message includes the field name and the allow-list", func(t *testing.T) {
		os.Setenv("PGF_ALLOWED_PATHS", tmp)
		defer os.Unsetenv("PGF_ALLOWED_PATHS")
		err := validatePaths(schema, connector.Values{
			"out": "/etc/passwd",
		}, "myaction")
		if err == nil {
			t.Fatal("expected error")
		}
		msg := err.Error()
		for _, want := range []string{"myaction", `"out"`, `"/etc/passwd"`, tmp} {
			if !strings.Contains(msg, want) {
				t.Errorf("error message missing %q: %s", want, msg)
			}
		}
	})
}

func TestAllowedPaths(t *testing.T) {
	t.Run("empty env returns nil", func(t *testing.T) {
		os.Unsetenv("PGF_ALLOWED_PATHS")
		if got := allowedPaths(); got != nil {
			t.Fatalf("expected nil for unset env, got %v", got)
		}
	})

	t.Run("colon-separated, abs-resolved, empties skipped", func(t *testing.T) {
		os.Setenv("PGF_ALLOWED_PATHS", "/tmp:/var/lib/pgf::/etc")
		defer os.Unsetenv("PGF_ALLOWED_PATHS")
		got := allowedPaths()
		want := []string{"/tmp", "/var/lib/pgf", "/etc"}
		if len(got) != len(want) {
			t.Fatalf("len(allowed)=%d, want %d (got %v)", len(got), len(want), got)
		}
		for i, w := range want {
			if got[i] != w {
				t.Fatalf("allowedPaths()[%d] = %q, want %q", i, got[i], w)
			}
		}
	})
}

// helper
func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
