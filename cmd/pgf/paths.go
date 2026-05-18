package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sistemica/pantograf/connector"
)

// allowedPaths reads PGF_ALLOWED_PATHS as a colon-separated list. Empty
// (or unset) means no enforcement — the same loose behaviour pgf had
// before path-whitelist support landed. Production deployments MUST
// set this; see SECURITY.md for the threat model.
func allowedPaths() []string {
	v := strings.TrimSpace(os.Getenv("PGF_ALLOWED_PATHS"))
	if v == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(v, ":") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		abs, err := filepath.Abs(p)
		if err == nil {
			out = append(out, abs)
		}
	}
	return out
}

// pathUnderAny reports whether `p` resolves to a location inside one of
// the allowed roots. Resolves to absolute first; resolves symlinks if
// the path exists (prevents symlink-out-of-jail tricks). For paths that
// don't exist yet (e.g. an output target), falls back to the cleaned
// absolute form.
func pathUnderAny(p string, allowed []string) bool {
	abs, err := filepath.Abs(p)
	if err != nil {
		return false
	}
	check := abs
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		check = real
	}
	for _, root := range allowed {
		// Compare directory boundaries to avoid /foo/bar matching /foo/barbaz.
		rootSep := strings.TrimRight(root, string(filepath.Separator)) + string(filepath.Separator)
		checkSep := check + string(filepath.Separator)
		if checkSep == rootSep || strings.HasPrefix(check, rootSep) {
			return true
		}
		if check == strings.TrimRight(root, string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// extractPaths pulls path values out of a Values entry. Handles string,
// []string, and []any (slice produced by JSON unmarshal).
func extractPaths(raw any) []string {
	switch v := raw.(type) {
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// validatePaths checks every IsPath field in the schema against the
// allow-list. Returns a non-nil error on the first violation. A nil
// allow-list (PGF_ALLOWED_PATHS unset) skips enforcement entirely.
func validatePaths(schema connector.Schema, values connector.Values, label string) error {
	allowed := allowedPaths()
	if len(allowed) == 0 {
		return nil
	}
	for _, f := range schema.Fields {
		if !f.IsPath {
			continue
		}
		// Skip path checks for fields the user's other choices have
		// made irrelevant (e.g. token_path when auth_mode=basic).
		if !f.IsActive(values) {
			continue
		}
		raw, ok := values[f.Name]
		if !ok {
			continue
		}
		for _, p := range extractPaths(raw) {
			if !pathUnderAny(p, allowed) {
				return fmt.Errorf("%s: field %q value %q is outside PGF_ALLOWED_PATHS (%s)",
					label, f.Name, p, strings.Join(allowed, ":"))
			}
		}
	}
	return nil
}
