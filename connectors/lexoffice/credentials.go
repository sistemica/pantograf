package lexoffice

import (
	"context"
	"errors"
	"fmt"
	stdhttp "net/http"

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
			Description: "Lexoffice → Einstellungen → Öffentliche API. Sent as `Authorization: Bearer <key>`.",
		},
		{
			Name:    fAPIBase,
			Label:   "API base URL",
			Kind:    connector.FieldString,
			Default: "https://api.lexware.io",
			Description: "Default since the rebrand; legacy api.lexoffice.io was retired Dec 2025.",
		},
	}}
}

func (credSpec) Presets() []connector.Preset { return nil }

func (credSpec) Defaults(p connector.Values) connector.Values {
	out := make(connector.Values, len(p))
	for k, v := range p {
		out[k] = v
	}
	return out
}

// Validate calls /v1/profile — cheap, returns the bound organisation. Echos
// the company name into the wizard output for confirmation.
func (credSpec) Validate(ctx context.Context, c connector.Credential) error {
	cli, err := apiClient(c)
	if err != nil {
		return err
	}
	var profile map[string]any
	if err := cli.GetJSON(ctx, "/v1/profile", nil, &profile); err != nil {
		return fmt.Errorf("profile probe: %w", err)
	}
	if name, ok := profile["companyName"].(string); ok && name != "" {
		fmt.Printf("(company: %s) ", name)
	}
	return nil
}

func apiClient(c connector.Credential) (*httptr.Client, error) {
	apiKey := c.Values.String(fAPIKey)
	if apiKey == "" {
		return nil, errors.New("api_key is empty")
	}
	base := c.Values.String(fAPIBase)
	if base == "" {
		base = "https://api.lexware.io"
	}
	// Don't bake `Accept: application/json` here — the JSON helpers
	// (GetJSON / PostJSON / ...) set it themselves when needed; binary
	// downloads via Do() must NOT have it (Lexware returns base64-in-JSON
	// for /v1/files/{id} when Accept is JSON, but native bytes when blank).
	return httptr.New(httptr.Config{
		BaseURL: base,
		Headers: stdhttp.Header{
			"Authorization": []string{"Bearer " + apiKey},
		},
	})
}
