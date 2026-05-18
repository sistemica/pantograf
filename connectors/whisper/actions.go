package whisper

import (
	"context"
	"errors"

	"github.com/sistemica/pantograf/connector"
	httptr "github.com/sistemica/pantograf/transport/http"
)

// ── list-models ───────────────────────────────────────────────────────────

type listModelsAction struct{}

func (listModelsAction) Name() string             { return "list-models" }
func (listModelsAction) DisplayName() string      { return "List models" }
func (listModelsAction) Description() string      { return "Available STT models on this endpoint." }
func (listModelsAction) Schema() connector.Schema { return connector.Schema{} }

func (listModelsAction) Run(ctx context.Context, sess connector.Session, _ connector.Values) (any, error) {
	s := sess.(*session)
	var out map[string]any
	if err := s.http.GetJSON(ctx, "/models", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ── transcribe ────────────────────────────────────────────────────────────

type transcribeAction struct{}

func (transcribeAction) Name() string         { return "transcribe" }
func (transcribeAction) DisplayName() string  { return "Transcribe audio" }
func (transcribeAction) Description() string  { return "Speech-to-text. Multipart upload of an audio file to /audio/transcriptions." }
func (transcribeAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "model", Kind: connector.FieldString,
			Description: "Model id. Defaults to default_model from the credential, if set."},
		{Name: "audio", Kind: connector.FieldString, Required: true, IsPath: true,
			Description: "Local path to audio file (mp3/wav/m4a/ogg/webm/flac). Subject to PGF_ALLOWED_PATHS."},
		{Name: "language", Kind: connector.FieldString,
			Description: "ISO-639-1 hint (en, de, fr, ...). Empty = auto-detect."},
		{Name: "prompt", Kind: connector.FieldLongText,
			Description: "Optional context to prime the model (proper nouns, jargon)."},
		{Name: "temperature", Kind: connector.FieldString,
			Description: "0.0–1.0. Empty = backend default."},
		{Name: "vad_filter", Kind: connector.FieldBool, Default: false,
			Description: "faster-whisper-server extension: skip non-speech segments. OpenAI ignores."},
		{Name: "verbose", Kind: connector.FieldBool, Default: false,
			Description: "Request verbose_json: segments + word timestamps when the backend supports."},
		{Name: "raw", Kind: connector.FieldBool, Default: false,
			Description: "Return the full backend response instead of {text, ...}."},
	}}
}

func (a transcribeAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	model := resolveModel(s, params)
	audio := params.String("audio")
	if audio == "" {
		return nil, errors.New("audio is required")
	}
	if model == "" {
		return nil, errors.New("model is required (or set default_model in the credential)")
	}

	format := "json"
	if params.Bool("verbose") {
		format = "verbose_json"
	}
	fields := map[string]string{
		"model":           model,
		"response_format": format,
	}
	if l := params.String("language"); l != "" {
		fields["language"] = l
	}
	if p := params.String("prompt"); p != "" {
		fields["prompt"] = p
	}
	if t := params.String("temperature"); t != "" {
		fields["temperature"] = t
	}
	if params.Bool("vad_filter") {
		fields["vad_filter"] = "true"
	}

	files := []httptr.FileField{{FieldName: "file", Path: audio}}

	var resp map[string]any
	if err := s.http.PostMultipart(ctx, "/audio/transcriptions", fields, files, &resp); err != nil {
		return nil, err
	}
	if params.Bool("raw") {
		return resp, nil
	}
	return simplify(resp), nil
}

// ── translate ─────────────────────────────────────────────────────────────
//
// Whisper's built-in translate-to-English mode. The endpoint
// /audio/translations always emits English regardless of source language;
// there's no `language` field (only `prompt`, `temperature`, `model`).

type translateAction struct{}

func (translateAction) Name() string         { return "translate" }
func (translateAction) DisplayName() string  { return "Translate audio to English" }
func (translateAction) Description() string  { return "Speech-to-English-text. Source language auto-detected. Multipart upload to /audio/translations." }
func (translateAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "model", Kind: connector.FieldString,
			Description: "Model id. Defaults to default_model from the credential."},
		{Name: "audio", Kind: connector.FieldString, Required: true, IsPath: true,
			Description: "Local path to audio file. Subject to PGF_ALLOWED_PATHS."},
		{Name: "prompt", Kind: connector.FieldLongText,
			Description: "Optional priming context (English)."},
		{Name: "temperature", Kind: connector.FieldString,
			Description: "0.0–1.0. Empty = backend default."},
		{Name: "verbose", Kind: connector.FieldBool, Default: false,
			Description: "Request verbose_json."},
		{Name: "raw", Kind: connector.FieldBool, Default: false,
			Description: "Return the full backend response."},
	}}
}

func (a translateAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	model := resolveModel(s, params)
	audio := params.String("audio")
	if audio == "" {
		return nil, errors.New("audio is required")
	}
	if model == "" {
		return nil, errors.New("model is required (or set default_model in the credential)")
	}

	format := "json"
	if params.Bool("verbose") {
		format = "verbose_json"
	}
	fields := map[string]string{
		"model":           model,
		"response_format": format,
	}
	if p := params.String("prompt"); p != "" {
		fields["prompt"] = p
	}
	if t := params.String("temperature"); t != "" {
		fields["temperature"] = t
	}

	files := []httptr.FileField{{FieldName: "file", Path: audio}}

	var resp map[string]any
	if err := s.http.PostMultipart(ctx, "/audio/translations", fields, files, &resp); err != nil {
		return nil, err
	}
	if params.Bool("raw") {
		return resp, nil
	}
	return simplify(resp), nil
}

// resolveModel returns the action param if set, else the credential's
// default_model. Empty if neither is supplied.
func resolveModel(s *session, params connector.Values) string {
	if m := params.String("model"); m != "" {
		return m
	}
	return s.cred.Values.String(fDefaultModel)
}

// simplify picks the common fields most consumers want.
func simplify(resp map[string]any) map[string]any {
	out := map[string]any{}
	if t, ok := resp["text"].(string); ok {
		out["text"] = t
	}
	for _, k := range []string{"language", "duration", "segments", "words"} {
		if v, ok := resp[k]; ok {
			out[k] = v
		}
	}
	return out
}
