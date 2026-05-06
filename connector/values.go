package connector

import (
	"fmt"
	"strconv"
	"strings"
)

// Values is a typed bag carrying parameter or credential values. It is the
// runtime counterpart of Schema — the wizard and action runners produce one,
// connector code consumes one.
//
// The accessors return zero values when a key is missing, mirroring how Go
// maps work; use Has() to disambiguate. Type coercion is intentional and
// minimal: strings parse as int/bool, numbers stringify, booleans stringify.
// Anything more is the caller's problem.
type Values map[string]any

func (v Values) Has(key string) bool {
	_, ok := v[key]
	return ok
}

func (v Values) String(key string) string {
	raw, ok := v[key]
	if !ok || raw == nil {
		return ""
	}
	switch x := raw.(type) {
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	default:
		return fmt.Sprint(x)
	}
}

func (v Values) Int(key string) int {
	raw, ok := v[key]
	if !ok || raw == nil {
		return 0
	}
	switch x := raw.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		n, _ := strconv.Atoi(x)
		return n
	}
	return 0
}

func (v Values) Bool(key string) bool {
	raw, ok := v[key]
	if !ok || raw == nil {
		return false
	}
	switch x := raw.(type) {
	case bool:
		return x
	case string:
		b, _ := strconv.ParseBool(x)
		return b
	case int:
		return x != 0
	}
	return false
}

// StringList returns key as a list. A single string with commas is split; a
// pre-built []string passes through. Whitespace is trimmed and empty entries
// dropped — handy for CLI input like "-p to=a@x,b@y, c@z".
func (v Values) StringList(key string) []string {
	raw, ok := v[key]
	if !ok || raw == nil {
		return nil
	}
	switch x := raw.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			out = append(out, fmt.Sprint(e))
		}
		return out
	case string:
		if x == "" {
			return nil
		}
		parts := strings.Split(x, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if t := strings.TrimSpace(p); t != "" {
				out = append(out, t)
			}
		}
		return out
	}
	return nil
}

// WithDefaults returns a copy of v with any missing fields populated from
// schema defaults. Existing keys are left untouched.
func (v Values) WithDefaults(s Schema) Values {
	out := make(Values, len(v)+len(s.Fields))
	for k, val := range v {
		out[k] = val
	}
	for _, f := range s.Fields {
		if f.Default == nil {
			continue
		}
		if _, ok := out[f.Name]; !ok {
			out[f.Name] = f.Default
		}
	}
	return out
}
