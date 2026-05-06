// Package webhook is the generic HTTP receiver. One instance = one
// upstream channel: a URL that any HTTP client can POST/GET/etc to. The
// runtime hosts it via `pgf serve`. Inbound requests turn into Events
// emitted on stdout; the response is configurable per-instance (static
// body, file read at request time, or just status).
package webhook

import (
	"context"

	"github.com/sistemica/pantograf/connector"
	"github.com/sistemica/pantograf/state"
)

type Connector struct{}

func (Connector) Descriptor() connector.Descriptor {
	return connector.Descriptor{
		Name:        "webhook",
		DisplayName: "Generic webhook receiver",
		Description: "HTTP-in primitive. Any method, any path, captures method/query/headers/body. Optional auth + configurable response.",
		Version:     "0.1.0",
		Categories:  []string{"http", "trigger"},
	}
}

func (Connector) Credential() connector.CredentialSpec { return credSpec{} }

func (Connector) Actions() []connector.Action { return nil }

func (Connector) Triggers() []connector.Trigger {
	return []connector.Trigger{incomingTrigger{}}
}

func (c Connector) Open(ctx context.Context, cred connector.Credential, opts connector.OpenOptions) (connector.Session, error) {
	return &session{c: c, cred: cred, state: opts.State}, nil
}

type session struct {
	c     Connector
	cred  connector.Credential
	state state.Store
}

func (s *session) Connector() connector.Connector { return s.c }
func (s *session) State() state.Store             { return s.state }
func (s *session) Close() error                   { return nil }

// Register adds this connector to the given registry.
func Register(r *connector.Registry) error { return r.Register(Connector{}) }
