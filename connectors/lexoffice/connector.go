// Package lexoffice is the Lexoffice (Lexware Office) connector. German
// accounting / invoicing API. Bearer-token auth, REST + JSON, plus a
// dedicated file-download endpoint for invoice PDFs.
//
// v0.1 scope: read-side actions (profile, contacts, vouchers, invoices,
// PDF download). Write actions (create invoice, upload voucher file) come
// next once the read path is solid.
package lexoffice

import (
	"context"

	"github.com/sistemica/pantograf/connector"
	"github.com/sistemica/pantograf/state"
	httptr "github.com/sistemica/pantograf/transport/http"
)

type Connector struct{}

func (Connector) Descriptor() connector.Descriptor {
	return connector.Descriptor{
		Name:        "lexoffice",
		DisplayName: "Lexware Office (formerly Lexoffice)",
		Description: "Read German accounting data: profile, contacts, vouchers, invoices, invoice PDFs.",
		Version:     "0.1.0",
		Categories:  []string{"accounting", "invoicing"},
	}
}

func (Connector) Credential() connector.CredentialSpec { return credSpec{} }

func (Connector) Triggers() []connector.Trigger { return nil }

func (Connector) Actions() []connector.Action {
	return []connector.Action{
		getProfileAction{},
		listContactsAction{},
		getContactAction{},
		listVouchersAction{},
		getVoucherAction{},
		downloadVoucherPDFAction{},
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

func Register(r *connector.Registry) error { return r.Register(Connector{}) }
