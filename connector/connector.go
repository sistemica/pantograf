package connector

import (
	"context"

	"github.com/sistemica/pantograf/state"
)

// Descriptor is a connector type's self-description. It is static
// per-connector — no instance information here.
type Descriptor struct {
	Name        string   // canonical, kebab-case: "fastmail", "hub-spot"
	DisplayName string   // human-friendly: "Fastmail"
	Description string
	Version     string
	Categories  []string // free-form tags: "email", "crm", ...
}

// OpenOptions are runtime-injected dependencies handed to a Connector at
// Open time. Adding a field here is a backward-compatible change for
// callers (the runtime fills it); connector authors opt in by reading
// the field they care about.
type OpenOptions struct {
	// State is the per-instance state store. Connectors that need to
	// persist offsets, cursors, or other small state read/write here.
	// Always non-nil — the runtime substitutes a no-op if the operator
	// disables persistent state.
	State state.Store
}

// Connector is the type-level definition. Instances of a Connector type are
// created by binding a stored Credential through Open().
type Connector interface {
	Descriptor() Descriptor
	Credential() CredentialSpec
	Actions() []Action
	Triggers() []Trigger // may return nil for connectors that have no event sources

	// Open binds a populated credential into a live Session. opts carries
	// runtime-injected dependencies (state store, future: logger, metrics).
	// Callers must Close().
	Open(ctx context.Context, cred Credential, opts OpenOptions) (Session, error)
}

// Session is the runtime, credential-bound handle. Each action takes one.
type Session interface {
	Connector() Connector
	State() state.Store
	Close() error
}

// Action is one callable operation on a Connector. Self-describes its input
// schema; runs against an open Session.
type Action interface {
	Name() string
	DisplayName() string
	Description() string
	Schema() Schema
	// Run executes the action. params has already had defaults applied;
	// schema validation is the action's responsibility (helper functions
	// in package validate live separately to keep this interface small).
	Run(ctx context.Context, sess Session, params Values) (any, error)
}
