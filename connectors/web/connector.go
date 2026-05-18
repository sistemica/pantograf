// Package web is the HTTP fetch + page-extraction connector. Default
// path is pure-Go (net/http + goquery + html-to-markdown + readability).
// JS-heavy pages can be rendered by connecting to a Chrome instance over
// CDP; the connector never auto-spawns a browser.
//
// All actions share a per-instance disk cache (in the state store), so
// calling extract-links → extract-media → extract-markdown on the same
// URL costs one HTTP (or one CDP roundtrip), not three.
package web

import (
	"context"

	"github.com/sistemica/pantograf/connector"
	"github.com/sistemica/pantograf/state"
)

type Connector struct{}

func (Connector) Descriptor() connector.Descriptor {
	return connector.Descriptor{
		Name:        "web",
		DisplayName: "Web (fetch + extract)",
		Description: "Fetch pages and extract markdown, links, media, or HTML by CSS selector. Default HTTP; optional CDP browser mode for JS-heavy pages.",
		Version:     "0.1.0",
		Categories:  []string{"scrape", "io"},
	}
}

func (Connector) Credential() connector.CredentialSpec { return credSpec{} }

func (Connector) Triggers() []connector.Trigger { return nil }

func (Connector) Actions() []connector.Action {
	return []connector.Action{
		fetchAction{},
		extractMarkdownAction{},
		extractHTMLAction{},
		extractLinksAction{},
		extractMediaAction{},
		screenshotAction{},
		searchAction{},
	}
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
