package synapse

import (
	"context"
	"errors"
	"fmt"
	stdhttp "net/http"
	"net/url"
	"strings"

	"github.com/sistemica/pantograf/connector"
	httptr "github.com/sistemica/pantograf/transport/http"
)

const (
	fHomeserver  = "homeserver_url"
	fAccessToken = "access_token"
	fAdminUserID = "admin_user_id"
)

type credSpec struct{}

func (credSpec) Kind() connector.AuthKind { return connector.AuthAPIKey }

func (credSpec) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{
			Name: fHomeserver, Label: "Homeserver URL", Kind: connector.FieldString, Required: true,
			Description: "Synapse C-S endpoint root, e.g. https://chat.sistemica.cloud. Same value as the matching matrix connector instance.",
		},
		{
			Name: fAccessToken, Label: "Admin access token", Kind: connector.FieldSecret, Required: true,
			Description: "Token belonging to an admin-flagged user. Get one from Element (Settings → Help → Advanced → Access Token) — the same token used by your matrix/<name> instance works IF that user has admin. Validate probes admin status at connect and refuses non-admin tokens.",
		},
		{
			Name: fAdminUserID, Label: "Admin user ID (auto-set)", Kind: connector.FieldString,
			Description: "Filled by Validate from /_matrix/client/v3/account/whoami. Used internally; safe to leave blank during connect.",
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

// Validate does three checks in order — each layer fails loudly with a
// clear remedy:
//   1. Whoami via the C-S API — confirms the token + URL are usable.
//   2. Server-version via /_synapse/admin — confirms the homeserver IS
//      Synapse (Dendrite/Conduit return 404 here).
//   3. /_synapse/admin/v1/users/{whoami_id}/admin — confirms the user
//      is admin (returns {"admin":true|false}). Refuses non-admin.
func (credSpec) Validate(ctx context.Context, c connector.Credential) error {
	v := c.Values
	hs := strings.TrimRight(v.String(fHomeserver), "/")
	token := v.String(fAccessToken)
	if hs == "" || token == "" {
		return errors.New("homeserver_url and access_token are required")
	}

	// (1) whoami via the standard C-S API — discovers user id + sanity-checks the token.
	cli, err := httptr.New(httptr.Config{
		BaseURL: hs,
		Headers: stdhttp.Header{"Authorization": []string{"Bearer " + token}},
	})
	if err != nil {
		return err
	}
	var who struct {
		UserID string `json:"user_id"`
	}
	if err := cli.GetJSON(ctx, "/_matrix/client/v3/account/whoami", nil, &who); err != nil {
		return fmt.Errorf("whoami: %w", err)
	}
	if who.UserID == "" {
		return errors.New("whoami: empty user_id (token invalid?)")
	}
	v[fAdminUserID] = who.UserID

	// (2) confirm Synapse — Dendrite/Conduit return 404 on /_synapse paths.
	var ver struct {
		ServerVersion string `json:"server_version"`
		PythonVersion string `json:"python_version,omitempty"`
	}
	if err := cli.GetJSON(ctx, "/_synapse/admin/v1/server_version", nil, &ver); err != nil {
		return fmt.Errorf("/_synapse/admin/v1/server_version: %w (homeserver may not be Synapse)", err)
	}

	// (3) admin probe — returns {"admin":bool}.
	var adm struct {
		Admin bool `json:"admin"`
	}
	path := "/_synapse/admin/v1/users/" + url.PathEscape(who.UserID) + "/admin"
	if err := cli.GetJSON(ctx, path, nil, &adm); err != nil {
		return fmt.Errorf("admin probe: %w (token doesn't carry admin privileges?)", err)
	}
	if !adm.Admin {
		return fmt.Errorf("%s is not an admin on this homeserver — grant admin first (e.g. `UPDATE users SET admin=1 WHERE name='%s'` on the Synapse db) then re-run", who.UserID, who.UserID)
	}

	fmt.Printf("(synapse %s, admin %s) ", ver.ServerVersion, who.UserID)
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
