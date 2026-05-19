// Package synapse wraps the Synapse-specific admin API at
// /_synapse/admin/v{1,2}/... — distinct from the matrix connector
// (which speaks the standard /_matrix/client/v3/... API any homeserver
// implements). Synapse admin endpoints are NOT a Matrix protocol
// feature; they're a server-implementation thing. Dendrite and
// Conduit have their own admin surfaces at different paths.
//
// Auth uses the same access-token shape as matrix, but the token's
// owner must have the admin flag set on the homeserver (one-time SQL
// `UPDATE users SET admin=1` to grant). Validate probes the admin
// status at connect time and refuses non-admin tokens.
//
// Pair this with a `matrix/<name>` instance pointing at the same
// homeserver if you want both ordinary user operations and admin
// operations from the same account — the two connectors stay
// independent so a non-admin user can never see admin actions.
package synapse

import (
	"context"

	"github.com/sistemica/pantograf/connector"
	"github.com/sistemica/pantograf/state"
	httptr "github.com/sistemica/pantograf/transport/http"
)

type Connector struct{}

func (Connector) Descriptor() connector.Descriptor {
	return connector.Descriptor{
		Name:        "synapse",
		DisplayName: "Synapse (admin API)",
		Description: "Manage users, rooms, server-wide state on a Synapse-flavoured Matrix homeserver. Requires an admin-flagged access token. Distinct from `matrix` (which is the standard Client-Server API any homeserver implements).",
		Version:     "0.1.0",
		Categories:  []string{"matrix", "admin"},
	}
}

func (Connector) Credential() connector.CredentialSpec { return credSpec{} }

func (Connector) Triggers() []connector.Trigger { return nil }

func (Connector) Actions() []connector.Action {
	return []connector.Action{
		serverVersionAction{},
		listUsersAction{},
		getUserAction{},
		createUserAction{},
		setPasswordAction{},
		deactivateUserAction{},
		listRoomsAction{},
		deleteRoomAction{},
		purgeHistoryAction{},
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
