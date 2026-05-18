package connector

import (
	"context"
	"testing"
)

func TestSchemaField(t *testing.T) {
	s := Schema{Fields: []FieldSpec{
		{Name: "a", Kind: FieldString},
		{Name: "b", Kind: FieldInt, IsPath: true},
	}}
	got, ok := s.Field("b")
	if !ok || got.Name != "b" || !got.IsPath {
		t.Fatalf("Field(b) = %+v, ok=%v", got, ok)
	}
	if _, ok := s.Field("nope"); ok {
		t.Fatal("Field(nope) should not be found")
	}
}

func TestFieldSpecIsActive(t *testing.T) {
	always := FieldSpec{Name: "a", Kind: FieldString}
	if !always.IsActive(Values{}) {
		t.Error("field with no ShowWhen should always be active")
	}

	gated := FieldSpec{
		Name: "refresh_token",
		Kind: FieldString,
		ShowWhen: func(v Values) bool {
			return v.String("auth_mode") == "oauth"
		},
	}
	if gated.IsActive(Values{}) {
		t.Error("ShowWhen=false should make field inactive (no auth_mode)")
	}
	if gated.IsActive(Values{"auth_mode": "basic"}) {
		t.Error("ShowWhen=false should make field inactive (auth_mode=basic)")
	}
	if !gated.IsActive(Values{"auth_mode": "oauth"}) {
		t.Error("ShowWhen=true should make field active (auth_mode=oauth)")
	}
}

func TestValuesAccessors(t *testing.T) {
	v := Values{
		"s":   "hi",
		"b":   true,
		"n":   42,
		"f":   3.14,
		"sl":  []string{"a", "b"},
		"csv": "x, y , ,z",
	}
	if v.String("s") != "hi" {
		t.Errorf("String(s) = %q", v.String("s"))
	}
	if v.Bool("b") != true {
		t.Errorf("Bool(b) = %v", v.Bool("b"))
	}
	if v.Int("n") != 42 {
		t.Errorf("Int(n) = %d", v.Int("n"))
	}
	if got := v.StringList("sl"); len(got) != 2 || got[0] != "a" {
		t.Errorf("StringList(sl) = %v", got)
	}
	// CSV string → split + trimmed + empties dropped
	if got := v.StringList("csv"); len(got) != 3 || got[0] != "x" || got[2] != "z" {
		t.Errorf("StringList(csv) = %v", got)
	}
}

func TestValuesWithDefaults(t *testing.T) {
	v := Values{"a": "set"}
	schema := Schema{Fields: []FieldSpec{
		{Name: "a", Kind: FieldString, Default: "should-not-overwrite"},
		{Name: "b", Kind: FieldInt, Default: 42},
	}}
	out := v.WithDefaults(schema)
	if out.String("a") != "set" {
		t.Errorf("a should remain 'set', got %q", out.String("a"))
	}
	if out.Int("b") != 42 {
		t.Errorf("b should default to 42, got %d", out.Int("b"))
	}
}

func TestValuesHas(t *testing.T) {
	v := Values{"x": ""}
	if !v.Has("x") {
		t.Error("Has should be true even for empty value")
	}
	if v.Has("y") {
		t.Error("Has should be false for missing key")
	}
}

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	c := stubConnector{name: "test"}
	if err := r.Register(c); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := r.Register(c); err == nil {
		t.Fatal("duplicate Register should error")
	}
	got, ok := r.Get("test")
	if !ok || got.Descriptor().Name != "test" {
		t.Fatalf("Get(test) failed")
	}
	if _, ok := r.Get("nope"); ok {
		t.Fatal("Get(nope) should not find")
	}
	descs := r.List()
	if len(descs) != 1 || descs[0].Name != "test" {
		t.Fatalf("List = %+v", descs)
	}
}

// stubConnector — minimal Connector impl for registry tests.
type stubConnector struct{ name string }

func (s stubConnector) Descriptor() Descriptor   { return Descriptor{Name: s.name} }
func (stubConnector) Credential() CredentialSpec { return nil }
func (stubConnector) Actions() []Action          { return nil }
func (stubConnector) Triggers() []Trigger        { return nil }
func (stubConnector) Open(context.Context, Credential, OpenOptions) (Session, error) {
	return nil, nil
}
