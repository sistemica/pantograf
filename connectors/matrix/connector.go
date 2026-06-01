// Package matrix is the Matrix (Client-Server API) connector. Bearer-token
// auth, optional login fallback (mints token at Validate, password discarded).
//
// Each pantograf instance = one Matrix user account, identified by its
// access_token. Multiple instances coexist (e.g. matrix/hannes,
// matrix/claudia) — same connector, different identity.
//
// v0.1: send/read messages, list/get rooms, whoami. The `/sync` polling
// trigger is a planned addition.
package matrix

import (
	"context"
	"sync/atomic"

	"github.com/sistemica/pantograf/connector"
	"github.com/sistemica/pantograf/state"
	httptr "github.com/sistemica/pantograf/transport/http"
)

type Connector struct{}

func (Connector) Descriptor() connector.Descriptor {
	return connector.Descriptor{
		Name:        "matrix",
		DisplayName: "Matrix (Client-Server API)",
		Description: "Send + read messages, list rooms. Bearer auth, login-fallback for token minting.",
		Version:     "0.1.0",
		Categories:  []string{"messaging"},
	}
}

func (Connector) Credential() connector.CredentialSpec { return credSpec{} }

func (Connector) Triggers() []connector.Trigger {
	return []connector.Trigger{messagesTrigger{}}
}

func (Connector) Actions() []connector.Action {
	return []connector.Action{
		whoamiAction{},
		listRoomsAction{},
		getRoomAction{},
		sendMessageAction{},
		setTypingAction{},
		getMessagesAction{},
		createRoomAction{},
		createSpaceAction{},
		inviteUserAction{},
		addRoomToSpaceAction{},
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
	txn   atomic.Uint64 // monotonic counter for send-event txnIds
}

func (s *session) Connector() connector.Connector { return s.c }
func (s *session) State() state.Store             { return s.state }
func (s *session) Close() error                   { return nil }

// Register adds this connector to the given registry.
func Register(r *connector.Registry) error { return r.Register(Connector{}) }
