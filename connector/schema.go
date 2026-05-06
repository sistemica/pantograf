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
