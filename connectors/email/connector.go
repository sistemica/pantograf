// Package email is the unified email connector. One credential per account
// covers both IMAP (read/list/search/draft) and SMTP (send). Vendor presets
// in credentials.go fill protocol fields for Fastmail, GMX, Gmail, etc.
package email

import (
	"context"

	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/sistemica/pantograf/connector"
	"github.com/sistemica/pantograf/state"
	imaptr "github.com/sistemica/pantograf/transport/imap"
)

type Connector struct{}

func (Connector) Descriptor() connector.Descriptor {
	return connector.Descriptor{
		Name:        "email",
		DisplayName: "Email (IMAP + SMTP)",
		Description: "Read, search, send, draft email via IMAP+SMTP. Provider presets included.",
		Version:     "0.1.0",
		Categories:  []string{"email"},
	}
}

func (Connector) Credential() connector.CredentialSpec { return credSpec{} }

func (Connector) Actions() []connector.Action {
	return []connector.Action{
		readEmailsAction{},
		getEmailAction{},
		downloadAttachmentAction{},
		listFoldersAction{},
		searchEmailsAction{},
		saveDraftAction{},
		sendEmailAction{},
	}
}

// Triggers — IMAP IDLE / new-message stream comes later.
func (Connector) Triggers() []connector.Trigger { return nil }

// Open holds the credential; the IMAP client is dialled lazily on first use
// because not every action needs IMAP (send_email doesn't), and dialling
// has a 1+ second cost.
func (c Connector) Open(ctx context.Context, cred connector.Credential, opts connector.OpenOptions) (connector.Session, error) {
	return &session{c: c, cred: cred, state: opts.State}, nil
}

type session struct {
	c     Connector
	cred  connector.Credential
	state state.Store
	imap  *imapclient.Client // lazy
}

func (s *session) Connector() connector.Connector { return s.c }
func (s *session) State() state.Store             { return s.state }

func (s *session) Close() error {
	if s.imap != nil {
		_ = s.imap.Logout().Wait()
		err := s.imap.Close()
		s.imap = nil
		return err
	}
	return nil
}

// imapClient returns the dialled client, opening one on first call.
func (s *session) imapClient() (*imapclient.Client, error) {
	if s.imap != nil {
		return s.imap, nil
	}
	cli, err := imaptr.Dial(imapConfigFromCred(s.cred))
	if err != nil {
		return nil, err
	}
	s.imap = cli
	return cli, nil
}

// Register adds this connector to the given registry. Call from main or
// from a blank-import init() in a presets package.
func Register(r *connector.Registry) error { return r.Register(Connector{}) }
