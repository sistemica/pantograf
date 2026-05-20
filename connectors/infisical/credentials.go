package infisical

import (
	"context"
	"errors"
	"fmt"
	stdhttp "net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sistemica/pantograf/connector"
	httptr "github.com/sistemica/pantograf/transport/http"
)

const (
	fAPIBase            = "api_base"
	fClientID           = "client_id"
	fClientSecret       = "client_secret"
	fDefaultProjectID   = "default_project_id"
	fDefaultEnvironment = "default_environment"
)

const defaultAPIBase = "https://app.infisical.com"

type credSpec struct{}

func (credSpec) Kind() connector.AuthKind { return connector.AuthAPIKey }

func (credSpec) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{
			Name: fAPIBase, Label: "API base URL", Kind: connector.FieldString, Required: true,
			Default: defaultAPIBase,
			Description: "Without /api suffix. e.g. https://app.infisical.com, https://eu.infisical.com, or your self-hosted https://infisical.example.com.",
		},
		{
			Name: fClientID, Label: "Universal Auth Client ID", Kind: connector.FieldString, Required: true,
			Description: "From Infisical → Organization → Identities → <identity> → Universal Auth → Client ID. Pair this with the matching client_secret.",
		},
		{
			Name: fClientSecret, Label: "Universal Auth Client Secret", Kind: connector.FieldSecret, Required: true,
			Description: "From the same identity. The secret can only be displayed at creation — generate a new one if you lost it.",
		},
		{
			Name: fDefaultProjectID, Label: "Default project ID (optional)", Kind: connector.FieldString,
			Description: "Pre-fills the project_id param on every action when omitted. e.g. 6f3a... Find it in the project's URL or settings.",
		},
		{
			Name: fDefaultEnvironment, Label: "Default environment slug (optional)", Kind: connector.FieldString,
			Default: "dev",
			Description: "Pre-fills the environment param. Common values: dev, staging, prod.",
		},
	}}
}

func (credSpec) Presets() []connector.Preset {
	return []connector.Preset{
		{
			Name: "Infisical Cloud (US)",
			Description: "Default https://app.infisical.com.",
			Values: connector.Values{fAPIBase: defaultAPIBase},
		},
		{
			Name: "Infisical Cloud (EU)",
			Description: "https://eu.infisical.com — EU data-residency tier.",
			Values: connector.Values{fAPIBase: "https://eu.infisical.com"},
		},
		{
			Name: "Self-hosted",
			Description: "Enter the base URL of your own Infisical instance.",
			Values: connector.Values{},
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

// Validate logs in via Universal Auth — confirms api_base reachable +
// identity exists + secret correct. Doesn't probe any specific project
// or workspace; subsequent action calls will surface scope errors when
// the agent reaches for something the identity can't see.
func (credSpec) Validate(ctx context.Context, c connector.Credential) error {
	_, err := authedClient(ctx, c)
	if err != nil {
		return err
	}
	fmt.Printf("(logged in as Universal Auth identity) ")
	return nil
}

// authedClient performs Universal Auth login and returns an http.Client
// whose default headers carry the resulting Bearer token. Single call
// site used by both Validate and Open.
func authedClient(ctx context.Context, c connector.Credential) (*httptr.Client, error) {
	base := strings.TrimRight(c.Values.String(fAPIBase), "/")
	if base == "" {
		base = defaultAPIBase
	}
	clientID := c.Values.String(fClientID)
	clientSecret := c.Values.String(fClientSecret)
	if clientID == "" || clientSecret == "" {
		return nil, errors.New("client_id and client_secret are required")
	}

	// Login uses form-urlencoded body — separate one-shot client so we
	// don't accidentally bake the login URL into the long-lived client.
	loginCli, err := httptr.New(httptr.Config{
		BaseURL: base,
		Timeout: 30 * time.Second,
	})
	if err != nil {
		return nil, err
	}

	form := url.Values{}
	form.Set("clientId", clientID)
	form.Set("clientSecret", clientSecret)

	var resp loginResponse
	if err := loginCli.PostForm(ctx, "/api/v1/auth/universal-auth/login", form, &resp); err != nil {
		return nil, fmt.Errorf("universal-auth login: %w", err)
	}
	if resp.AccessToken == "" {
		return nil, errors.New("universal-auth login: empty access token in response")
	}

	return httptr.New(httptr.Config{
		BaseURL: base,
		Headers: stdhttp.Header{
			"Authorization": []string{"Bearer " + resp.AccessToken},
		},
	})
}

type loginResponse struct {
	AccessToken       string `json:"accessToken"`
	ExpiresIn         int    `json:"expiresIn"`
	AccessTokenMaxTTL int    `json:"accessTokenMaxTTL"`
	TokenType         string `json:"tokenType"`
}
