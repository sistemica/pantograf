// Package infisical is the connector for Infisical secrets management.
// One pgf instance = one Infisical machine identity (clientId +
// clientSecret pair) on one Infisical deployment (hosted at app.infisical.com,
// eu.infisical.com, or a self-hosted instance).
//
// Auth: Universal Auth. On Open(), POST clientId+clientSecret to
// /api/v1/auth/universal-auth/login → receive a short-lived Bearer
// accessToken (~2h TTL by default). The token is held in memory for
// the lifetime of the session; for a long-running `pgf serve` daemon
// it would need refresh logic on 401 (not implemented in v1 —
// per-action calls are short enough that re-login on Open is
// negligible).
//
// Resource hierarchy mirrors Infisical's UI:
//   project (workspace) → environment (slug: dev/staging/prod) →
//   folder (secretPath, default "/") → secret (by name)
//
// The connector uses v3 /raw endpoints — the server-decrypted variants
// — because the agent wants plaintext values, not E2EE-wrapped blobs.
// E2EE-enabled workspaces will refuse /raw access; the action layer
// surfaces that error verbatim so the operator can choose to disable
// E2EE in the workspace settings (or wrap the encryption client-side
// outside this connector).
package infisical

import (
	"context"

	"github.com/sistemica/pantograf/connector"
	"github.com/sistemica/pantograf/state"
	httptr "github.com/sistemica/pantograf/transport/http"
)

type Connector struct{}

func (Connector) Descriptor() connector.Descriptor {
	return connector.Descriptor{
		Name:        "infisical",
		DisplayName: "Infisical (secrets management)",
		Description: "Read/write secrets and folders in an Infisical workspace via Universal Auth. Self-hosted-friendly.",
		Version:     "0.1.0",
		Categories:  []string{"secrets", "infra"},
	}
}

func (Connector) Credential() connector.CredentialSpec { return credSpec{} }

func (Connector) Triggers() []connector.Trigger { return nil }

func (Connector) Actions() []connector.Action {
	return []connector.Action{
		// projects
		listProjectsAction{},
		createProjectAction{},
		getProjectAction{},
		updateProjectAction{},
		deleteProjectAction{},
		// environments
		listEnvironmentsAction{},
		createEnvironmentAction{},
		updateEnvironmentAction{},
		deleteEnvironmentAction{},
		// folders + secrets
		listFoldersAction{},
		createFolderAction{},
		deleteFolderAction{},
		listSecretsAction{},
		getSecretAction{},
		createSecretAction{},
		updateSecretAction{},
		deleteSecretAction{},
		// org members
		listOrgMembersAction{},
		updateOrgMemberRoleAction{},
		removeOrgMemberAction{},
		// project members (users + identities)
		listProjectMembersAction{},
		addProjectMemberAction{},
		updateProjectMemberRoleAction{},
		removeProjectMemberAction{},
		listProjectIdentitiesAction{},
		addProjectIdentityAction{},
		removeProjectIdentityAction{},
	}
}

func (c Connector) Open(ctx context.Context, cred connector.Credential, opts connector.OpenOptions) (connector.Session, error) {
	cli, err := authedClient(ctx, cred)
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
