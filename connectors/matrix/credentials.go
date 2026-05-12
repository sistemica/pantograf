package matrix

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
	fHomeserver  = "homeserver_url"
	fAccessToken = "access_token"
	fUserID      = "user_id"
	fLoginUser   = "login_user"
	fLoginPass   = "login_password"
)

type credSpec struct{}

func (credSpec) Kind() connector.AuthKind { return connector.AuthAPIKey }

func (credSpec) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{
			Name:        fHomeserver,
			Label:       "Homeserver URL",
			Kind:        connector.FieldString,
			Required:    true,
			Description: "Matrix Client-Server endpoint root, e.g. https://matrix.nextmind.team",
		},
		{
			Name:        fAccessToken,
			Label:       "Access token (recommended)",
			Kind:        connector.FieldSecret,
			Description: "Element → Settings → Help & About → Advanced → Access Token. If empty, login_user + login_password will be used to mint one at Validate.",
		},
		{
			Name:        fLoginUser,
			Label:       "Login username (fallback if no access_token)",
			Kind:        connector.FieldString,
			Description: "Bare local part — e.g. 'claudia' (without the @ or :server).",
		},
		{
			Name:        fLoginPass,
			Label:       "Login password (fallback)",
			Kind:        connector.FieldSecret,
			Description: "Used only at Validate to mint an access_token. Discarded after; not persisted.",
		},
		{
			Name:        fUserID,
			Label:       "User ID (auto-set on login)",
			Kind:        connector.FieldString,
			Description: "Filled by Validate. e.g. @claudia:matrix.nextmind.team",
		},
	}}
}

func (credSpec) Presets() []connector.Preset { return nil }

func (credSpec) Defaults(p connector.Values) connector.Values {
	out := make(connector.Values, len(p))
	for k, v := range p {
		out[k] = v
	}
	if u, ok := out[fHomeserver].(string); ok {
		out[fHomeserver] = strings.TrimRight(u, "/")
	}
	return out
}

// Validate establishes credentials, mutating the cred values to:
//   - mint access_token from login_user+login_password if not given
//   - record user_id from /account/whoami
//   - drop login_password (never persisted)
//
// Mutation works because Values is a map and the wizard's outer Credential
// is held by reference; the post-Validate save uses the (mutated) Values.
func (credSpec) Validate(ctx context.Context, c connector.Credential) error {
	v := c.Values
	hs := v.String(fHomeserver)
	if hs == "" {
		return errors.New("homeserver_url is required")
	}

	cli, err := httptr.New(httptr.Config{BaseURL: hs})
	if err != nil {
		return err
	}

	token := v.String(fAccessToken)
	if token == "" {
		// Login path.
		user := v.String(fLoginUser)
		pass := v.String(fLoginPass)
		if user == "" || pass == "" {
			return errors.New("either access_token, or login_user + login_password, is required")
		}
		body := map[string]any{
			"type":     "m.login.password",
			"user":     user,
			"password": pass,
			"device_id": "pantograf",
			"initial_device_display_name": "pantograf",
		}
		var loginResp struct {
			AccessToken string `json:"access_token"`
			UserID      string `json:"user_id"`
			HomeServer  string `json:"home_server"`
			DeviceID    string `json:"device_id"`
		}
		if err := cli.SendJSON(ctx, "POST", "/_matrix/client/v3/login", body, &loginResp); err != nil {
			return fmt.Errorf("login: %w", err)
		}
		if loginResp.AccessToken == "" {
			return errors.New("login: server returned no access_token")
		}
		v[fAccessToken] = loginResp.AccessToken
		v[fUserID] = loginResp.UserID
		// Wipe the password from the persisted credential.
		delete(v, fLoginPass)
		token = loginResp.AccessToken
	}

	// Confirm the token works (covers both freshly-minted and user-supplied).
	probeCli, err := httptr.New(httptr.Config{
		BaseURL: hs,
		Headers: stdhttp.Header{"Authorization": []string{"Bearer " + token}},
	})
	if err != nil {
		return err
	}
	var who struct {
		UserID   string `json:"user_id"`
		DeviceID string `json:"device_id"`
	}
	if err := probeCli.GetJSON(ctx, "/_matrix/client/v3/account/whoami", nil, &who); err != nil {
		return fmt.Errorf("whoami probe: %w", err)
	}
	if who.UserID != "" {
		v[fUserID] = who.UserID
		fmt.Printf("(as %s) ", who.UserID)
	}
	return nil
}

func apiClient(c connector.Credential) (*httptr.Client, error) {
	hs := strings.TrimRight(c.Values.String(fHomeserver), "/")
	token := c.Values.String(fAccessToken)
	if hs == "" || token == "" {
		return nil, errors.New("homeserver_url and access_token are required")
	}
	return httptr.New(httptr.Config{
		BaseURL: hs,
		Headers: stdhttp.Header{"Authorization": []string{"Bearer " + token}},
	})
}
