package telegram

import (
	"context"
	"errors"
	"strings"

	"github.com/sistemica/pantograf/connector"
)

// ── set_webhook ───────────────────────────────────────────────────────────
//
// Wraps Telegram's setWebhook — points the bot at an HTTPS URL where the
// runtime hosts a generic webhook receiver (the `webhook` connector).
// Telegram delivers updates as POSTs to that URL until delete_webhook is
// called (or another setWebhook with a new URL).

type setWebhookAction struct{}

func (setWebhookAction) Name() string         { return "set_webhook" }
func (setWebhookAction) DisplayName() string  { return "Set webhook URL" }
func (setWebhookAction) Description() string  { return "Tell Telegram to POST updates to the given HTTPS URL. Mutually exclusive with the messages polling trigger." }
func (setWebhookAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "url", Label: "Webhook URL (HTTPS)", Kind: connector.FieldString, Required: true,
			Description: "Typically https://your.host/webhook/<name>/incoming served by the webhook connector."},
		{Name: "secret_token", Label: "Secret token (optional)", Kind: connector.FieldString,
			Description: "Telegram includes this in X-Telegram-Bot-Api-Secret-Token; the receiver verifies it."},
		{Name: "allowed_updates", Label: "Filter update kinds", Kind: connector.FieldStringList,
			Default: "message,edited_message,channel_post,callback_query"},
		{Name: "drop_pending_updates", Label: "Drop queued updates", Kind: connector.FieldBool, Default: false},
	}}
}

func (a setWebhookAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s, ok := sess.(*session)
	if !ok {
		return nil, errors.New("set_webhook: wrong session type")
	}
	params = params.WithDefaults(a.Schema())
	urlStr := strings.TrimSpace(params.String("url"))
	if urlStr == "" {
		return nil, errors.New("url is required")
	}
	body := map[string]any{
		"url":                  urlStr,
		"drop_pending_updates": params.Bool("drop_pending_updates"),
	}
	if allowed := splitCSV(params.StringList("allowed_updates")); len(allowed) > 0 {
		body["allowed_updates"] = allowed
	}
	if tok := strings.TrimSpace(params.String("secret_token")); tok != "" {
		body["secret_token"] = tok
	}
	var ok2 bool
	if err := post(ctx, s.http, "setWebhook", body, &ok2); err != nil {
		return nil, err
	}
	return map[string]any{"set": true, "url": urlStr}, nil
}

// ── delete_webhook ────────────────────────────────────────────────────────

type deleteWebhookAction struct{}

func (deleteWebhookAction) Name() string         { return "delete_webhook" }
func (deleteWebhookAction) DisplayName() string  { return "Delete webhook" }
func (deleteWebhookAction) Description() string  { return "Clear the webhook so the bot can be polled or pointed elsewhere." }
func (deleteWebhookAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "drop_pending_updates", Label: "Drop queued updates", Kind: connector.FieldBool, Default: false},
	}}
}

func (a deleteWebhookAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s, ok := sess.(*session)
	if !ok {
		return nil, errors.New("delete_webhook: wrong session type")
	}
	params = params.WithDefaults(a.Schema())
	body := map[string]any{
		"drop_pending_updates": params.Bool("drop_pending_updates"),
	}
	var ok2 bool
	if err := post(ctx, s.http, "deleteWebhook", body, &ok2); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}

// ── get_webhook_info ──────────────────────────────────────────────────────

type getWebhookInfoAction struct{}

func (getWebhookInfoAction) Name() string         { return "get_webhook_info" }
func (getWebhookInfoAction) DisplayName() string  { return "Get webhook info" }
func (getWebhookInfoAction) Description() string  { return "Return current webhook URL and pending update count." }
func (getWebhookInfoAction) Schema() connector.Schema { return connector.Schema{} }

func (getWebhookInfoAction) Run(ctx context.Context, sess connector.Session, _ connector.Values) (any, error) {
	s, ok := sess.(*session)
	if !ok {
		return nil, errors.New("get_webhook_info: wrong session type")
	}
	var info map[string]any
	if err := post(ctx, s.http, "getWebhookInfo", struct{}{}, &info); err != nil {
		return nil, err
	}
	return info, nil
}
