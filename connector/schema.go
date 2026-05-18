package connector

// FieldKind classifies a parameter or credential field. The wizard and
// validator dispatch off this. Keep the set small and explicit — no reflect.
type FieldKind string

const (
	FieldString     FieldKind = "string"
	FieldLongText   FieldKind = "long_text"
	FieldSecret     FieldKind = "secret"
	FieldInt        FieldKind = "int"
	FieldBool       FieldKind = "bool"
	FieldStringList FieldKind = "string_list"
	FieldEnum       FieldKind = "enum"
)

// FieldSpec is one parameter declaration. A connector hand-writes these in
// its Schema() method (no struct-tag reflection at runtime).
type FieldSpec struct {
	Name        string
	Label       string
	Description string
	Kind        FieldKind
	Required    bool
	Default     any
	Options     []EnumOption

	// IsPath marks the field's value as a local filesystem path. When set,
	// the runtime validates the value against PGF_ALLOWED_PATHS before the
	// action or credential is allowed to run. Use for any field whose
	// value reaches os.Open / os.Create / io.Copy / equivalents — agents
	// can otherwise abuse such fields to exfiltrate the credential dir
	// even when sudo is restricted to the pgf binary.
	//
	// Works with FieldString and FieldStringList. Dual-mode fields (path
	// OR URL, e.g. Telegram media) leave this false and validate inside
	// the connector when the value is path-shaped.
	IsPath bool

	// ShowWhen is an optional predicate that controls whether the field is
	// active for a given set of already-collected values. The wizard skips
	// the prompt when this returns false, the paths validator skips the
	// check, and Required-ness is suspended. Used to gate driver-specific
	// fields (e.g. an OAuth `refresh_token` field that only matters when
	// auth_mode=oauth). When nil, the field is always active.
	//
	// Predicates should be deterministic and side-effect-free. They see the
	// values collected so far, so ordering matters — list the discriminator
	// field (the one ShowWhen reads) before any dependent fields.
	ShowWhen func(Values) bool
}

// IsActive reports whether the field should be prompted / validated /
// required given the values gathered so far. Fields without ShowWhen are
// always active.
func (f FieldSpec) IsActive(v Values) bool {
	if f.ShowWhen == nil {
		return true
	}
	return f.ShowWhen(v)
}

type EnumOption struct {
	Value string
	Label string
}

// Schema is an ordered list of FieldSpecs. It drives the credential wizard,
// CLI flag parsing for action params, and the externally-visible
// self-description for any consumer (HTTP, MCP, agent, ...).
type Schema struct {
	Fields []FieldSpec
}

// Field returns the spec for a named field, or zero + false.
func (s Schema) Field(name string) (FieldSpec, bool) {
	for _, f := range s.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return FieldSpec{}, false
}
