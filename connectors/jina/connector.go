// Package jina wraps Jina AI's hosted Reader / Search / Grounding
// endpoints. Use it as the fallback when the local `web` connector hits
// bot detection or JS-heavy pages it can't render — Jina runs a real
// browser fleet on residential-ish IPs and returns LLM-clean markdown.
//
// Three endpoints, one connector instance per Jina deployment (free or
// keyed). Default base URLs are Jina's public hosts; override only if
// you're using a self-hosted Reader mirror.
package jina

import (
	"context"

	"github.com/sistemica/pantograf/connector"
	"github.com/sistemica/pantograf/state"
	httptr "github.com/sistemica/pantograf/transport/http"
)

type Connector struct{}

func (Connector) Descriptor() connector.Descriptor {
	return connector.Descriptor{
		Name:        "jina",
		DisplayName: "Jina AI (Reader + Search + Grounding)",
		Description: "Hosted browser/SaaS for URL→markdown, web search, and statement grounding. Robust against bot blocking; useful when the local web connector fails.",
		Version:     "0.1.0",
		Categories:  []string{"scrape", "ai"},
	}
}

func (Connector) Credential() connector.CredentialSpec { return credSpec{} }

func (Connector) Triggers() []connector.Trigger { return nil }

func (Connector) Actions() []connector.Action {
	return []connector.Action{
		readAction{},
		searchAction{},
		groundAction{},
	}
}

func (c Connector) Open(ctx context.Context, cred connector.Credential, opts connector.OpenOptions) (connector.Session, error) {
	cli, err := readerClient(cred)
	if err != nil {
		return nil, err
	}
	return &session{c: c, cred: cred, http: cli, state: opts.State}, nil
}

type session struct {
	c     Connector
	cred  connector.Credential
	http  *httptr.Client
	state state.Store
}

func (s *session) Connector() connector.Connector { return s.c }
func (s *session) State() state.Store             { return s.state }
func (s *session) Close() error                   { return nil }

// Register adds this connector to the given registry.
func Register(r *connector.Registry) error { return r.Register(Connector{}) }
