// Package youtrack is the YouTrack (JetBrains) connector. Permanent-token
// auth, REST + JSON. One pantograf instance per user-token — multi-user
// access is achieved by registering multiple instances:
//
//   pgf connect youtrack admin       # admin token
//   pgf connect youtrack katharina   # katharina's personal token
//
// YouTrack has no impersonation; each token runs as its owning user with
// that user's permissions and audit-log identity.
package youtrack

import (
	"context"

	"github.com/sistemica/pantograf/connector"
	"github.com/sistemica/pantograf/state"
	httptr "github.com/sistemica/pantograf/transport/http"
)

type Connector struct{}

func (Connector) Descriptor() connector.Descriptor {
	return connector.Descriptor{
		Name:        "youtrack",
		DisplayName: "YouTrack (JetBrains)",
		Description: "Issues, projects, users. Bearer (permanent token) auth.",
		Version:     "0.1.0",
		Categories:  []string{"issues", "collab"},
	}
}

func (Connector) Credential() connector.CredentialSpec { return credSpec{} }

func (Connector) Triggers() []connector.Trigger { return nil }

func (Connector) Actions() []connector.Action {
	return []connector.Action{
		meAction{},
		listUsersAction{},
		getUserAction{},
		createUserAction{},
		listProjectsAction{},
		getProjectAction{},
		createProjectAction{},
		listIssuesAction{},
		getIssueAction{},
		createIssueAction{},
		updateIssueAction{},
		addCommentAction{},
		applyCommandAction{},
		setAssigneeAction{},
		listAttachmentsAction{},
		attachFileAction{},
		downloadAttachmentAction{},
		createTokenAction{},
		listProjectTeamAction{},
		listArticlesAction{},
		getArticleAction{},
		getArticleTreeAction{},
		listChildArticlesAction{},
		createArticleAction{},
		updateArticleAction{},
		deleteArticleAction{},
		linkIssuesAction{},
		listIssueLinksAction{},
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
