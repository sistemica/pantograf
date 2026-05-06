package telegram

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/sistemica/pantograf/connector"
	httptr "github.com/sistemica/pantograf/transport/http"
)

const (
	fToken         = "bot_token"
	fDefaultChatID = "default_chat_id"
	fAPIBase       = "api_base"
)

type credSpec struct{}

func (credSpec) Kind() connector.AuthKind { return connector.AuthAPIKey }

func (credSpec) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{
			Name: fToken, Label: "Bot token", Kind: connector.FieldSecret, Required: true,
			Description: "From @BotFather. Looks like 1234567890:ABC...",
		},
		{
			Name: fDefaultChatID, Label: "Default chat ID (optional)", Kind: connector.FieldString,
			Description: "Numeric ID or @channelname. If set, send_* actions can omit chat_id.",
		},
		{
			Name: fAPIBase, Label: "API base URL", Kind: connector.FieldString,
			Default: "https://api.telegram.org",
			Description: "Override only for self-hosted Bot API servers.",
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

// Validate calls getMe — Telegram's standard health check. Returns the bot
// username on success so the wizard can echo it for confirmation.
func (credSpec) Validate(ctx context.Context, c connector.Credential) error {
	cli, err := apiClient(c)
	if err != nil {
		return err
	}
	var resp struct {
		OK     bool   `json:"ok"`
		Result botUser `json:"result"`
		Description string `json:"description"`
	}
	if err := cli.GetJSON(ctx, "/getMe", url.Values{}, &resp); err != nil {
		return err
	}
	if !resp.OK {
		desc := resp.Description
		if desc == "" {
			desc = "Telegram returned ok=false"
		}
		return errors.New(desc)
	}
	fmt.Printf("(bot @%s) ", resp.Result.Username)
	return nil
}

type botUser struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

// apiClient builds a *transport/http.Client whose BaseURL embeds the bot
// token, so every request is automatically authenticated.
func apiClient(c connector.Credential) (*httptr.Client, error) {
	token := c.Values.String(fToken)
	if token == "" {
		return nil, errors.New("bot_token is empty")
	}
	apiBase := c.Values.String(fAPIBase)
	if apiBase == "" {
		apiBase = "https://api.telegram.org"
	}
	return httptr.New(httptr.Config{
		BaseURL: fmt.Sprintf("%s/bot%s", apiBase, token),
	})
}
