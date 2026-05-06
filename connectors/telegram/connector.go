// Package telegram is the Telegram Bot API connector. One credential = one
// bot token. Actions wrap the most-used Bot API methods on top of the
// shared transport/http client.
package telegram

import (
	"context"

	"github.com/sistemica/pantograf/connector"
	"github.com/sistemica/pantograf/state"
	httptr "github.com/sistemica/pantograf/transport/http"
)

type Connector struct{}

func (Connector) Descriptor() connector.Descriptor {
	return connector.Descriptor{
		Name:        "telegram",
		DisplayName: "Telegram (Bot API)",
		Description: "Send messages, photos, documents through a Telegram bot.",
		Version:     "0.1.0",
		Categories:  []string{"messaging"},
	}
}

func (Connector) Credential() connector.CredentialSpec { return credSpec{} }

func (Connector) Actions() []connector.Action {
	return []connector.Action{
		getMeAction{},
		getUpdatesAction{},
		sendMessageAction{},
		sendPhotoAction{},
		sendDocumentAction{},
		setWebhookAction{},
		deleteWebhookAction{},
		getWebhookInfoAction{},
	}
}

// Triggers — only the polling messages trigger. To receive via webhook,
// set the webhook URL pointing at a generic webhook connector instance
// using set_webhook, then host that with `mw serve`.
func (Connector) Triggers() []connector.Trigger {
	return []connector.Trigger{messagesTrigger{}}
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
func (s *session) Close() error                   { return nil } // HTTP is per-call

// Register adds this connector to the given registry.
func Register(r *connector.Registry) error { return r.Register(Connector{}) }
