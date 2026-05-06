package secrets

import (
	"fmt"

	"github.com/sistemica/pantograf/connector"
)

// SealValues encrypts every Schema field of kind FieldSecret in v, leaving
// other fields untouched. Already-sealed values are skipped (idempotent).
func SealValues(vault *Vault, schema connector.Schema, v connector.Values) (connector.Values, error) {
	out := make(connector.Values, len(v))
	for k, val := range v {
		out[k] = val
	}
	for _, f := range schema.Fields {
		if f.Kind != connector.FieldSecret {
			continue
		}
		raw, ok := out[f.Name]
		if !ok || raw == nil {
			continue
		}
		s, isStr := raw.(string)
		if !isStr || s == "" || IsSealed(s) {
			continue
		}
		sealed, err := vault.Seal(s)
		if err != nil {
			return nil, fmt.Errorf("seal %s: %w", f.Name, err)
		}
		out[f.Name] = sealed
	}
	return out, nil
}

// OpenValues decrypts every FieldSecret value. Plain (legacy) values are
// returned unchanged so old credentials keep working until rewritten.
func OpenValues(vault *Vault, schema connector.Schema, v connector.Values) (connector.Values, error) {
	out := make(connector.Values, len(v))
	for k, val := range v {
		out[k] = val
	}
	for _, f := range schema.Fields {
		if f.Kind != connector.FieldSecret {
			continue
		}
		raw, ok := out[f.Name]
		if !ok || raw == nil {
			continue
		}
		s, isStr := raw.(string)
		if !isStr || s == "" {
			continue
		}
		opened, err := vault.Open(s)
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", f.Name, err)
		}
		out[f.Name] = opened
	}
	return out, nil
}
