package youtrack

import (
	"context"
	"errors"
	"fmt"
	stdhttp "net/http"
	"strings"

	"github.com/sistemica/pantograf/connector"
	httptr "github.com/sistemica/pantograf/transport/http"
)

const (
	fToken   = "token"
	fBaseURL = "base_url"
)

type credSpec struct{}

func (credSpec) Kind() connector.AuthKind { return connector.AuthAPIKey }

func (credSpec) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{
			Name:        fToken,
			Label:       "Permanent token",
			Kind:        connector.FieldSecret,
			Required:    true,
			Description: "From YouTrack → Profile → Authentication → New token. Looks like 'perm-...' (or perm-base64.NDEtMA==.suffix). Sent as `Authorization: Bearer <token>`.",
		},
		{
			Name:        fBaseURL,
			Label:       "YouTrack base URL",
			Kind:        connector.FieldString,
			Required:    true,
			Description: "e.g. https://tasks.sistemica.cloud — without trailing /api.",
		},
	}}
}

func (credSpec) Presets() []connector.Preset { return nil }

func (credSpec) Defaults(p connector.Values) connector.Values {
	out := make(connector.Values, len(p))
	for k, v := range p {
		out[k] = v
	}
	// Strip trailing slash on base_url for predictable URL composition.
	if u, ok := out[fBaseURL].(string); ok {
		out[fBaseURL] = strings.TrimRight(u, "/")
	}
	return out
}

// Validate hits /api/users/me to confirm the token works and the base URL
// resolves. Cheap call, returns the bound user — printed back so the user
// can verify they connected as the right account.
func (credSpec) Validate(ctx context.Context, c connector.Credential) error {
	cli, err := apiClient(c)
	if err != nil {
		return err
	}
	var me struct {
		Login    string `json:"login"`
		FullName string `json:"fullName"`
		Email    string `json:"email"`
	}
	if err := cli.GetJSON(ctx, "/api/users/me?fields=login,fullName,email", nil, &me); err != nil {
		return fmt.Errorf("users/me probe: %w", err)
	}
	if me.Login != "" {
		fmt.Printf("(as %s)", me.Login)
		if me.FullName != "" {
			fmt.Printf(" %s", me.FullName)
		}
		fmt.Print(" ")
	}
	return nil
}

func apiClient(c connector.Credential) (*httptr.Client, error) {
	token := c.Values.String(fToken)
	if token == "" {
		return nil, errors.New("token is empty")
	}
	base := strings.TrimRight(c.Values.String(fBaseURL), "/")
	if base == "" {
		return nil, errors.New("base_url is empty")
	}
	return httptr.New(httptr.Config{
		BaseURL: base,
		Headers: stdhttp.Header{
			"Authorization": []string{"Bearer " + token},
		},
	})
}
