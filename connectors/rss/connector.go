// Package rss is a generic RSS / Atom / JSON Feed reader. One credential =
// one feed URL. State store tracks the last-seen item id per instance so
// `list-new` and the `new-items` trigger only emit items the user hasn't
// processed before.
//
// No auth in v1 (most feeds are public). Future: HTTP basic auth for
// gated feeds.
package rss

import (
	"context"

	"github.com/sistemica/pantograf/connector"
	"github.com/sistemica/pantograf/state"
)

type Connector struct{}

func (Connector) Descriptor() connector.Descriptor {
	return connector.Descriptor{
		Name:        "rss",
		DisplayName: "RSS / Atom / JSON Feed",
		Description: "Stateful feed reader. Tracks last-seen item per instance; only surfaces items the consumer hasn't acked.",
		Version:     "0.1.0",
		Categories:  []string{"news", "feed"},
	}
}

func (Connector) Credential() connector.CredentialSpec { return credSpec{} }

func (Connector) Actions() []connector.Action {
	return []connector.Action{
		fetchAction{},
		listNewAction{},
		markSeenAction{},
		infoAction{},
		resetAction{},
	}
}

func (Connector) Triggers() []connector.Trigger {
	return []connector.Trigger{newItemsTrigger{}}
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
