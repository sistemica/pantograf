package telegram

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/sistemica/pantograf/connector"
	httptr "github.com/sistemica/pantograf/transport/http"
)

// apiResp is the envelope every Bot API call returns.
type apiResp struct {
	OK          bool   `json:"ok"`
	Result      any    `json:"result,omitempty"`
	ErrorCode   int    `json:"error_code,omitempty"`
	Description string `json:"description,omitempty"`
}

// post is the per-action wrapper: send a typed body, decode `result` into
// out (if non-nil), surface `description` as the error on ok=false.
func post(ctx context.Context, cli *httptr.Client, method string, body, out any) error {
	wrap := struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		ErrorCode   int             `json:"error_code"`
		Result      any             `json:"result"`
	}{Result: out}
	if err := cli.SendJSON(ctx, "POST", "/"+method, body, &wrap); err != nil {
		return err
	}
	if !wrap.OK {
		return fmt.Errorf("telegram %s: %s (error_code=%d)", method, wrap.Description, wrap.ErrorCode)
	}
	return nil
}

func postMultipart(ctx context.Context, cli *httptr.Client, method string, fields map[string]string, files []httptr.FileField, out any) error {
	wrap := struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		ErrorCode   int    `json:"error_code"`
		Result      any    `json:"result"`
	}{Result: out}
	if err := cli.PostMultipart(ctx, "/"+method, fields, files, &wrap); err != nil {
		return err
	}
	if !wrap.OK {
		return fmt.Errorf("telegram %s: %s (error_code=%d)", method, wrap.Description, wrap.ErrorCode)
	}
	return nil
}

func resolveChatID(s *session, params connector.Values) (string, error) {
	chat := params.String("chat_id")
	if chat == "" {
		chat = s.cred.Values.String(fDefaultChatID)
	}
	if chat == "" {
		return "", errors.New("chat_id is required (or set default_chat_id in the credential)")
	}
	return chat, nil
}

// isURL reports whether s looks like an absolute http(s) URL — the tell for
// "Telegram should fetch this remotely" vs "we should upload from disk".
func isURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// ── get_me ────────────────────────────────────────────────────────────────

type getMeAction struct{}

func (getMeAction) Name() string         { return "get-me" }
func (getMeAction) DisplayName() string  { return "Get bot info" }
func (getMeAction) Description() string  { return "Returns the bot's identity (id, username, first_name)." }
func (getMeAction) Schema() connector.Schema {
	return connector.Schema{}
}

func (getMeAction) Run(ctx context.Context, sess connector.Session, _ connector.Values) (any, error) {
	s, ok := sess.(*session)
	if !ok {
		return nil, errors.New("get_me: wrong session type")
	}
	var bot botUser
	if err := post(ctx, s.http, "getMe", struct{}{}, &bot); err != nil {
		return nil, err
	}
	return bot, nil
}

// ── send_message ──────────────────────────────────────────────────────────

type sendMessageAction struct{}

func (sendMessageAction) Name() string         { return "send-message" }
func (sendMessageAction) DisplayName() string  { return "Send message" }
func (sendMessageAction) Description() string  { return "Send a text message to a chat." }
func (sendMessageAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "chat_id", Label: "Chat ID", Kind: connector.FieldString,
			Description: "Numeric ID or @channelname. Defaults to the credential's default_chat_id."},
		{Name: "text", Label: "Message text", Kind: connector.FieldLongText, Required: true},
		{Name: "parse_mode", Label: "Parse mode", Kind: connector.FieldEnum,
			Options: []connector.EnumOption{
				{Value: "", Label: "(none)"},
				{Value: "HTML", Label: "HTML"},
				{Value: "Markdown", Label: "Markdown (legacy)"},
				{Value: "MarkdownV2", Label: "MarkdownV2"},
			}},
		{Name: "disable_notification", Label: "Silent", Kind: connector.FieldBool, Default: false},
		{Name: "reply_to_message_id", Label: "Reply to message ID", Kind: connector.FieldInt},
	}}
}

