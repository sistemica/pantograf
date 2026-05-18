// Package whisper is the dedicated speech-to-text connector. Targets
// standalone OpenAI-compatible Whisper servers: faster-whisper-server,
// whisper.cpp's HTTP front-end, vLLM-Whisper, Speaches, etc. The wire
// shape is identical to OpenAI's /audio/transcriptions endpoint; the
// difference from the `llm` connector's transcribe action is only where
// it points (a local STT-only server, often without auth) and that this
// connector exposes the few audio-specific extras some servers add
// (vad_filter, language auto-detect threshold, ...).
//
// For agents already paying for an OpenAI key or running a unified LLM
// proxy that bridges audio, use llm/<instance> transcribe — no second
// connector needed.
package whisper

import (
	"context"

	"github.com/sistemica/pantograf/connector"
	"github.com/sistemica/pantograf/state"
	httptr "github.com/sistemica/pantograf/transport/http"
)

type Connector struct{}

func (Connector) Descriptor() connector.Descriptor {
	return connector.Descriptor{
		Name:        "whisper",
		DisplayName: "Whisper (speech-to-text)",
		Description: "Dedicated STT connector for standalone Whisper servers (faster-whisper-server, whisper.cpp, vLLM-Whisper). OpenAI-shape /audio/transcriptions.",
		Version:     "0.1.0",
		Categories:  []string{"ai", "audio"},
	}
}

func (Connector) Credential() connector.CredentialSpec { return credSpec{} }

func (Connector) Triggers() []connector.Trigger { return nil }

func (Connector) Actions() []connector.Action {
	return []connector.Action{
		listModelsAction{},
		transcribeAction{},
		translateAction{},
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
