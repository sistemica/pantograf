package llm

import (
	"context"
	"errors"
	"fmt"
	stdhttp "net/http"
	"strings"
	"time"

	"github.com/sistemica/pantograf/connector"
	httptr "github.com/sistemica/pantograf/transport/http"
)

const (
	fAPIKey  = "api_key"
	fAPIBase = "api_base"
)

type credSpec struct{}

func (credSpec) Kind() connector.AuthKind { return connector.AuthAPIKey }

func (credSpec) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{
			Name:        fAPIKey,
			Label:       "API key",
			Kind:        connector.FieldSecret,
			Required:    true,
			Description: "Sent as `Authorization: Bearer <key>`.",
		},
		{
			Name:        fAPIBase,
			Label:       "API base URL",
			Kind:        connector.FieldString,
			Required:    true,
			Description: "OpenAI-compatible `/v1` root. e.g. http://192.168.1.125:4000/v1, https://api.openai.com/v1.",
		},
	}}
}

func (credSpec) Presets() []connector.Preset {
	return []connector.Preset{
		{
			Name:        "Local LLM proxy",
			Description: "http://192.168.1.125:4000/v1 — Strix Halo + Mac Studio aggregate (qwen36-27b / nemotron / etc.)",
			Values:      connector.Values{fAPIBase: "http://192.168.1.125:4000/v1"},
		},
		{
			Name:        "OpenAI",
			Description: "https://api.openai.com/v1 — official",
			Values:      connector.Values{fAPIBase: "https://api.openai.com/v1"},
		},
		{
			Name:        "Custom",
			Description: "Enter base URL manually (e.g. Together, Groq, Ollama, vLLM)",
			Values:      connector.Values{},
		},
	}
}

func (credSpec) Defaults(p connector.Values) connector.Values {
	out := make(connector.Values, len(p))
	for k, v := range p {
		out[k] = v
	}
	if u, ok := out[fAPIBase].(string); ok {
		out[fAPIBase] = strings.TrimRight(u, "/")
	}
	return out
}

// Validate calls /models. Cheap, confirms both auth + base URL + that
// the endpoint speaks the OpenAI shape (returns {data: [...]}).
func (credSpec) Validate(ctx context.Context, c connector.Credential) error {
	cli, err := apiClient(c)
	if err != nil {
		return err
	}
	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := cli.GetJSON(ctx, "/models", nil, &resp); err != nil {
		return fmt.Errorf("models probe: %w", err)
	}
	if len(resp.Data) > 0 {
		fmt.Printf("(%d models, first: %s) ", len(resp.Data), resp.Data[0].ID)
	}
	return nil
}

func apiClient(c connector.Credential) (*httptr.Client, error) {
	key := c.Values.String(fAPIKey)
	if key == "" {
		return nil, errors.New("api_key is empty")
	}
	base := strings.TrimRight(c.Values.String(fAPIBase), "/")
	if base == "" {
		return nil, errors.New("api_base is empty")
	}
	return httptr.New(httptr.Config{
		BaseURL: base,
		Headers: stdhttp.Header{
			"Authorization": []string{"Bearer " + key},
		},
		// LLM calls — especially with thinking — routinely take minutes.
		// The 30s default would clip every reasoning call. Long timeout
		// here; ctx still drives shutdown if the caller cancels.
		Timeout: 10 * time.Minute,
	})
}
