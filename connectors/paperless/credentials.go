package paperless

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
	fURL      = "url"
	fToken    = "token"
	fUsername = "username"
	fPassword = "password"
)

type credSpec struct{}

func (credSpec) Kind() connector.AuthKind { return connector.AuthAPIKey }

func (credSpec) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{
			Name: fURL, Label: "Base URL", Kind: connector.FieldString, Required: true,
			Description: "Paperless-ngx root, e.g. https://paperless.example.org — no trailing slash, no /api suffix.",
		},
		{
			Name: fToken, Label: "API token", Kind: connector.FieldSecret,
			Description: "Personal API token (Settings → My Profile → API Token). Leave blank to derive one from username+password at connect.",
		},
		{
			Name: fUsername, Label: "Username", Kind: connector.FieldString,
			Description: "Only needed when no token is given — exchanged for a token at connect via /api/token/, then discarded is NOT done (kept for re-login).",
		},
		{
			Name: fPassword, Label: "Password", Kind: connector.FieldSecret,
			Description: "Paired with username for the token exchange. Encrypted at rest.",
		},
	}}
}

func (credSpec) Presets() []connector.Preset { return nil }

func (credSpec) Defaults(p connector.Values) connector.Values {
	out := make(connector.Values, len(p))
	for k, v := range p {
		out[k] = v
	}
	if u, ok := out[fURL].(string); ok {
		out[fURL] = strings.TrimRight(strings.TrimSpace(u), "/")
	}
	return out
}

// Validate resolves a usable token (exchanging username+password when no
// token was supplied) and probes the documents endpoint, reporting how many
// documents the account can see.
func (credSpec) Validate(ctx context.Context, c connector.Credential) error {
	cli, err := apiClient(ctx, c)
	if err != nil {
		return err
	}
	var page listPage
	if err := cli.GetJSON(ctx, "/api/documents/", url.Values{"page_size": {"1"}}, &page); err != nil {
		return fmt.Errorf("probe /api/documents/: %w", err)
	}
	fmt.Printf("(%d documents) ", page.Count)
	return nil
}

// apiClient returns an http client whose default header carries the
// Paperless token. When the credential has no token but does have
// username+password, it first exchanges them at /api/token/ and writes the
// resulting token back into the credential values so it persists. Single
// call site for both Validate and Open (mirrors infisical/matrix).
func apiClient(ctx context.Context, c connector.Credential) (*httptr.Client, error) {
	base := strings.TrimRight(strings.TrimSpace(c.Values.String(fURL)), "/")
	if base == "" {
		return nil, errors.New("url is required")
	}
	token := strings.TrimSpace(c.Values.String(fToken))

	if token == "" {
		user := c.Values.String(fUsername)
		pass := c.Values.String(fPassword)
		if user == "" || pass == "" {
			return nil, errors.New("provide either token, or username + password")
		}
		t, err := exchangeToken(ctx, base, user, pass)
		if err != nil {
			return nil, err
		}
		token = t
		c.Values[fToken] = token // persist so later Opens skip the exchange
	}

	return httptr.New(httptr.Config{
		BaseURL: base,
		Headers: stdhttp.Header{"Authorization": []string{"Token " + token}},
		Timeout: 60 * time.Second,
	})
}

// exchangeToken POSTs username+password to /api/token/ and returns the token.
func exchangeToken(ctx context.Context, base, user, pass string) (string, error) {
	loginCli, err := httptr.New(httptr.Config{BaseURL: base, Timeout: 30 * time.Second})
	if err != nil {
		return "", err
	}
	var resp struct {
		Token string `json:"token"`
	}
	body := map[string]string{"username": user, "password": pass}
	if err := loginCli.SendJSON(ctx, stdhttp.MethodPost, "/api/token/", body, &resp); err != nil {
		return "", fmt.Errorf("token exchange: %w", err)
	}
	if resp.Token == "" {
		return "", errors.New("token exchange: empty token in response (bad credentials?)")
	}
	return resp.Token, nil
}
