package bunny

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

const defaultAPIBase = "https://api.bunny.net"

type credSpec struct{}

func (credSpec) Kind() connector.AuthKind { return connector.AuthAPIKey }

func (credSpec) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{
			Name: fAPIKey, Label: "Bunny.net API key", Kind: connector.FieldSecret, Required: true,
			Description: "From https://dash.bunny.net/account/api-key. Sent as `AccessKey: <key>` header (not Bearer).",
		},
		{
			Name: fAPIBase, Label: "API base URL", Kind: connector.FieldString,
			Default: defaultAPIBase,
			Description: "Override only for testing. Default https://api.bunny.net.",
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

// Validate calls GET /dnszone with perPage=5 — cheap, confirms auth +
// reachability, and reports how many zones the account holds.
func (credSpec) Validate(ctx context.Context, c connector.Credential) error {
	cli, err := apiClient(c)
	if err != nil {
		return err
	}
	var resp zonePage
	if err := cli.GetJSON(ctx, "/dnszone?page=1&perPage=5", nil, &resp); err != nil {
		return fmt.Errorf("dnszone probe: %w", err)
	}
	fmt.Printf("(%d zones) ", resp.TotalItems)
	return nil
}

func apiClient(c connector.Credential) (*httptr.Client, error) {
	key := c.Values.String(fAPIKey)
	if key == "" {
		return nil, errors.New("api_key is required")
	}
	base := c.Values.String(fAPIBase)
	if base == "" {
		base = defaultAPIBase
	}
	return httptr.New(httptr.Config{
		BaseURL: base,
		Headers: stdhttp.Header{
			"AccessKey": []string{key},
		},
	})
}
