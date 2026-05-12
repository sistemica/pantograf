// Package llm is the OpenAI-compatible LLM connector. Talks to any
// `/v1/chat/completions` endpoint: your local proxy, OpenAI, Together,
// Groq, Ollama, vLLM, anything that speaks the OpenAI shape.
//
// Multiple instances coexist — one per backend / per key. Typical setup:
//   pgf connect llm proxy    (Sistemica's local proxy at 192.168.1.125:4000)
//   pgf connect llm openai   (api.openai.com)
package llm

import (
	"context"

	"github.com/sistemica/pantograf/connector"
	"github.com/sistemica/pantograf/state"
	httptr "github.com/sistemica/pantograf/transport/http"
)

type Connector struct{}

func (Connector) Descriptor() connector.Descriptor {
	return connector.Descriptor{
		Name:        "llm",
		DisplayName: "LLM (OpenAI-compatible)",
		Description: "Chat completions + embeddings + model list against any OpenAI-compatible endpoint.",
		Version:     "0.1.0",
		Categories:  []string{"ai"},
	}
}

func (Connector) Credential() connector.CredentialSpec { return credSpec{} }

func (Connector) Triggers() []connector.Trigger { return nil }

func (Connector) Actions() []connector.Action {
	return []connector.Action{
		listModelsAction{},
		chatCompletionAction{},
		embedAction{},
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
