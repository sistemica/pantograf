// Package paperless is the connector for Paperless-ngx, the open-source
// document-management system. One pgf instance = one Paperless-ngx server
// (one URL + one user's token).
//
// Auth: Paperless uses `Authorization: Token <token>` (DRF TokenAuth). A
// token can be supplied directly, or derived from username+password via
// POST /api/token/ at connect time — the matrix-style login fallback. The
// resolved token is written back into the credential so later sessions skip
// the exchange.
//
// Resource model mirrors the Paperless UI:
//
//	document  — the OCR'd file + its metadata
//	  ├── correspondent   (who sent it)
//	  ├── document_type   (what it is)
//	  └── tags[]          (free-form labels)
//
// Uploads are asynchronous: post_document/ returns a task UUID; the file is
// OCR'd and consumed in the background. Poll task-status with that UUID to
// learn the resulting document id.
package paperless

import (
	"context"

	"github.com/sistemica/pantograf/connector"
	"github.com/sistemica/pantograf/state"
	httptr "github.com/sistemica/pantograf/transport/http"
)

type Connector struct{}

func (Connector) Descriptor() connector.Descriptor {
	return connector.Descriptor{
		Name:        "paperless",
		DisplayName: "Paperless-ngx",
		Description: "Search, read, upload, download, and organise documents in a Paperless-ngx instance. Token auth (or username+password exchange). Tags / correspondents / document-types CRUD.",
		Version:     "0.1.0",
		Categories:  []string{"documents", "dms"},
	}
}

func (Connector) Credential() connector.CredentialSpec { return credSpec{} }

func (Connector) Triggers() []connector.Trigger { return nil }

func (Connector) Actions() []connector.Action {
	return []connector.Action{
		// documents
		listDocumentsAction{},
		getDocumentAction{},
		downloadDocumentAction{},
		uploadDocumentAction{},
		updateDocumentAction{},
		deleteDocumentAction{},
		// taxonomy (read)
		taxonomyListAction{name: "list-tags", label: "tags", path: "/api/tags/"},
		taxonomyListAction{name: "list-correspondents", label: "correspondents", path: "/api/correspondents/"},
		taxonomyListAction{name: "list-document-types", label: "document types", path: "/api/document_types/"},
		// taxonomy (create)
		createTagAction{},
		createCorrespondentAction{},
		createDocumentTypeAction{},
		// misc
		taskStatusAction{},
		statisticsAction{},
	}
}

func (c Connector) Open(ctx context.Context, cred connector.Credential, opts connector.OpenOptions) (connector.Session, error) {
	cli, err := apiClient(ctx, cred)
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

// listPage is Paperless's standard paginated envelope.
type listPage struct {
	Count    int     `json:"count"`
	Next     *string `json:"next"`
	Previous *string `json:"previous"`
	Results  []any   `json:"results"`
}
