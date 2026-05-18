// Package bunny is the Bunny.net DNS connector. Manages zones and
// records via api.bunny.net. One pgf instance = one Bunny account
// (one API key); zones are referenced by their numeric Bunny ID.
//
// Auth uses Bunny's `AccessKey: <key>` header (NOT Authorization: Bearer)
// so the connector builds the http client with that header baked in.
//
// Bunny encodes record types as integers (A=0, AAAA=1, CNAME=2, ...).
// The action layer accepts human-friendly strings and translates on the
// wire — agents never need to remember the enum.
package bunny

import (
	"context"

	"github.com/sistemica/pantograf/connector"
	"github.com/sistemica/pantograf/state"
	httptr "github.com/sistemica/pantograf/transport/http"
)

type Connector struct{}

func (Connector) Descriptor() connector.Descriptor {
	return connector.Descriptor{
		Name:        "bunny",
		DisplayName: "Bunny.net (DNS)",
		Description: "Manage Bunny.net DNS zones and records. List/create/delete zones; add/update/delete records of any type (A, AAAA, CNAME, MX, SRV, CAA, TXT, NS, …).",
		Version:     "0.1.0",
		Categories:  []string{"dns", "infra"},
	}
}

func (Connector) Credential() connector.CredentialSpec { return credSpec{} }

func (Connector) Triggers() []connector.Trigger { return nil }

func (Connector) Actions() []connector.Action {
	return []connector.Action{
		// DNS zones + records
		listZonesAction{},
		getZoneAction{},
		createZoneAction{},
		deleteZoneAction{},
		checkAvailabilityAction{},
		exportZoneAction{},
		addRecordAction{},
		updateRecordAction{},
		deleteRecordAction{},
		// CDN pull zones + hostnames + certs
		listPullZonesAction{},
		getPullZoneAction{},
		createPullZoneAction{},
		updatePullZoneAction{},
		deletePullZoneAction{},
		addHostnameAction{},
		removeHostnameAction{},
		loadFreeCertificateAction{},
		setForceSSLAction{},
	}
}

func (c Connector) Open(ctx context.Context, cred connector.Credential, opts connector.OpenOptions) (connector.Session, error) {
	cli, err := apiClient(cred)
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
