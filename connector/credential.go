package connector

import (
	"context"
	"time"
)

// AuthKind tags the broad shape of authentication a connector uses. It is
// purely informational — wiring is done by Schema fields, not by AuthKind.
type AuthKind string

const (
	AuthBasic   AuthKind = "basic"    // username + password / IMAP / SMTP
	AuthAPIKey  AuthKind = "api_key"  // single-token bearer / x-api-key
	AuthOAuth2  AuthKind = "oauth2"   // OAuth2 redirect flow
	AuthCustom  AuthKind = "custom"   // bespoke (e.g. JWT-from-cert)
	AuthNone    AuthKind = "none"     // public service
)

// CredentialSpec is the connector-author's declaration of what an instance
// needs to authenticate. It is what drives the interactive wizard.
type CredentialSpec interface {
	// Kind is informational; the wizard reads Schema, not Kind.
	Kind() AuthKind

	// Schema lists the fields the user must (or may) provide.
	Schema() Schema

	// Presets are vendor-specific config sets the wizard offers as a first
	// choice (e.g. "Fastmail", "GMX"). Picking one prefills a subset of
	// fields; the wizard then prompts only for what remains. May be nil
	// for connectors with no meaningful per-vendor distinction.
	Presets() []Preset

	// Defaults fills any values the connector can derive from what the
	// user has already provided (e.g. inferring smtp.fastmail.com from
	// imap.fastmail.com). Called after preset application, before
	// per-field prompting completes. Returning input unchanged is fine.
	Defaults(partial Values) Values

	// Validate probes the live service to confirm the credential works.
	// Called at the end of the wizard. Return a useful error so the user
	// knows what to fix. Connectors that cannot probe (e.g. no public
	// status endpoint) may return nil.
	Validate(ctx context.Context, c Credential) error
}

// Preset is a named, partial set of values the wizard can apply at the start
// of the credential-collection flow. Vendor knowledge lives here as data,
// not as a separate connector type.
type Preset struct {
	Name        string // "Fastmail"
	Description string // shown next to the name in the picker
	Values      Values // partial — fields it does not set get prompted normally
}

// Credential is one named, populated set of values for a connector type.
// Persisted via the Store; used at runtime to Open() a Session.
type Credential struct {
	Type      string    `yaml:"type"`
	Name      string    `yaml:"name"`
	Values    Values    `yaml:"values"`
	CreatedAt time.Time `yaml:"created_at"`
	UpdatedAt time.Time `yaml:"updated_at"`
}
