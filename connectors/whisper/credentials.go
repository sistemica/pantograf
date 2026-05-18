package whisper

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
	fAPIBase       = "api_base"
	fAPIKey        = "api_key"
	fDefaultModel  = "default_model"
)

type credSpec struct{}

func (credSpec) Kind() connector.AuthKind { return connector.AuthAPIKey }

func (credSpec) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{
			Name: fAPIBase, Label: "API base URL", Kind: connector.FieldString, Required: true,
			Description: "/v1-rooted base URL of the Whisper server. e.g. http://192.168.1.125:8000/v1, https://api.openai.com/v1.",
		},
		{
			Name: fAPIKey, Label: "API key (optional)", Kind: connector.FieldSecret,
			Description: "Bearer token, if the server requires one. Most local servers (faster-whisper-server with defaults) don't.",
		},
		{
			Name: fDefaultModel, Label: "Default model (optional)", Kind: connector.FieldString,
			Description: "Pre-fills the model param on transcribe/translate when omitted. e.g. Systran/faster-whisper-large-v3.",
		},
	}}
}

func (credSpec) Presets() []connector.Preset {
	return []connector.Preset{
		{
			Name:        "faster-whisper-server (local)",
			Description: "http://localhost:8000/v1 — Systran/faster-whisper-server default.",
			Values:      connector.Values{fAPIBase: "http://localhost:8000/v1"},
		},
		{
			Name:        "OpenAI Whisper",
			Description: "https://api.openai.com/v1 — official; needs api_key.",
			Values:      connector.Values{fAPIBase: "https://api.openai.com/v1"},
		},
		{
			Name:        "Custom",
			Description: "Enter the server URL manually.",
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

// Validate calls /models. Cheap and works against every implementation
// we've tested (faster-whisper-server, OpenAI, vLLM-Whisper).
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
	base := strings.TrimRight(c.Values.String(fAPIBase), "/")
	if base == "" {
		return nil, errors.New("api_base is empty")
	}
	hdr := stdhttp.Header{}
	if key := c.Values.String(fAPIKey); key != "" {
		hdr.Set("Authorization", "Bearer "+key)
	}
	return httptr.New(httptr.Config{
		BaseURL: base,
		Headers: hdr,
		// STT calls are usually quick (seconds), but a long file on a
		// CPU backend can run minutes. Match the llm connector's budget.
		Timeout: 10 * time.Minute,
	})
}
