// Package file is the pluggable-backend file connector. One credential
// picks a driver (local | s3) and binds to one bucket / root; the action
// set (list, get, put, delete, stat, search, presign) stays identical
// regardless. Same design philosophy memo uses for its history backends.
//
// Adding a backend is one file implementing the driver interface +
// a couple of ShowWhen-gated fields on the credential schema. The
// action layer never grows.
package file

import (
	"context"

	"github.com/sistemica/pantograf/connector"
	"github.com/sistemica/pantograf/state"
)

type Connector struct{}

func (Connector) Descriptor() connector.Descriptor {
	return connector.Descriptor{
		Name:        "file",
		DisplayName: "File (pluggable backend)",
		Description: "List/get/put/delete/stat/search files. Backends: local filesystem, S3-compatible (AWS, MinIO, R2, B2).",
		Version:     "0.1.0",
		Categories:  []string{"storage", "io"},
	}
}

func (Connector) Credential() connector.CredentialSpec { return credSpec{} }

func (Connector) Triggers() []connector.Trigger { return nil }

func (Connector) Actions() []connector.Action {
	return []connector.Action{
		listAction{},
		statAction{},
		getAction{},
		putAction{},
		deleteAction{},
		searchAction{},
		presignAction{},
	}
}

func (c Connector) Open(ctx context.Context, cred connector.Credential, opts connector.OpenOptions) (connector.Session, error) {
	drv, err := buildDriver(cred)
	if err != nil {
		return nil, err
	}
	return &session{c: c, cred: cred, drv: drv, state: opts.State}, nil
}

type session struct {
	c     Connector
	cred  connector.Credential
	drv   driver
	state state.Store
}

func (s *session) Connector() connector.Connector { return s.c }
func (s *session) State() state.Store             { return s.state }
func (s *session) Close() error                   { return nil }

// Register adds this connector to the given registry.
func Register(r *connector.Registry) error { return r.Register(Connector{}) }