func (a sendMessageAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s, ok := sess.(*session)
	if !ok {
		return nil, errors.New("send_message: wrong session type")
	}
	params = params.WithDefaults(a.Schema())
	chat, err := resolveChatID(s, params)
	if err != nil {
		return nil, err
	}
	text := params.String("text")
	if text == "" {
		return nil, errors.New("text is required")
	}
	body := map[string]any{
		"chat_id":              chat,
		"text":                 text,
		"disable_notification": params.Bool("disable_notification"),
	}
	if pm := params.String("parse_mode"); pm != "" {
		body["parse_mode"] = pm
	}
	if rid := params.Int("reply_to_message_id"); rid > 0 {
		body["reply_to_message_id"] = rid
	}
	var msg map[string]any
	if err := post(ctx, s.http, "sendMessage", body, &msg); err != nil {
		return nil, err
	}
	return msg, nil
}

// ── send_photo ────────────────────────────────────────────────────────────

type sendPhotoAction struct{}

func (sendPhotoAction) Name() string         { return "send-photo" }
func (sendPhotoAction) DisplayName() string  { return "Send photo" }
func (sendPhotoAction) Description() string  { return "Send a photo. Accepts a local file path or an http(s) URL." }
func (sendPhotoAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "chat_id", Label: "Chat ID", Kind: connector.FieldString},
		{Name: "photo", Label: "File path or URL", Kind: connector.FieldString, Required: true},
		{Name: "caption", Label: "Caption", Kind: connector.FieldString},
		{Name: "parse_mode", Label: "Parse mode", Kind: connector.FieldString},
	}}
}

func (a sendPhotoAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s, ok := sess.(*session)
	if !ok {
		return nil, errors.New("send_photo: wrong session type")
	}
	params = params.WithDefaults(a.Schema())
	return sendMedia(ctx, s, params, "sendPhoto", "photo")
}

// ── send_document ─────────────────────────────────────────────────────────

type sendDocumentAction struct{}

func (sendDocumentAction) Name() string         { return "send-document" }
func (sendDocumentAction) DisplayName() string  { return "Send document" }
func (sendDocumentAction) Description() string  { return "Send any file as a document. Accepts a local file path or an http(s) URL." }
func (sendDocumentAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "chat_id", Label: "Chat ID", Kind: connector.FieldString},
		{Name: "document", Label: "File path or URL", Kind: connector.FieldString, Required: true},
		{Name: "caption", Label: "Caption", Kind: connector.FieldString},
		{Name: "parse_mode", Label: "Parse mode", Kind: connector.FieldString},
	}}
}

func (a sendDocumentAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s, ok := sess.(*session)
	if !ok {
		return nil, errors.New("send_document: wrong session type")
	}
	params = params.WithDefaults(a.Schema())
	return sendMedia(ctx, s, params, "sendDocument", "document")
}

// sendMedia is the shared implementation for sendPhoto / sendDocument /
// future sendVideo etc. mediaField names the form field that carries the
// file ("photo", "document"). If the caller's value is a URL we send a
// JSON body with the URL string; otherwise we upload the file via multipart.
func sendMedia(ctx context.Context, s *session, params connector.Values, method, mediaField string) (any, error) {
	chat, err := resolveChatID(s, params)
	if err != nil {
		return nil, err
	}
	media := params.String(mediaField)
	if media == "" {
		return nil, fmt.Errorf("%s is required", mediaField)
	}
	caption := params.String("caption")
	parseMode := params.String("parse_mode")

	var msg map[string]any

	if isURL(media) {
		body := map[string]any{
			"chat_id":    chat,
			mediaField:   media,
		}
		if caption != "" {
			body["caption"] = caption
		}
		if parseMode != "" {
			body["parse_mode"] = parseMode
		}
		if err := post(ctx, s.http, method, body, &msg); err != nil {
			return nil, err
		}
		return msg, nil
	}

	if _, err := os.Stat(media); err != nil {
		return nil, fmt.Errorf("file %s: %w", media, err)
	}
	fields := map[string]string{
		"chat_id": chat,
	}
	if caption != "" {
		fields["caption"] = caption
	}
	if parseMode != "" {
		fields["parse_mode"] = parseMode
	}
	files := []httptr.FileField{{FieldName: mediaField, Path: media}}
	if err := postMultipart(ctx, s.http, method, fields, files, &msg); err != nil {
		return nil, err
	}
	return msg, nil
}

// silence unused
var _ apiResp
